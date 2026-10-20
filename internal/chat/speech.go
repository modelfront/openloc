package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/ai"
	"openloc/internal/auth"
	"openloc/internal/file"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// Voice for guided tours: a tour card's text read aloud by
// OpenAI text-to-speech, on the OpenAI key the organization picked for chat.
// Any member may use it, like the tours themselves. Audio of saved playbook
// steps is cached in storage under one key for every tenant — that text ships
// in the binary and carries no organization data. Chat-made steps can name
// the organization's items, so they are never cached.

const (
	speechModel = "gpt-4o-mini-tts"
	speechVoice = "cedar"
	// speechInstructions sets the delivery, not the words.
	speechInstructions  = "You are a friendly product guide with showing someone around an app"
	maxSpeechAudioBytes = 5 << 20
	speechTimeout       = 30 * time.Second
)

// speechText is what a step says: its spoken line, else its card body, with
// the pronunciation fixes applied. The title is not read — alone it sounds
// like a label, not a sentence.
func speechText(say, body string) string {
	text := strings.TrimSpace(say)
	if text == "" {
		text = strings.TrimSpace(body)
	}
	return pronounce(text)
}

// savedSpeech holds the spoken text of every saved tour step.
var savedSpeech = func() map[string]bool {
	out := map[string]bool{}
	for _, pb := range playbooks {
		for _, s := range pb.Tour {
			out[speechText(s.Say, s.Body)] = true
		}
	}
	return out
}()

func speechCacheKey(text string) string {
	sum := sha256.Sum256([]byte(speechModel + "|" + speechVoice + "|" + speechInstructions + "|" + text))
	return "tour-speech/" + hex.EncodeToString(sum[:]) + ".mp3"
}

// speechKey returns the organization's chat OpenAI key when it can speak.
// Azure serves speech per deployment, on a path this call does not build, so
// an Azure endpoint has no voice.
//
// In the demo organization, which holds no keys, the key is that of the org
// the visitor came from; saved tour text is normally served from the cache.
func (h *Handler) speechKey(r *http.Request) (apiKey, baseURL string, ok bool, err error) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	tenantID := claims.TenantID
	if claims.Demo {
		if claims.RealTenantID == 0 {
			return "", "", false, nil
		}
		tenantID = claims.RealTenantID
	}
	apiKey, baseURL, err = h.tenantVendorKey(r.Context(), tenantID, ai.ProviderOpenAI)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if ai.IsAzureEndpoint(baseURL) {
		return "", "", false, nil
	}
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return apiKey, strings.TrimRight(baseURL, "/"), true, nil
}

// HandleSpeechStatus is GET /api/chat/speech: whether tours can speak.
func (h *Handler) HandleSpeechStatus(w http.ResponseWriter, r *http.Request) {
	_, _, ok, err := h.speechKey(r)
	if err != nil {
		logging.Error(r.Context(), err, "loading the voice key failed", "tour-speech-key-failed")
		httputil.ServerError(w, "load", "voice settings")
		return
	}
	httputil.Data(w, map[string]bool{"available": ok})
}

type speechRequest struct {
	Body string `json:"body"`
	Say  string `json:"say"`
}

