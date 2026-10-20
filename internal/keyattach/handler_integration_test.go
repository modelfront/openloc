package keyattach_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/keyattach"
	"openloc/internal/testutil"
)

type listResp struct {
	Data []keyattach.Engine `json:"data"`
}

// A deleted key leaves its engines keyless; a new key of the same vendor lists
// them, fixes the ticked ones in one go, and touches nothing else.
func TestIntegrationKeyAttach(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "AttachCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@attach.test", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@attach.test", "pass", "project_manager")
	other := testutil.SeedTenant(t, q, "OtherCo")
	otherOwner := testutil.SeedUserWithRole(t, q, other.ID, "owner@other.test", "pass", "owner")

	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{TenantID: tenant.ID, Name: "sys", PromptText: "review", OutStatus: true})
	if err != nil {
		t.Fatal(err)
	}
	oldAI := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "old", "enc")
	oldDeepL := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "old", "enc")

	createAI := func(name, model string) int64 {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines", map[string]any{
			"name": name, "rows": []map[string]any{{"system_prompt_id": prompt.ID, "token_id": oldAI.ID, "model": model, "bad_output_action": "reject"}},
		}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct{ Data struct{ ID int64 } }
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.ID
	}
	fits := createAI("Reviewer", "gpt-4o")
	stale := createAI("Old reviewer", "gpt-4o")
	if _, err := pool.Exec(ctx, `UPDATE ai_engine_rows SET model = 'gpt-3' WHERE engine_id = $1`, stale); err != nil {
		t.Fatal(err)
	}
	deepl, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{Name: "DeepL formal", EngineType: "deepl", Enabled: true, TenantID: tenant.ID, TokenID: &oldDeepL.ID})
	if err != nil {
		t.Fatal(err)
	}
	keyed := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "keeper", "enc")
	keptID := createAI("Has a key", "gpt-4o")
	if _, err := pool.Exec(ctx, `UPDATE ai_engine_rows SET token_id = $1 WHERE engine_id = $2`, keyed.ID, keptID); err != nil {
		t.Fatal(err)
	}

	// The engines that lose their key are listed before the delete.
	rec := testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/engines", oldAI.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var using listResp
	testutil.DecodeJSON(t, rec, &using)
	if got := names(using.Data); got != "Old reviewer,Reviewer" {
		t.Errorf("engines using the old key = %s", got)
	}

	for _, tok := range []db.AiVendorToken{oldAI, oldDeepL} {
		rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/vendors/%s/tokens/%d", providerOf(t, q, tenant.ID, tok.ID), tok.ID), nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNoContent)
	}
	newAI := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "new", "enc")
	newDeepL := testutil.SeedAIVendorToken(t, q, tenant.ID, "deepl", "new", "enc")
	keylessURL := fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/keyless-engines", newAI.ID)
	attachURL := fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/attach", newAI.ID)

	t.Run("lists the vendor's keyless engines, with fit", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", keylessURL, nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var got listResp
		testutil.DecodeJSON(t, rec, &got)
		if names(got.Data) != "Old reviewer,Reviewer" {
			t.Fatalf("keyless openai engines = %s (the keyed engine and the DeepL one must not appear)", names(got.Data))
		}
		for _, e := range got.Data {
			wantFits := e.ID == fits
			if e.Fits != wantFits {
				t.Errorf("%s fits=%v, want %v", e.Name, e.Fits, wantFits)
			}
			if e.ID == stale && e.Reason != "This key cannot use gpt-3." {
				t.Errorf("reason = %q", e.Reason)
			}
		}
		rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d/keyless-engines", newDeepL.ID), nil, owner.ID, tenant.ID, "owner")
		var mt listResp
		testutil.DecodeJSON(t, rec, &mt)
		if len(mt.Data) != 1 || mt.Data[0].ID != deepl.ID || mt.Data[0].Kind != keyattach.KindMT || !mt.Data[0].Fits {
			t.Errorf("keyless deepl engines = %+v", mt.Data)
		}
	})

	t.Run("refuses an engine the key cannot serve, changing nothing", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", attachURL, map[string]any{"ai_engine_ids": []int64{fits, stale}}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if tokenOf(t, pool, fits) != nil {
			t.Error("a refused request must not attach the key to any engine")
		}
	})

	t.Run("project managers cannot list or attach", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", attachURL, map[string]any{"ai_engine_ids": []int64{fits}}, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "GET", keylessURL, nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/engines", newAI.ID), nil, pm.ID, tenant.ID, "project_manager")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})

	t.Run("another tenant cannot see or use the key", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "GET", keylessURL, nil, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		rec = testutil.DoRequestWithRole(t, router, "POST", attachURL, map[string]any{"ai_engine_ids": []int64{fits}}, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/engines", newAI.ID), nil, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("another tenant's own key cannot reach this tenant's engines", func(t *testing.T) {
		theirAI := testutil.SeedAIVendorToken(t, q, other.ID, "openai", "theirs", "enc")
		theirDeepL := testutil.SeedAIVendorToken(t, q, other.ID, "deepl", "theirs", "enc")
		rec := testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/keyless-engines", theirAI.ID), nil, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var got listResp
		testutil.DecodeJSON(t, rec, &got)
		if len(got.Data) != 0 {
			t.Errorf("another tenant's keyless list = %+v, want empty", got.Data)
		}
		rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/attach", theirAI.ID),
			map[string]any{"ai_engine_ids": []int64{fits}}, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d/attach", theirDeepL.ID),
			map[string]any{"mt_engine_ids": []int64{deepl.ID}}, otherOwner.ID, other.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if tokenOf(t, pool, fits) != nil {
			t.Error("another tenant's key must not reach this tenant's AI engine")
		}
		after, err := q.GetMTEngine(ctx, db.GetMTEngineParams{ID: deepl.ID, TenantID: tenant.ID})
		if err != nil || after.TokenID != nil {
			t.Errorf("DeepL engine token = %v (%v), want none", after.TokenID, err)
		}
	})

	t.Run("a key of another vendor cannot fix the engine", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d/attach", newDeepL.ID),
			map[string]any{"ai_engine_ids": []int64{fits}}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/keyless-engines", newDeepL.ID), nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("attaches the ticked engines", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", attachURL, map[string]any{"ai_engine_ids": []int64{fits}}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := tokenOf(t, pool, fits); got == nil || *got != newAI.ID {
			t.Errorf("Reviewer token = %v, want %d", got, newAI.ID)
		}
		if tokenOf(t, pool, stale) != nil {
			t.Error("an engine that was not ticked must stay keyless")
		}
		if got := tokenOf(t, pool, keptID); got == nil || *got != keyed.ID {
			t.Errorf("an engine with a key must keep it, got %v", got)
		}
		rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/ai/vendors/deepl/tokens/%d/attach", newDeepL.ID),
			map[string]any{"mt_engine_ids": []int64{deepl.ID}}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		after, err := q.GetMTEngine(ctx, db.GetMTEngineParams{ID: deepl.ID, TenantID: tenant.ID})
		if err != nil || after.TokenID == nil || *after.TokenID != newDeepL.ID {
			t.Errorf("DeepL engine token = %v (%v), want %d", after.TokenID, err, newDeepL.ID)
		}
	})

	t.Run("an engine that already has a key is refused", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, "POST", attachURL, map[string]any{"ai_engine_ids": []int64{keptID}}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if got := tokenOf(t, pool, keptID); got == nil || *got != keyed.ID {
			t.Errorf("the engine's own key changed to %v", got)
		}
	})
}

