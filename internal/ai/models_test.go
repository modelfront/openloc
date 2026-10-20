package ai

import (
	"context"
	"encoding/json"
	"testing"
)

// A provider with no model list (an extension) must serialize its models as an
// empty array, not `null` — the frontend provider picker reads `.models.length`
// and a nil slice (JSON null) crashed the AI page.
func TestModelsSerializeToArrayNotNull(t *testing.T) {
	c := NewCatalog(nil, nil, DefaultModelLimit)
	b, err := json.Marshal(c.Models(context.Background(), 1, testExtProvider))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("Models(extension) must serialize to [], got %s", b)
	}
	// An unfetched vendor is the same shape — the page renders before the
	// catalog has anything for this tenant.
	b, err = json.Marshal(c.Models(context.Background(), 1, ProviderOpenAI))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("Models(OpenAI) with an empty catalog must serialize to [], got %s", b)
	}
}
