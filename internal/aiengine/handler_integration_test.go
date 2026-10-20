// AI engine registry: an engine is a name + purpose + exactly
// one provider row that applies to all language pairs (langcode.Wildcard). The
// provider's vendor decides extension (no prompts) vs LLM (prompts
// required). Covers CRUD, tenant isolation, role gating, row validation,
// cross-tenant prompt rejection, and the AI-router delete guard.
package aiengine_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationAIEngines(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	pmA := testutil.SeedUserWithRole(t, q, tenantA.ID, "pmA@test.com", "pass", "project_manager")
	linguistA := testutil.SeedUserWithRole(t, q, tenantA.ID, "lingA@test.com", "pass", "linguist")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	// Engine + segment prompts for tenant A, and a system prompt owned by B.
	sysPrompt, err := q.CreateAIPrompt(context.Background(), db.CreateAIPromptParams{
		TenantID: tenantA.ID, Name: "sys", PromptText: "review", OutStatus: true,
	})
	if err != nil {
		t.Fatalf("seed system prompt: %v", err)
	}
	// An OpenAI token and an extension token — the vendor behind the ROW's
	// token is what makes it an extension row vs an LLM one.
	llmTok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "openai", "Default", "enc")
	testutil.SeedExtension(t, q, tenantA.ID, "acme-qe", "qe")
	extTok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "ext:acme-qe", "Default", "enc")
	// A key whose extension is installed only in ANOTHER tenant.
	testutil.SeedExtension(t, q, tenantB.ID, "other-qe", "qe")
	strayTok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "ext:other-qe", "Default", "enc")
	promptB, err := q.CreateAIPrompt(context.Background(), db.CreateAIPromptParams{
		TenantID: tenantB.ID, Name: "sysB", PromptText: "x", OutStatus: true,
	})
	if err != nil {
		t.Fatalf("seed tenant B prompt: %v", err)
	}

	type engResp struct {
		Data struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Purpose string `json:"purpose"`
			Rows    []struct {
				SourceLang     string   `json:"source_lang"`
				TargetLangs    []string `json:"target_langs"`
				SystemPromptID *int64   `json:"system_prompt_id"`
			} `json:"rows"`
		} `json:"data"`
	}

	// A valid LLM row. source/target langs are ignored by the backend (forced to
	// the wildcard), so they're omitted here.
	llmRow := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"system_prompt_id": sysPrompt.ID, "token_id": llmTok.ID, "model": "gpt-4o",
			"bad_output_action": "reject",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// Owner creates a single-row LLM engine; the row applies to all pairs.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "Reviewer", "rows": []map[string]any{llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created engResp
	testutil.DecodeJSON(t, rec, &created)
	engineID := created.Data.ID
	if len(created.Data.Rows) != 1 || created.Data.Rows[0].SourceLang != "*" ||
		len(created.Data.Rows[0].TargetLangs) != 1 || created.Data.Rows[0].TargetLangs[0] != "*" {
		t.Fatalf("expected 1 wildcard row, got %+v", created.Data.Rows)
	}

	// Pins that the row round-trips its own token/model/settings:
	// assert the persisted values, not just a 201.
	{
		var withModel struct {
			Data struct {
				Rows []struct {
					TokenID  *int64         `json:"token_id"`
					Model    string         `json:"model"`
					Settings map[string]any `json:"settings"`
				} `json:"rows"`
			} `json:"data"`
		}
		rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
			map[string]any{"name": "with-settings", "rows": []map[string]any{
				llmRow(map[string]any{"settings": map[string]any{"temperature": 0.4}}),
			}}, ownerA.ID, tenantA.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		testutil.DecodeJSON(t, rec, &withModel)
		row := withModel.Data.Rows[0]
		if row.TokenID == nil || *row.TokenID != llmTok.ID {
			t.Fatalf("token_id should round-trip, got %v", row.TokenID)
		}
		if row.Model != "gpt-4o" {
			t.Fatalf("model should round-trip, got %q", row.Model)
		}
		if row.Settings["temperature"] != 0.4 {
			t.Fatalf("settings should round-trip, got %v", row.Settings)
		}
	}

	// A token is required — there is no provider to fall back to.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "no-token", "rows": []map[string]any{
			{"system_prompt_id": sysPrompt.ID, "bad_output_action": "reject"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// The model must be in the token vendor's catalog.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "bad-model", "rows": []map[string]any{
			llmRow(map[string]any{"model": "not-a-real-model"}),
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Another tenant's token can't be borrowed (cross-tenant guard).
	tokB := testutil.SeedAIVendorToken(t, q, tenantB.ID, "openai", "Default", "enc")
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "cross-tenant-token", "rows": []map[string]any{
			llmRow(map[string]any{"token_id": tokB.ID}),
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Name required.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "  ", "rows": []map[string]any{llmRow(nil)}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// An extension key makes an extension QE+APE engine — no prompts needed;
	// the model is the extension's id.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "ext-review", "rows": []map[string]any{
			{"token_id": extTok.ID, "model": "acme-qe"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var extEng engResp
	testutil.DecodeJSON(t, rec, &extEng)
	if len(extEng.Data.Rows) != 1 || extEng.Data.Rows[0].SystemPromptID != nil {
		t.Fatalf("expected 1 extension row with null system prompt, got %+v", extEng.Data.Rows)
	}
	// Any other model name is refused.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "ext-wrong-model", "rows": []map[string]any{
			{"token_id": extTok.ID, "model": "gpt-4o"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "acme-qe") {
		t.Errorf("want a message naming the extension id, got %s", rec.Body.String())
	}
	// A key whose extension this tenant has not installed is refused, even
	// when another tenant installed one with that id.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "ext-stray", "rows": []map[string]any{
			{"token_id": strayTok.ID, "model": "other-qe"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "is not added") {
		t.Errorf("want 'is not added', got %s", rec.Body.String())
	}

	// ── Engine purpose ──
	// An MT prompt for the purpose checks: out_text only, no status/comments.
	mtPrompt, err := q.CreateAIPrompt(context.Background(), db.CreateAIPromptParams{
		TenantID: tenantA.ID, Name: "mt-prompt", PromptText: "translate", OutText: true,
	})
	if err != nil {
		t.Fatalf("seed mt prompt: %v", err)
	}
	// A qe_ape prompt: status + text together (promptMode). The other rejection
	// cases use `sysPrompt`, which is status-only and so reads back as "QE" —
	// only this shape reaches the qe_ape label.
	qeApePrompt, err := q.CreateAIPrompt(context.Background(), db.CreateAIPromptParams{
		TenantID: tenantA.ID, Name: "qe-ape-prompt", PromptText: "judge and fix", OutStatus: true, OutText: true,
	})
	if err != nil {
		t.Fatalf("seed qe_ape prompt: %v", err)
	}

	// An MT-purpose engine with an MT prompt is valid, and the purpose persists.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "mt-engine", "purpose": "mt", "rows": []map[string]any{
			llmRow(map[string]any{"system_prompt_id": mtPrompt.ID}),
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var mtEngine struct {
		Data struct {
			ID      int64  `json:"id"`
			Purpose string `json:"purpose"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &mtEngine)
	if mtEngine.Data.Purpose != "mt" {
		t.Fatalf("expected persisted purpose 'mt', got %q", mtEngine.Data.Purpose)
	}

	// Omitted purpose defaults to qe_ape.
	if created.Data.Purpose != "qe_ape" {
		t.Fatalf("omitted purpose must default to qe_ape, got %q", created.Data.Purpose)
	}

	// Duplicate names are allowed (migration 083) — the form defaults every new
	// engine to "New AI engine", so the second create must not collide.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "Reviewer", "rows": []map[string]any{llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// An unknown purpose is rejected.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "bad-purpose", "purpose": "everything", "rows": []map[string]any{llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// An MT engine can't use a QE prompt…
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "mt-qe-prompt", "purpose": "mt", "rows": []map[string]any{llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// …nor a QE+APE one. The message names the prompt's mode, so it pins the
	// spelling the user sees: the label is derived from the `qe_ape` slug, which
	// no search for a literal "QE+APE" would find.
	//
	// Anti-tautology RED proof: derive the label with
	// strings.ReplaceAll(mode, "_", "/") → the body reads "QE/APE" and this fails.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "mt-qeape-prompt", "purpose": "mt", "rows": []map[string]any{
			llmRow(map[string]any{"system_prompt_id": qeApePrompt.ID}),
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "QE+APE prompt") {
		t.Fatalf("want a 'QE+APE prompt' message, got %s", body)
	}

	// …nor an extension (an extension reviews, it can't machine-translate).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "mt-ext", "purpose": "mt", "rows": []map[string]any{
			{"token_id": extTok.ID, "model": "acme-qe"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// An MT VENDOR (DeepL/Google) can't back an AI engine of either purpose: it
	// is a translation endpoint, not a model — no QE+APE output contract, no
	// prompt. Its credential belongs in an mt_engines row.
	// Both purposes are checked: the rule holds for either one.
	deeplTok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "deepl", "Default", "enc")
	for _, purpose := range []string{"qe_ape", "mt"} {
		rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
			map[string]any{"name": "vendor-" + purpose, "purpose": purpose, "rows": []map[string]any{
				llmRow(map[string]any{"token_id": deeplTok.ID, "model": "deepl"}),
			}}, ownerA.ID, tenantA.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		// Named for what it is, not "invalid model" — DeepL has no registered
		// models, so the generic message would win without the ordered check.
		if body := rec.Body.String(); !strings.Contains(body, "translation vendor") {
			t.Fatalf("purpose %s: want a 'translation vendor' message, got %s", purpose, body)
		}
	}

	// A QE+APE engine can't use an MT prompt.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "qe-mt-prompt", "purpose": "qe_ape", "rows": []map[string]any{
			llmRow(map[string]any{"system_prompt_id": mtPrompt.ID}),
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Purpose is immutable after creation.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/engines/%d", mtEngine.Data.ID),
		map[string]any{"name": "mt-engine", "purpose": "qe_ape", "rows": []map[string]any{llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// An engine with no rows / nil rows is rejected (needs exactly one provider).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "no-rows", "rows": []map[string]any{}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "nil-rows"}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// More than one row is rejected (exactly one provider per engine).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "two-rows", "rows": []map[string]any{llmRow(nil), llmRow(nil)}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// A provider is required.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "no-provider", "rows": []map[string]any{
			{"system_prompt_id": sysPrompt.ID, "bad_output_action": "reject"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// System prompt is required on an LLM row.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "no-sys-prompt", "rows": []map[string]any{
			{"token_id": llmTok.ID, "model": "gpt-4o", "bad_output_action": "reject"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Bad-output action is required on an LLM row.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "no-action", "rows": []map[string]any{
			{"token_id": llmTok.ID, "model": "gpt-4o", "system_prompt_id": sysPrompt.ID},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Invalid bad_output_action is rejected.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "bad-action", "rows": []map[string]any{llmRow(map[string]any{"bad_output_action": "explode"})}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Cross-tenant system prompt reference is rejected (tenant-scoped lookup).
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "cross", "rows": []map[string]any{
			{"token_id": llmTok.ID, "model": "gpt-4o", "system_prompt_id": promptB.ID, "bad_output_action": "reject"},
		}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Role gating: PM can't create, linguist can't list.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "pm", "rows": []map[string]any{llmRow(nil)}}, pmA.ID, tenantA.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/engines", nil, linguistA.ID, tenantA.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Tenant isolation on the list.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/engines", nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listB struct {
		Data []struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listB)
	if len(listB.Data) != 0 {
		t.Fatalf("tenant B must not see tenant A engines, got %d", len(listB.Data))
	}

	// Update replaces the row and renames.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/engines/%d", engineID),
		map[string]any{"name": "Reviewer v2", "rows": []map[string]any{llmRow(nil)}}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated engResp
	testutil.DecodeJSON(t, rec, &updated)
	if updated.Data.Name != "Reviewer v2" || len(updated.Data.Rows) != 1 {
		t.Fatalf("expected renamed engine with 1 row, got %q / %d rows", updated.Data.Name, len(updated.Data.Rows))
	}

	// Delete guard: a live AI router referencing the engine — as default or
	// in a rule — blocks deletion; once the router is gone the delete goes
	// through.
	asDefault := testutil.SeedAIRouter(t, q, tenantA.ID, "uses as default", &engineID)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/engines/%d", engineID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if err := q.SoftDeleteAIRouter(context.Background(), db.SoftDeleteAIRouterParams{ID: asDefault.ID, TenantID: tenantA.ID}); err != nil {
		t.Fatalf("retire router: %v", err)
	}
	inRule := testutil.SeedAIRouter(t, q, tenantA.ID, "uses in a rule", nil)
	testutil.SeedAIRouterRule(t, q, inRule.ID, "fr", engineID)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/engines/%d", engineID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if err := q.SoftDeleteAIRouter(context.Background(), db.SoftDeleteAIRouterParams{ID: inRule.ID, TenantID: tenantA.ID}); err != nil {
		t.Fatalf("retire router: %v", err)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/engines/%d", engineID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

// `enabled` is part of the UPDATE statement, which is a
// trap: sqlc sends Go's zero value for an omitted param, so an update that says
// nothing about the switch would quietly turn every engine off. Absent must
// mean KEEP.
func TestIntegrationAIEngineUpdateKeepsTheSwitch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SwitchTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@switch.test", "pass", "owner")
	testutil.SeedExtension(t, q, tenant.ID, "acme-qe", "qe")
	tok := testutil.SeedAIVendorToken(t, q, tenant.ID, "ext:acme-qe", "ext", "ENC(ext-key)")

	engine, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{
		TenantID: tenant.ID, Name: "Reviewer", Purpose: "qe_ape",
	})
	if err != nil {
		t.Fatalf("seed engine: %v", err)
	}
	if _, err := q.CreateAIEngineRow(context.Background(), db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "*", TargetLangs: []string{"*"},
		TokenID: &tok.ID, Model: "acme-qe", Settings: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	body := map[string]any{
		"name": "Reviewer renamed",
		"rows": []map[string]any{{
			"source_lang": "*", "target_langs": []string{"*"},
			"token_id": tok.ID, "model": "acme-qe", "settings": map[string]any{},
		}},
	}
	// No `enabled` in the payload — a rename must not touch the switch.
	rec := testutil.DoRequestWithRole(t, router, "PATCH",
		fmt.Sprintf("/api/ai/engines/%d", engine.ID), body, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	after, err := q.GetAIEngine(context.Background(), db.GetAIEngineParams{ID: engine.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Enabled {
		t.Fatal("a rename switched the engine off — absent `enabled` must keep the stored value")
	}

	// ...and an explicit false still works, or the switch would do nothing.
	body["enabled"] = false
	rec = testutil.DoRequestWithRole(t, router, "PATCH",
		fmt.Sprintf("/api/ai/engines/%d", engine.ID), body, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	after, _ = q.GetAIEngine(context.Background(), db.GetAIEngineParams{ID: engine.ID, TenantID: tenant.ID})
	if after.Enabled {
		t.Error("an explicit enabled=false did not switch the engine off")
	}
	// The list reports it, which is what every warning reads.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/engines", nil, owner.ID, tenant.ID, "owner")
	if !strings.Contains(rec.Body.String(), `"unusable":"disabled"`) {
		t.Errorf("a switched-off engine is not flagged: %s", rec.Body.String())
	}
}
