package ai

import (
	"regexp"
	"strconv"
	"strings"
)

// Turning a provider's raw model id into something the app can offer and call.
//
// Two jobs, deliberately separate:
//
//   - familyFromID is a PURE function of the id. It decides which request
//     parameters a model accepts, and it is read on the runtime path (settings.go
//     builds the outbound body from it, in the worker, with no HTTP request in
//     scope). It must never depend on the fetched catalog: a refresh failure
//     changing `temperature: 0` back on for a GPT-5 call is a hard 400 on every
//     segment.
//   - classifyRemote is the LISTING filter. It decides whether a fetched id is a
//     text-generation model worth showing in a dropdown.
//
// OpenAI's /v1/models is the reason the second one is pattern matching rather
// than a lookup: each row carries only {id, created, owned_by} — no modality or
// capability field, and no server-side type filter — so "is this a chat model or
// a text-to-speech model" can only be read off the id.

// openAIAllowPrefixes are the id families that can generate text. Anything not
// starting with one of these is some other product (embeddings, whisper, sora).
var openAIAllowPrefixes = []string{"gpt-", "o1", "o3", "o4", "chatgpt-"}

// openAIDenyMarkers are substrings marking a SPECIALIZED model inside an allowed
// family — gpt-4o-mini-tts is a speech model, gpt-4o-realtime is a session API.
// Substrings rather than exact prefixes so a new dated variant of one of these
// can't slip through.
var openAIDenyMarkers = []string{
	"audio", "realtime", "transcribe", "tts", "image", "search",
	"moderation", "instruct", "embedding", "whisper", "dall-e", "sora",
	"codex", "computer-use", "-live",
}

// oSeries matches OpenAI's reasoning line — o1, o3, o4-mini — as opposed to the
// gpt- line, whose generation decides the same thing.
var oSeries = regexp.MustCompile(`^o\d`)

// firstReasoningGPT is the GPT generation that reasons: 5 and up take
// reasoning_effort + verbosity and reject temperature, top_p, the penalties and
// max_tokens; 4 and below are the other way round. Sending the wrong set is a
// 400, not a soft degradation.
const firstReasoningGPT = 5.0

// gptGeneration reads the number after "gpt-": 4 from "gpt-4o", 4.1 from
// "gpt-4.1-mini", 5.6 from "gpt-5.6". Returns 0 for an id that isn't gpt-N,
// which classifies as a chat model.
func gptGeneration(id string) float64 {
	rest, ok := strings.CutPrefix(id, "gpt-")
	if !ok {
		return 0
	}
	// The generation runs to the first separator: "5.6-mini" → "5.6", and
	// "4o" → "4" once the trailing letters are cut.
	seg, _, _ := strings.Cut(rest, "-")
	seg = strings.TrimRight(seg, "abcdefghijklmnopqrstuvwxyz")
	n, err := strconv.ParseFloat(seg, 64)
	if err != nil {
		return 0
	}
	return n
}

// ftPrefix marks an OpenAI fine-tune: `ft:<base model>:<org>::<id>`. The base
// model decides the parameter family, so the id is unwrapped before matching.
const ftPrefix = "ft:"

// baseModelID unwraps a fine-tune id to the model it was trained from, and
// returns other ids unchanged.
func baseModelID(id string) string {
	if !strings.HasPrefix(id, ftPrefix) {
		return id
	}
	rest := id[len(ftPrefix):]
	if i := strings.Index(rest, ":"); i > 0 {
		return rest[:i]
	}
	return rest
}

// familyFromID returns the request-parameter family a model accepts. Unknown
// providers and ids yield familyNone — no knobs offered rather than knobs that
// 400.
func familyFromID(provider, id string) paramFamily {
	base := strings.ToLower(baseModelID(strings.TrimSpace(id)))
	switch provider {
	case ProviderOpenAI:
		if !hasAnyPrefix(base, openAIAllowPrefixes) {
			return familyNone
		}
		if oSeries.MatchString(base) || gptGeneration(base) >= firstReasoningGPT {
			return familyOpenAIReasoning
		}
		return familyOpenAIChat
	case ProviderClaude:
		if !strings.HasPrefix(base, "claude-") {
			return familyNone
		}
		// Fable always thinks — an explicit thinking:disabled is rejected.
		if strings.Contains(base, "fable") {
			return familyClaudeEffortAlwaysThinking
		}
		if claudeVersion(base) < firstAdaptiveClaude {
			return familyClaudeBudget
		}
		return familyClaudeEffort
	}
	return familyNone
}

// firstAdaptiveClaude is the generation where Anthropic replaced the sampling
// knobs and the fixed thinking budget with adaptive thinking + `effort`. Below
// it, `effort` is rejected; at or above it, `temperature` and `budget_tokens`
// are. Neither degrades softly — the wrong set is a 400 on every request.
const firstAdaptiveClaude = 4.8

