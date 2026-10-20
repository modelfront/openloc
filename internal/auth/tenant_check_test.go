package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The tenant gate answers 403 org_pending for an organization that is not
// approved or does not exist, and 503 when the check itself fails.
func TestCheckTenantActiveSeparatesOutagesFromRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"not approved", ErrTenantNotApproved, http.StatusForbidden},
		{"gone", pgx.ErrNoRows, http.StatusForbidden},
		{"database down", errors.New("failed to connect to server"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/projects", nil)
			gate := func(context.Context, int64) (bool, error) { return false, tc.err }
			if _, ok := checkTenantActive(rec, req, gate, 1, false); ok {
				t.Fatal("the gate let the request through")
			}
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
