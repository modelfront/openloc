package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCursor_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	p := ParseCursor(r, 0) // maxLimit=0 → default 100
	if p.Cursor != 0 {
		t.Errorf("expected cursor 0, got %d", p.Cursor)
	}
	if p.Limit != 50 {
		t.Errorf("expected default limit 50, got %d", p.Limit)
	}
}

func TestParseCursor_Valid(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?cursor=42&limit=25", nil)
	p := ParseCursor(r, 100)
	if p.Cursor != 42 {
		t.Errorf("expected cursor 42, got %d", p.Cursor)
	}
	if p.Limit != 25 {
		t.Errorf("expected limit 25, got %d", p.Limit)
	}
}

func TestParseCursor_Clamped(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?limit=9999", nil)
	p := ParseCursor(r, 500)
	if p.Limit != 50 { // exceeds maxLimit, falls back to default
		t.Errorf("expected default 50 (clamped), got %d", p.Limit)
	}
}

func TestParseCursor_Invalid(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items?cursor=abc&limit=-1", nil)
	p := ParseCursor(r, 100)
	if p.Cursor != 0 {
		t.Errorf("expected cursor 0 for invalid input, got %d", p.Cursor)
	}
	if p.Limit != 50 {
		t.Errorf("expected default limit for invalid input, got %d", p.Limit)
	}
}

func TestParseCursor_SmallMaxLimit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	p := ParseCursor(r, 10)
	if p.Limit != 10 { // defaultLimit capped by maxLimit
		t.Errorf("expected limit 10 (capped), got %d", p.Limit)
	}
}

func TestPaginated_WithTotal(t *testing.T) {
	w := httptest.NewRecorder()
	items := []string{"a", "b"}
	Paginated(w, items, 42, true, 100)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["cursor"].(float64) != 42 {
		t.Errorf("expected cursor 42, got %v", resp["cursor"])
	}
	if resp["has_more"] != true {
		t.Errorf("expected has_more true")
	}
	if resp["total"].(float64) != 100 {
		t.Errorf("expected total 100, got %v", resp["total"])
	}
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Errorf("expected 2 items, got %d", len(data))
	}
}

func TestPaginated_WithoutTotal(t *testing.T) {
	w := httptest.NewRecorder()
	Paginated(w, []string{}, 0, false, -1)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if _, ok := resp["total"]; ok {
		t.Error("expected no 'total' field when total=-1")
	}
	if resp["has_more"] != false {
		t.Error("expected has_more false for empty result")
	}
}
