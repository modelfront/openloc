package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Outside development the assistant and the voice refuse a loopback endpoint
// before any request reaches it, so a tenant-set base URL cannot read internal
// services (a 302 to the file store would come back as "audio").
func TestChatClientsRefuseInternalAddressesOutsideDev(t *testing.T) {
	t.Setenv("ENV", "production")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ctx := context.Background()

	if _, err := openAISpeech(ctx, "sk-test", srv.URL, "hello"); err == nil {
		t.Error("speech: call to a loopback endpoint succeeded")
	}
	llm := NewOpenAI("sk-test", srv.URL, "gpt-4o")
	if _, err := llm.Stream(ctx, Request{Messages: []Message{{Role: "user", Text: "hi"}}}, func(string) {}); err == nil {
		t.Error("assistant: call to a loopback endpoint succeeded")
	}
	claude := NewAnthropic("sk-ant-test", srv.URL, "")
	if _, err := claude.Stream(ctx, Request{Messages: []Message{{Role: "user", Text: "hi"}}}, func(string) {}); err == nil {
		t.Error("assistant (Claude): call to a loopback endpoint succeeded")
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("loopback server received %d requests, want 0", n)
	}
}

// Every chat turn builds a new LLM, so the adapters share one client and its
// connection pool instead of opening a fresh TLS connection per turn.
func TestNewOpenAISharesOneHTTPClient(t *testing.T) {
	a := NewOpenAI("sk-a", "", "").(*openaiLLM)
	b := NewOpenAI("sk-b", "https://acme.openai.azure.com", "gpt-4o").(*openaiLLM)
	if a.client != b.client {
		t.Error("NewOpenAI built a new http.Client per call; want one shared client")
	}
}
