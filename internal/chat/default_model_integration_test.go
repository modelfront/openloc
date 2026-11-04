package chat_test

import (
	"net/http"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/chat"
	"openloc/internal/testutil"
)

// The default follows the tenant's Claude key: the newest Sonnet it lists,
// not a model name fixed at build time.
func TestIntegrationChatDefaultIsTheNewestSonnet(t *testing.T) {
	f := newChatFixture(t, nil)
	f.catalog.Set(f.tenantA.ID, ai.ProviderClaude, []ai.Model{
		{ID: "claude-opus-5-5"}, {ID: "claude-sonnet-5"}, {ID: "claude-sonnet-5-5"}, {ID: "claude-haiku-4-5"},
	})

	var defaults []string
	for _, m := range f.models(t) {
		if m.Default {
			defaults = append(defaults, m.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != "claude-sonnet-5-5" {
		t.Errorf("default models = %v, want [claude-sonnet-5-5]", defaults)
	}

	var gotModel string
	f.handler.SetLLMFactory(func(_, _, _, model string) chat.LLM {
		gotModel = model
		return f.llm
	})
	rec := f.do(t, "POST", "/api/chat", `{"message":"q"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("send without a model: %d %s", rec.Code, rec.Body.String())
	}
	if gotModel != "claude-sonnet-5-5" {
		t.Errorf("a send without a model used %q, want claude-sonnet-5-5", gotModel)
	}

	// Dashboard chart suggestions run on the same default.
	gotModel = ""
	f.router.Get("/api/chat/suggest-chart", f.handler.HandleSuggestChart)
	rec = f.do(t, "GET", "/api/chat/suggest-chart", "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest-chart: %d %s", rec.Code, rec.Body.String())
	}
	if gotModel != "claude-sonnet-5-5" {
		t.Errorf("a chart suggestion used %q, want claude-sonnet-5-5", gotModel)
	}
}

// A key whose list has no Sonnet keeps the fixed fallback as the default.
func TestIntegrationChatDefaultFallsBackWithoutASonnet(t *testing.T) {
	f := newChatFixture(t, nil)
	f.catalog.Set(f.tenantA.ID, ai.ProviderClaude, []ai.Model{{ID: "claude-opus-5-5"}})

	var gotModel string
	f.handler.SetLLMFactory(func(_, _, _, model string) chat.LLM {
		gotModel = model
		return f.llm
	})
	rec := f.do(t, "POST", "/api/chat", `{"message":"q"}`, f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("send without a model: %d %s", rec.Code, rec.Body.String())
	}
	if gotModel != chat.FallbackModel {
		t.Errorf("a send without a model used %q, want %q", gotModel, chat.FallbackModel)
	}
}

type listedModel struct {
	ID      string `json:"id"`
	Default bool   `json:"default"`
}

func (f *chatFixture) models(t *testing.T) []listedModel {
	t.Helper()
	rec := f.do(t, "GET", "/api/chat/models", "", f.ownerA, f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("models: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []listedModel `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}