func names(es []keyattach.Engine) string {
	out := ""
	for i, e := range es {
		if i > 0 {
			out += ","
		}
		out += e.Name
	}
	return out
}

// tokenOf reads an AI engine's row key straight from the table.
func tokenOf(t *testing.T, pool *pgxpool.Pool, engineID int64) *int64 {
	t.Helper()
	var tok *int64
	if err := pool.QueryRow(context.Background(), `SELECT token_id FROM ai_engine_rows WHERE engine_id = $1`, engineID).Scan(&tok); err != nil {
		t.Fatalf("read engine %d key: %v", engineID, err)
	}
	return tok
}

func providerOf(t *testing.T, q db.Querier, tenantID, tokenID int64) string {
	t.Helper()
	tok, err := q.GetVendorToken(context.Background(), db.GetVendorTokenParams{ID: tokenID, TenantID: tenantID})
	if err != nil {
		t.Fatal(err)
	}
	return tok.Provider
}

// An engine with several keyless rows is listed once, and fits only if the key
// can serve every row: the attach gives the key to all of them.
func TestIntegrationKeyAttachMultiRowEngine(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RowsCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@rows.test", "pass", "owner")
	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{TenantID: tenant.ID, Name: "sys", PromptText: "review", OutStatus: true})
	if err != nil {
		t.Fatal(err)
	}
	oldAI := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "old", "enc")
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines", map[string]any{
		"name": "Mixed", "rows": []map[string]any{{"system_prompt_id": prompt.ID, "token_id": oldAI.ID, "model": "gpt-4o", "bad_output_action": "reject"}},
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct{ Data struct{ ID int64 } }
	testutil.DecodeJSON(t, rec, &created)
	mixed := created.Data.ID
	// A second row on a model the new key cannot use, listed after the first.
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_engine_rows (engine_id, source_lang, target_langs, system_prompt_id, bad_output_action, sort_order, token_id, model, settings, provider)
		SELECT engine_id, 'fr', target_langs, system_prompt_id, bad_output_action, sort_order + 1, token_id, 'gpt-3', settings, provider
		FROM ai_engine_rows WHERE engine_id = $1`, mixed); err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d", oldAI.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	newAI := testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "new", "enc")

	rec = testutil.DoRequestWithRole(t, router, "GET", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/keyless-engines", newAI.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got listResp
	testutil.DecodeJSON(t, rec, &got)
	if len(got.Data) != 1 || got.Data[0].ID != mixed {
		t.Fatalf("keyless engines = %+v, want Mixed once", got.Data)
	}
	if got.Data[0].Fits || got.Data[0].Reason != "This key cannot use gpt-3." {
		t.Errorf("Mixed fits=%v reason=%q, want a refusal naming gpt-3", got.Data[0].Fits, got.Data[0].Reason)
	}

	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/ai/vendors/openai/tokens/%d/attach", newAI.ID),
		map[string]any{"ai_engine_ids": []int64{mixed}}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	var keyed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_engine_rows WHERE engine_id = $1 AND token_id IS NOT NULL`, mixed).Scan(&keyed); err != nil {
		t.Fatal(err)
	}
	if keyed != 0 {
		t.Errorf("%d rows got the key, want none", keyed)
	}
}
