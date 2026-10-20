package mt

import (
	"context"
	"testing"
)

func TestMock_Name(t *testing.T) {
	m := Mock{}
	if m.Name() != "mock" {
		t.Errorf("expected 'mock', got %q", m.Name())
	}
}

func TestMock_Translate(t *testing.T) {
	m := Mock{}
	results, err := m.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "es",
		Texts:      []string{"Hello", "Goodbye"},
	})
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Text != "[ES] Hello" {
		t.Errorf("expected '[ES] Hello', got %q", results[0].Text)
	}
	if results[1].Text != "[ES] Goodbye" {
		t.Errorf("expected '[ES] Goodbye', got %q", results[1].Text)
	}
}

func TestMock_TranslateEmpty(t *testing.T) {
	m := Mock{}
	results, err := m.Translate(context.Background(), TranslateRequest{
		SourceLang: "en",
		TargetLang: "fr",
		Texts:      []string{},
	})
	if err != nil {
		t.Fatalf("Translate failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
