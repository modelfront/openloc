package httputil

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// An offset-paged list reads its cursor, limit and search text; a cursor past
// int32 or an over-long search is the caller's 400.
func TestParseOffsetPage(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?cursor=20&limit=10&q=+ada+", nil)
	p, err := ParseOffsetPage(r, 100)
	if err != nil || p.Cursor != 20 || p.Limit != 10 || p.Off() != 20 || p.Q == nil || *p.Q != "ada" {
		t.Fatalf("ParseOffsetPage = %+v, %v", p, err)
	}
	if _, err := ParseOffsetPage(httptest.NewRequest("GET", "/x?cursor=3000000000", nil), 100); err == nil {
		t.Error("a cursor past int32 was accepted")
	}
	long := strings.Repeat("a", MaxSearch+1)
	if _, err := ParseOffsetPage(httptest.NewRequest("GET", "/x?q="+long, nil), 100); err == nil {
		t.Error("an over-long search was accepted")
	}
}

// A page's total comes from its rows; a page past the end leaves it out.
func TestPageTotal(t *testing.T) {
	for _, c := range []struct {
		cursor int64
		rows   int
		first  int64
		want   int64
	}{
		{0, 3, 12, 12},
		{0, 0, 0, 0},
		{30, 0, 0, -1},
	} {
		if got := PageTotal(c.cursor, c.rows, c.first); got != c.want {
			t.Errorf("PageTotal(%d, %d, %d) = %d, want %d", c.cursor, c.rows, c.first, got, c.want)
		}
	}
}
