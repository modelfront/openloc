package ai_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// An owner of one org sends another org's API key id. The key lookup is
// scoped by tenant, so the edit and the delete are 404 and the key keeps its
// name and stays in place. The key's own owner on the same routes is the
// positive control.
func TestIntegrationAIVendorToken_OtherTenantCannotEditOrDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	victimOwner, victim := testutil.RegisterUser(t, q, "victim")
	attackerOwner, attacker := testutil.RegisterUser(t, q, "attacker")
	testutil.SeedAIVendorToken(t, q, attacker, "openai", "attacker-key", "ENC(attacker)")

	tokenPath := func(id int64) string { return fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", id) }
	// victimToken reads the key straight from the database: "" when it is gone.
	victimToken := func(t *testing.T, id int64) string {
		t.Helper()
		tok, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: id, TenantID: victim})
		if errors.Is(err, pgx.ErrNoRows) {
			return ""
		}
		if err != nil {
			t.Fatalf("GetVendorToken: %v", err)
		}
		return tok.Name
	}

	t.Run("PATCH", func(t *testing.T) {
		tok := testutil.SeedAIVendorToken(t, q, victim, "openai", "victim-patch", "ENC(victim)")

		rec := testutil.DoRequest(t, router, http.MethodPatch, tokenPath(tok.ID),
			map[string]any{"name": "hijacked"}, attackerOwner, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if got := victimToken(t, tok.ID); got != "victim-patch" {
			t.Fatalf("another org's PATCH changed the key: name = %q, want victim-patch", got)
		}

		rec = testutil.DoRequest(t, router, http.MethodPatch, tokenPath(tok.ID),
			map[string]any{"name": "renamed"}, victimOwner, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := victimToken(t, tok.ID); got != "renamed" {
			t.Errorf("owner's PATCH: name = %q, want renamed", got)
		}
	})

	t.Run("DELETE", func(t *testing.T) {
		tok := testutil.SeedAIVendorToken(t, q, victim, "openai", "victim-delete", "ENC(victim)")

		rec := testutil.DoRequest(t, router, http.MethodDelete, tokenPath(tok.ID), nil, attackerOwner, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if victimToken(t, tok.ID) == "" {
			t.Fatal("another org's DELETE removed the key")
		}

		rec = testutil.DoRequest(t, router, http.MethodDelete, tokenPath(tok.ID), nil, victimOwner, victim)
		testutil.AssertStatus(t, rec, http.StatusNoContent)
		if victimToken(t, tok.ID) != "" {
			t.Error("owner's DELETE returned 204 but the key is still there")
		}
	})
}

// connectionFor is the tenant check behind every route that takes a
// connection id: POST and PATCH .../tokens (connection_id in the body) and
// PATCH and DELETE .../connections/{id}. Another org's id is sent both ways
// here — in the URL of PATCH .../connections and in the body of
// PATCH .../tokens — and neither touches the other org's endpoint.
func TestIntegrationAIConnection_OtherTenantsIDIsRejected(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	victimOwner, victim := testutil.RegisterUser(t, q, "victim")
	attackerOwner, attacker := testutil.RegisterUser(t, q, "attacker")
	victimConn := testutil.SeedAIVendorToken(t, q, victim, "openai", "victim", "ENC(victim)").ConnectionID
	attackerTok := testutil.SeedAIVendorToken(t, q, attacker, "openai", "attacker", "ENC(attacker)")

	connPath := fmt.Sprintf("/api/ai/vendors/openai/connections/%d", victimConn)
	victimConnName := func(t *testing.T) string {
		t.Helper()
		c, err := q.GetAIConnection(ctx, db.GetAIConnectionParams{ID: victimConn, TenantID: victim})
		if err != nil {
			t.Fatalf("GetAIConnection: %v", err)
		}
		return c.Name
	}

	t.Run("connection id in the URL", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPatch, connPath,
			map[string]any{"name": "hijacked"}, attackerOwner, attacker)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if got := victimConnName(t); got != "victim" {
			t.Errorf("another org's PATCH renamed the connection to %q", got)
		}
	})

	t.Run("connection id in the body", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPatch,
			fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", attackerTok.ID),
			map[string]any{"name": "attacker", "connection_id": victimConn}, attackerOwner, attacker)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		tok, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: attackerTok.ID, TenantID: attacker})
		if err != nil {
			t.Fatalf("GetVendorToken: %v", err)
		}
		if tok.ConnectionID != attackerTok.ConnectionID {
			t.Errorf("the key moved onto connection %d, want it to stay on %d", tok.ConnectionID, attackerTok.ConnectionID)
		}
	})

	t.Run("own connection", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPatch, connPath,
			map[string]any{"name": "renamed"}, victimOwner, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := victimConnName(t); got != "renamed" {
			t.Errorf("owner's PATCH: name = %q, want renamed", got)
		}
	})
}