// claudeVersion reads the generation out of a model id: 5 from
// "claude-opus-5", 4.8 from "claude-opus-4-8", 4.5 from
// "claude-haiku-4-5-20251001", 3.5 from "claude-3-5-sonnet-20241022". An id
// carrying no version is treated as current — a new naming scheme is far more
// likely to be a new model than a resurrected old one.
func claudeVersion(id string) float64 {
	if alias := aliasOf(id); alias != "" {
		id = alias
	}
	var nums []float64
	for _, part := range strings.Split(id, "-") {
		if n, err := strconv.ParseFloat(part, 64); err == nil {
			nums = append(nums, n)
		}
	}
	switch len(nums) {
	case 0:
		return firstAdaptiveClaude
	case 1:
		return nums[0]
	default:
		return nums[0] + nums[1]/10
	}
}

// ProviderForModelID names the vendor a model id belongs to, or "" when no
// vendor claims that shape. Pure, like familyFromID: callers that only need
// "which adapter drives this" must not have to consult a tenant's catalog.
func ProviderForModelID(id string) string {
	for _, p := range []string{ProviderOpenAI, ProviderClaude} {
		if familyFromID(p, id) != familyNone {
			return p
		}
	}
	return ""
}

// classifyRemote turns a fetched provider row into a listable Model. ok is false
// when the id isn't a text-generation model — the vendor's catalog carries
// speech, image and embedding models alongside the ones we can drive.
func classifyRemote(provider string, m RemoteModel) (Model, bool) {
	id := strings.TrimSpace(m.ID)
	if id == "" {
		return Model{}, false
	}
	base := strings.ToLower(baseModelID(id))
	if provider == ProviderOpenAI && containsAny(base, openAIDenyMarkers) {
		return Model{}, false
	}
	family := familyFromID(provider, id)
	if family == familyNone {
		return Model{}, false
	}
	return Model{
		ID:         id,
		Label:      labelFor(m),
		family:     family,
		deprecated: m.Lifecycle == LifecycleDeprecated,
	}, true
}

// labelFor is the human name for a dropdown. Anthropic ships a display_name;
// OpenAI ships nothing, so the id is prettified.
func labelFor(m RemoteModel) string {
	if s := strings.TrimSpace(m.DisplayName); s != "" {
		return s
	}
	return prettyLabel(m.ID)
}

// labelCasing spells the id fragments users recognise. Anything absent is
// title-cased, which handles "turbo" → "Turbo" and "nano" → "Nano".
var labelCasing = map[string]string{
	"gpt": "GPT", "chatgpt": "ChatGPT", "4o": "4o", "3o": "3o",
	"mini": "mini", "nano": "nano", "turbo": "Turbo", "preview": "preview",
	"latest": "latest", "chat": "chat",
}

// prettyLabel derives "GPT-4o mini" from "gpt-4o-mini". The first fragment keeps
// its hyphen (the product name), the rest become words.
func prettyLabel(id string) string {
	if strings.HasPrefix(id, ftPrefix) {
		return "Fine-tune: " + prettyLabel(baseModelID(id))
	}
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if cased, ok := labelCasing[strings.ToLower(p)]; ok {
			parts[i] = cased
			continue
		}
		if r := []rune(p); len(r) > 0 && r[0] >= 'a' && r[0] <= 'z' {
			parts[i] = strings.ToUpper(string(r[0])) + string(r[1:])
		}
	}
	if len(parts) <= 2 {
		return strings.Join(parts, "-")
	}
	return parts[0] + "-" + parts[1] + " " + strings.Join(parts[2:], " ")
}

// datedSuffix matches a snapshot date at the end of an id: `-2024-08-06`
// (OpenAI) or `-20251001` (Anthropic).
var datedSuffix = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{8})$`)

// aliasOf strips a snapshot date, returning the alias that addresses the same
// model ("gpt-4o-2024-08-06" → "gpt-4o"). Returns "" when the id isn't dated.
func aliasOf(id string) string {
	if loc := datedSuffix.FindStringIndex(id); loc != nil {
		return id[:loc[0]]
	}
	return ""
}

// dropDatedDuplicates removes a dated snapshot when the alias addressing the
// same model is also in the list. Both providers publish an alias alongside
// several dated pins; without this, one model family fills the whole dropdown.
// A snapshot with no alias present is kept — that is how Haiku 4.5 is addressed.
func dropDatedDuplicates(models []Model) []Model {
	present := make(map[string]bool, len(models))
	for _, m := range models {
		present[m.ID] = true
	}
	out := models[:0]
	for _, m := range models {
		if alias := aliasOf(m.ID); alias != "" && present[alias] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
