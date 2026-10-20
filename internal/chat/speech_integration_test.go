package chat_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// fakeSpeech stands in for OpenAI's /v1/audio/speech and records each call.
type fakeSpeech struct {
	mu     sync.Mutex
	inputs []string
	auth   string
	status int
}

func (f *fakeSpeech) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("speech path = %s", r.URL.Path)
		}
		var body struct {
			Model, Voice, Input string
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("speech body %s: %v", raw, err)
		}
		f.mu.Lock()
		f.inputs = append(f.inputs, body.Input)
		f.auth = r.Header.Get("Authorization")
		status := f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("MP3:" + body.Input))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeSpeech) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.inputs...)
}

func speechAvailable(t *testing.T, f *chatFixture, tenantID int64) bool {
	t.Helper()
	owner := f.ownerA
	if tenantID == f.tenantB.ID {
		owner = f.ownerB
	}
	rec := f.do(t, "GET", "/api/chat/speech", "", owner, tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("speech status: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct{ Available bool } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data.Available
}

// savedStepWithSay is a real saved tour step that has a spoken line, as the
// browser receives it.
func savedStepWithSay(t *testing.T, f *chatFixture) (body, say string) {
	t.Helper()
	rec := f.do(t, "GET", "/api/chat/playbooks", "", f.ownerA, f.tenantA.ID)
	var env struct {
		Data []struct {
			Steps []struct{ Body, Say string } `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, pb := range env.Data {
		for _, s := range pb.Steps {
			if s.Say != "" {
				return s.Body, s.Say
			}
		}
	}
	t.Fatal("no saved tour step with a say line")
	return "", ""
}

func speechBody(body, say string) string {
	b, _ := json.Marshal(map[string]string{"body": body, "say": say})
	return string(b)
}

// Voice runs on the organization's own OpenAI key and exists only when there
// is one on the public API (or a compatible gateway) — never another tenant's.
func TestIntegrationTourSpeechNeedsTheTenantsOpenAIKey(t *testing.T) {
	f := newChatFixture(t, nil)
	fake := &fakeSpeech{}
	srv := fake.serve(t)

	// Tenant A has only a Claude key: no voice.
	if speechAvailable(t, f, f.tenantA.ID) {
		t.Fatal("voice available without an OpenAI key")
	}
	if rec := f.do(t, "POST", "/api/chat/speech", speechBody("Hi there.", ""), f.ownerA, f.tenantA.ID); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("speech without a key = %d, want 412", rec.Code)
	}

	// Tenant B's OpenAI key does not give tenant A a voice.
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantB.ID, "openai", "Default", f.cipher, srv.URL)
	if speechAvailable(t, f, f.tenantA.ID) {
		t.Fatal("tenant A got a voice from tenant B's key")
	}
	if !speechAvailable(t, f, f.tenantB.ID) {
		t.Fatal("tenant B has an OpenAI key but no voice")
	}

	// Azure serves speech on a per-deployment path this call does not build.
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantA.ID, "openai", "Azure", f.cipher, "https://acme.openai.azure.com")
	if speechAvailable(t, f, f.tenantA.ID) {
		t.Fatal("voice offered on an Azure endpoint")
	}
	if n := len(fake.calls()); n != 0 {
		t.Errorf("OpenAI called %d times while voice was unavailable", n)
	}
}

// Saved tour steps are generated once and then served from storage; a
// chat-made step can carry the organization's data and is never stored.
func TestIntegrationTourSpeechCachesOnlySavedSteps(t *testing.T) {
	f := newChatFixture(t, nil)
	fake := &fakeSpeech{}
	srv := fake.serve(t)
	f.handler.SetTrustedSpeechBase(srv.URL)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantA.ID, "openai", "Default", f.cipher, srv.URL)
	// A job to open, so the editor tour's steps are not left out.
	testutil.SeedJob(t, f.q, testutil.SeedFile(t, f.q, f.project.ID, "doc.docx", db.FileFormatDocx).ID, "de")

	body, say := savedStepWithSay(t, f)
	for i := 0; i < 2; i++ {
		rec := f.do(t, "POST", "/api/chat/speech", speechBody(body, say), f.ownerA, f.tenantA.ID)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" {
			t.Fatalf("saved step speech #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
		if calls := fake.calls(); len(calls) != 1 || rec.Body.String() != "MP3:"+calls[0] {
			t.Fatalf("audio #%d = %q, OpenAI inputs %q", i+1, rec.Body.String(), calls)
		}
	}
	// The voice reads the spoken line, not the card body.
	if got := fake.calls()[0]; got == body || !strings.HasPrefix(got, strings.Fields(say)[0]) {
		t.Errorf("OpenAI read %q; want the say line %q", got, say)
	}
	if fake.auth != "Bearer sk-ant-test-not-real" {
		t.Errorf("Authorization = %q, want the tenant's decrypted key", fake.auth)
	}

	chatMade := speechBody("This is the Chat Alpha project.", "")
	for i := 0; i < 2; i++ {
		if rec := f.do(t, "POST", "/api/chat/speech", chatMade, f.ownerA, f.tenantA.ID); rec.Code != http.StatusOK {
			t.Fatalf("chat-made speech: %d %s", rec.Code, rec.Body.String())
		}
	}
	if calls := fake.calls(); len(calls) != 3 {
		t.Fatalf("OpenAI calls = %d, want 3 (a chat-made step is never cached): %q", len(calls), calls)
	}
}

func TestIntegrationTourSpeechRejectsBadInputAndReportsProviderErrors(t *testing.T) {
	f := newChatFixture(t, nil)
	fake := &fakeSpeech{}
	srv := fake.serve(t)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantA.ID, "openai", "Default", f.cipher, srv.URL)

	long := make([]byte, 401)
	for i := range long {
		long[i] = 'a'
	}
	longSay := strings.Repeat("a", 601)
	for _, b := range []string{speechBody("", ""), speechBody(string(long), ""), speechBody("Hi", longSay), "not json"} {
		if rec := f.do(t, "POST", "/api/chat/speech", b, f.ownerA, f.tenantA.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.30q = %d, want 400", b, rec.Code)
		}
	}

	fake.status = http.StatusUnauthorized
	rec := f.do(t, "POST", "/api/chat/speech", speechBody("Hi there.", ""), f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("rejected key = %d, want 502", rec.Code)
	}
	var env struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "voice_failed" || env.Error.Message == "" || env.Error.Message == "bad key" {
		t.Errorf("error = %+v, want a user message, not the provider payload", env.Error)
	}
}

// fakeSpeechSaying answers every speech request with fixed audio, so a test
// can tell which endpoint's audio a listener got.
func fakeSpeechSaying(t *testing.T, audio string) (*httptest.Server, *int) {
	t.Helper()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte(audio))
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// The shared store of saved-step audio is filled only from OpenAI itself. An
// organization that points its key at its own server gets that server's audio,
// and nobody else ever hears it.
func TestIntegrationTourSpeechSharedCacheIgnoresCustomEndpoints(t *testing.T) {
	f := newChatFixture(t, nil)
	testutil.SeedJob(t, f.q, testutil.SeedFile(t, f.q, f.project.ID, "doc.docx", db.FileFormatDocx).ID, "de")
	openai, openaiCalls := fakeSpeechSaying(t, "OPENAI")
	own, _ := fakeSpeechSaying(t, "PLANTED")
	f.handler.SetTrustedSpeechBase(openai.URL)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantA.ID, "openai", "Default", f.cipher, openai.URL)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantB.ID, "openai", "Default", f.cipher, own.URL)

	body, say := savedStepWithSay(t, f)
	rec := f.do(t, "POST", "/api/chat/speech", speechBody(body, say), f.ownerB, f.tenantB.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != "PLANTED" {
		t.Fatalf("custom endpoint speech: %d %q", rec.Code, rec.Body.String())
	}
	rec = f.do(t, "POST", "/api/chat/speech", speechBody(body, say), f.ownerA, f.tenantA.ID)
	if rec.Body.String() != "OPENAI" {
		t.Fatalf("tenant A heard %q, want OpenAI's audio (the custom endpoint's reply must not be shared)", rec.Body.String())
	}
	if *openaiCalls != 1 {
		t.Fatalf("OpenAI calls = %d, want 1", *openaiCalls)
	}
}

// The demo organization holds no keys; a visitor's tour voice runs on the key
// of the org they came from, and a visitor with no org has no voice.
func TestIntegrationTourSpeechInTheDemoUsesTheVisitorsOrgKey(t *testing.T) {
	f := newChatFixture(t, nil)
	fake := &fakeSpeech{}
	srv := fake.serve(t)
	testutil.SeedAIVendorTokenBase(t, f.q, f.tenantA.ID, "openai", "Default", f.cipher, srv.URL)
	visit := func(realTenantID int64, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/chat/speech", strings.NewReader(body))
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
			TenantID: f.tenantB.ID, UserID: f.ownerB.ID, Role: "owner",
			Demo: true, RealUserID: f.ownerA.ID, RealTenantID: realTenantID,
		}))
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := visit(f.tenantA.ID, "GET", ""); !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("voice in the demo from an org with a key: %s", rec.Body.String())
	}
	if rec := visit(f.tenantA.ID, "POST", speechBody("Welcome to the demo.", "")); rec.Code != http.StatusOK || rec.Body.String() != "MP3:Welcome to the demo." {
		t.Fatalf("demo speech: %d %q", rec.Code, rec.Body.String())
	}
	if rec := visit(0, "GET", ""); !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Errorf("voice in the demo with no org: %s", rec.Body.String())
	}

	// A rejected key is the visitor's own org's key, not the demo's: the
	// message sends them there, not to the demo's AI settings.
	fake.status = http.StatusUnauthorized
	rec := visit(f.tenantA.ID, "POST", speechBody("Welcome back to the demo.", ""))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "your own organization") {
		t.Errorf("rejected key in the demo: %d %s", rec.Code, rec.Body.String())
	}
}