// HandleSpeech is POST /api/chat/speech: one tour card as MP3.
func (h *Handler) HandleSpeech(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.ClaimsFromContext(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req speechRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if len(strings.TrimSpace(req.Body)) > maxTourBodyLen || len(strings.TrimSpace(req.Say)) > maxTourSayLen {
		httputil.BadRequest(w, fmt.Sprintf("body (max %d) or say (max %d characters) is too long", maxTourBodyLen, maxTourSayLen))
		return
	}
	text := speechText(req.Say, req.Body)
	if text == "" {
		httputil.BadRequest(w, "nothing to say")
		return
	}
	apiKey, baseURL, ok, err := h.speechKey(r)
	if err != nil {
		logging.Error(r.Context(), err, "loading the voice key failed", "tour-speech-key-failed")
		httputil.ServerError(w, "load", "voice settings")
		return
	}
	if !ok {
		httputil.ErrorCode(w, http.StatusPreconditionFailed, "no_voice_key",
			"Voice needs an OpenAI key in AI settings.")
		return
	}

	// Any caller may read the shared cache, but only audio from OpenAI itself
	// may fill it: a custom endpoint is chosen by one organization's admin, and
	// its reply must never be what another organization hears.
	cacheable := savedSpeech[text] && h.storage != nil
	if cacheable {
		audio, err := h.cachedSpeech(r.Context(), text)
		if err != nil {
			logging.Warn(r.Context(), err, "reading cached tour speech failed", "tour-speech-cache-read-failed")
		}
		if audio != nil {
			writeAudio(w, audio)
			return
		}
	}
	audio, err := openAISpeech(r.Context(), apiKey, baseURL, text)
	if err != nil {
		var pe *ai.ProviderError
		if errors.As(err, &pe) {
			logging.Warn(r.Context(), err, "tour speech request failed", "tour-speech-provider-failed")
			httputil.ErrorCode(w, http.StatusBadGateway, "voice_failed", speechErrorMessage(r, err))
			return
		}
		logging.Error(r.Context(), err, "tour speech request failed", "tour-speech-failed")
		httputil.ServerError(w, "make", "voice")
		return
	}
	if cacheable && baseURL == h.trustedSpeechBase {
		if err := h.storage.Put(r.Context(), speechCacheKey(text), bytes.NewReader(audio)); err != nil {
			logging.Warn(r.Context(), err, "caching tour speech failed", "tour-speech-cache-write-failed")
		}
	}
	writeAudio(w, audio)
}

// cachedSpeech returns stored audio, or nil when none is stored yet.
func (h *Handler) cachedSpeech(ctx context.Context, text string) ([]byte, error) {
	rc, err := h.storage.Download(ctx, speechCacheKey(text))
	if errors.Is(err, file.ErrBlobNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxSpeechAudioBytes))
}

func writeAudio(w http.ResponseWriter, audio []byte) {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

var speechClient = netguard.Client(speechTimeout)

// openAISpeech calls POST /v1/audio/speech and returns the MP3.
func openAISpeech(ctx context.Context, apiKey, baseURL, text string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": speechModel, "voice": speechVoice, "input": text,
		"instructions": speechInstructions, "response_format": "mp3",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	ai.SetOpenAIAuth(req, apiKey, baseURL)
	req.Header.Set("Content-Type", "application/json")
	resp, err := speechClient.Do(req)
	if err != nil {
		return nil, classifyOpenAIErr(0, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxSpeechAudioBytes))
	if err != nil {
		return nil, fmt.Errorf("read speech: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyOpenAIErr(resp.StatusCode, string(audio))
	}
	return audio, nil
}

// SetTrustedSpeechBase sets the endpoint whose audio may fill the shared
// cache. Production leaves it at api.openai.com; tests point it at a fake.
func (h *Handler) SetTrustedSpeechBase(url string) { h.trustedSpeechBase = url }

// speechErrorMessage is what the tour card says when the voice fails. In the
// demo organization the key is the visitor's own org's, so the message sends
// them there, not to the demo's (empty, read-only) AI settings.
func speechErrorMessage(r *http.Request, err error) string {
	claims, _ := auth.ClaimsFromContext(r.Context())
	if claims == nil || !claims.Demo {
		return userFacingLLMError(err)
	}
	var pe *ai.ProviderError
	if errors.As(err, &pe) && (pe.Kind == ai.ProviderErrAuth || pe.Kind == ai.ProviderErrQuota) {
		return "The voice uses your own organization's OpenAI key, and OpenAI refused it. Check the key in AI settings in your own organization."
	}
	return userFacingLLMError(err)
}
