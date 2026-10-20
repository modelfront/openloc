package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestData(t *testing.T) {
	w := httptest.NewRecorder()
	Data(w, map[string]string{"name": "test"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["name"] != "test" {
		t.Errorf("expected data.name='test', got %v", data["name"])
	}
}

func TestCreated(t *testing.T) {
	w := httptest.NewRecorder()
	Created(w, map[string]int{"id": 42})

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["id"].(float64) != 42 {
		t.Errorf("expected data.id=42")
	}
}

func TestNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	NoContent(w)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %d bytes", w.Body.Len())
	}
}

func TestSetAttachmentFilename(t *testing.T) {
	// A UTF-8 name read as Latin-1 renders as "enâ†'de". RFC 5987 filename* must
	// carry the real name, and the ASCII filename= fallback must not contain raw
	// non-ASCII bytes.
	w := httptest.NewRecorder()
	SetAttachmentFilename(w, "Default Term Base (en→de).csv")

	cd := w.Header().Get("Content-Disposition")
	// "→" is 0xE2 0x86 0x92 → percent-encoded, uppercase hex.
	if !strings.Contains(cd, "filename*=UTF-8''") || !strings.Contains(cd, "%E2%86%92") {
		t.Errorf("expected RFC 5987 encoded arrow in filename*, got %q", cd)
	}
	// The plain filename= fallback must be pure ASCII (no raw multibyte bytes).
	fallback := cd[strings.Index(cd, `filename="`)+len(`filename="`):]
	fallback = fallback[:strings.IndexByte(fallback, '"')]
	for i := 0; i < len(fallback); i++ {
		if fallback[i] >= 0x80 {
			t.Errorf("ASCII fallback leaked a non-ASCII byte: %q", fallback)
			break
		}
	}
	// Header-breaking characters (quote, semicolon, slash) must be neutralized.
	w2 := httptest.NewRecorder()
	SetAttachmentFilename(w2, "a\"b;c/d.csv")
	if strings.Contains(w2.Header().Get("Content-Disposition"), `a"b;c/d`) {
		t.Errorf("unsafe characters not sanitized: %q", w2.Header().Get("Content-Disposition"))
	}
}
