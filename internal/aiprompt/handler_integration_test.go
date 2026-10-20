// AI prompt registry: CRUD, tenant isolation, role gating, the
// at-least-one-output rule, and the engine-reference delete guard.
package aiprompt_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/ai"
	"openloc/internal/aiprompt"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationAIPrompts(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	pmA := testutil.SeedUserWithRole(t, q, tenantA.ID, "pmA@test.com", "pass", "project_manager")
	linguistA := testutil.SeedUserWithRole(t, q, tenantA.ID, "lingA@test.com", "pass", "linguist")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	type idResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	type listResp struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}

	// Owner creates a valid prompt (name + text + at least one output). Provider,
	// model and settings live on the AI engine row, not the prompt.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{"name": "QE reviewer", "prompt_text": "Rate this", "out_status": true},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created idResp
	testutil.DecodeJSON(t, rec, &created)
	promptID := created.Data.ID

	// Changing a saved type is rejected before any instructions are written.
	for _, flags := range []map[string]any{
		{"out_status": true, "out_text": true},
		{"out_text": true, "out_comments": true},
		{"out_text": true},
	} {
		flags["name"] = "Changed type"
		flags["prompt_text"] = "Must not be saved"
		rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID), flags, ownerA.ID, tenantA.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		stored, err := q.GetAIPrompt(context.Background(), db.GetAIPromptParams{ID: promptID, TenantID: tenantA.ID})
		if err != nil || stored.PromptText != "Rate this" || !stored.OutStatus || stored.OutText {
			t.Fatalf("rejected type change modified prompt: %+v, %v", stored, err)
		}
	}

	// Ordinary edits keep working when the type stays QE.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		map[string]any{"name": "QE reviewer", "prompt_text": "Updated instructions", "out_status": true},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	stored, err := q.GetAIPrompt(context.Background(), db.GetAIPromptParams{ID: promptID, TenantID: tenantA.ID})
	if err != nil || stored.PromptText != "Updated instructions" {
		t.Fatalf("same-type update was not saved: %+v, %v", stored, err)
	}

	// A prompt with NO output format selected is rejected.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{"name": "No outputs", "prompt_text": "x"},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Empty name is rejected.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{"name": "  ", "prompt_text": "x", "out_text": true},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// PM (manager but not owner/admin) cannot create.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{"name": "PM prompt", "prompt_text": "x", "out_text": true},
		pmA.ID, tenantA.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// PM CAN list (manager-read). Listing lazily provisions the built-in
	// default templates, plus the custom prompt.
	defaults := len(aiprompt.DefaultPrompts)
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, pmA.ID, tenantA.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listA listResp
	testutil.DecodeJSON(t, rec, &listA)
	if len(listA.Data) != defaults+1 {
		t.Fatalf("tenant A expected %d prompts (defaults + QE reviewer), got %d", defaults+1, len(listA.Data))
	}

	// Linguist cannot even list.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, linguistA.ID, tenantA.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Tenant isolation: tenant B sees none of tenant A's prompts — only its
	// own lazily provisioned defaults.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listB listResp
	testutil.DecodeJSON(t, rec, &listB)
	if len(listB.Data) != defaults {
		t.Fatalf("tenant B must see only its %d default prompts, got %d", defaults, len(listB.Data))
	}
	for _, p := range listB.Data {
		if !p.IsDefault {
			t.Fatalf("tenant B must not see tenant A's custom prompt, got %q", p.Name)
		}
	}

	// Delete guard: create an engine whose row references the prompt, then the
	// prompt delete must 409 (an engine row still uses it). The row also needs a
	// (required) bad-output action.
	tok := testutil.SeedAIVendorToken(t, q, tenantA.ID, "openai", "Default", "enc")
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "eng", "rows": []map[string]any{
			{"source_lang": "en", "target_langs": []string{"fr"}, "system_prompt_id": promptID, "token_id": tok.ID, "model": "gpt-4o", "bad_output_action": "reject"},
		}},
		ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/prompts/%d", promptID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
}

// Every tenant gets the read-only stock templates on first list —
// undeletable, provisioned idempotently.
func TestIntegrationAIPrompts_Defaults(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DefaultsCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@defaults.test", "pass", "owner")

	type promptRow struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		PromptText string `json:"prompt_text"`
		IsDefault  bool   `json:"is_default"`
		OutStatus  bool   `json:"out_status"`
		OutText    bool   `json:"out_text"`
	}
	type listResp struct {
		Data []promptRow `json:"data"`
	}

	// First list provisions the defaults; a second list must not duplicate.
	for range 2 {
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var list listResp
		testutil.DecodeJSON(t, rec, &list)
		if len(list.Data) != len(aiprompt.DefaultPrompts) {
			t.Fatalf("expected exactly %d default prompts, got %d", len(aiprompt.DefaultPrompts), len(list.Data))
		}
	}

	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list listResp
	testutil.DecodeJSON(t, rec, &list)

	// Every stock template is provisioned once, read-only, with its own text and
	// mode-defining flags.
	stock := map[string]int{}
	for i, d := range aiprompt.DefaultPrompts {
		stock[d.Name] = i
	}
	for _, p := range list.Data {
		i, ok := stock[p.Name]
		if !ok {
			t.Fatalf("unexpected default prompt %q", p.Name)
		}
		delete(stock, p.Name)
		d := aiprompt.DefaultPrompts[i]
		if !p.IsDefault {
			t.Errorf("%s: must be flagged is_default", p.Name)
		}
		if p.PromptText != d.PromptText {
			t.Errorf("%s: text %q, want the stock template", p.Name, p.PromptText)
		}
		if p.OutStatus != d.OutStatus || p.OutText != d.OutText {
			t.Errorf("%s: flags status=%v text=%v, want status=%v text=%v", p.Name, p.OutStatus, p.OutText, d.OutStatus, d.OutText)
		}
	}
	if len(stock) != 0 {
		t.Fatalf("missing default prompts: %v", stock)
	}

	// The field meanings are instruction text, not contract: the platform does
	// not append them, so the translation template has to carry its own. What
	// the INPUT keys mean is not here: a segment arrives as JSON with named keys,
	// and "tm_matches" or "source" needs no gloss for a model to read it.
	var mt *promptRow
	for i := range list.Data {
		if list.Data[i].Name == "Default" {
			mt = &list.Data[i]
		}
	}
	if mt == nil {
		t.Fatal("the Default translation prompt is missing")
	}
	if !strings.Contains(mt.PromptText, `"target"`) {
		t.Errorf("Default must explain \"target\" in its own text")
	}
	if mt.OutStatus || !mt.OutText {
		t.Errorf("Default: flags status=%v text=%v, want an MT prompt", mt.OutStatus, mt.OutText)
	}

	def := list.Data[0]
	// Read-only: update rejects.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", def.ID),
		map[string]any{"name": def.Name, "prompt_text": "hacked", "out_status": true},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Undeletable: delete rejects with 409.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/prompts/%d", def.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Self-healing: a drifted default row (e.g. an older code version's
	// template) is refreshed to the current stock text on the next list.
	if _, err := pool.Exec(context.Background(),
		`UPDATE ai_prompts SET prompt_text = 'stale wording' WHERE id = $1`, def.ID); err != nil {
		t.Fatalf("tamper default: %v", err)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var after struct {
		Data []struct {
			ID         int64  `json:"id"`
			PromptText string `json:"prompt_text"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &after)
	found := false
	for _, p := range after.Data {
		if p.ID != def.ID {
			continue
		}
		found = true
		if p.PromptText != def.PromptText {
			t.Errorf("drifted default reads %q, want it refreshed to the stock template", p.PromptText)
		}
	}
	if !found {
		t.Fatalf("default prompt %d missing from the list after the heal", def.ID)
	}
}

// Language additions are stored with the prompt and come back with it, so the
// editor can load and re-save a prompt without losing them.
func TestIntegrationAIPromptLanguageAdditions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AdditionsTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	type addition struct {
		TargetLanguages []string `json:"target_languages"`
		Text            string   `json:"text"`
	}
	type promptResp struct {
		Data struct {
			ID                int64      `json:"id"`
			LanguageAdditions []addition `json:"language_additions"`
		} `json:"data"`
	}

	// Create with two additions.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{
			"name": "Reviewer", "prompt_text": "Be strict.", "out_status": true,
			"language_additions": []map[string]any{
				{"target_languages": []string{"DE", "de-AT"}, "text": "Use formal address."},
				{"target_languages": []string{"fr"}, "text": "Use the vouvoiement."},
			},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created promptResp
	testutil.DecodeJSON(t, rec, &created)
	promptID := created.Data.ID

	if len(created.Data.LanguageAdditions) != 2 {
		t.Fatalf("got %d additions, want 2", len(created.Data.LanguageAdditions))
	}
	// Codes come back normalized, and the duplicate base variant is kept (de
	// and de-AT are different codes), while case and separators are not.
	first := created.Data.LanguageAdditions[0]
	if first.TargetLanguages[0] != "de" || first.TargetLanguages[1] != "de-at" {
		t.Errorf("target languages = %v, want normalized [de de-at]", first.TargetLanguages)
	}
	// Submitted order is the append order the model will read.
	if created.Data.LanguageAdditions[1].Text != "Use the vouvoiement." {
		t.Errorf("additions came back out of order: %+v", created.Data.LanguageAdditions)
	}

	// They come back on the list too, so the editor needs one request.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			ID                int64      `json:"id"`
			LanguageAdditions []addition `json:"language_additions"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listed)
	var found bool
	for _, p := range listed.Data {
		if p.ID == promptID {
			found = true
			if len(p.LanguageAdditions) != 2 {
				t.Errorf("list: got %d additions, want 2", len(p.LanguageAdditions))
			}
		} else if p.LanguageAdditions == nil {
			// Never null: a client must not have to handle two spellings of none.
			t.Errorf("prompt %d: language_additions must be [] not null", p.ID)
		}
	}
	if !found {
		t.Fatal("created prompt missing from the list")
	}

	// Update replaces the whole set.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		map[string]any{
			"name": "Reviewer", "prompt_text": "Be strict.", "out_status": true,
			"language_additions": []map[string]any{{"target_languages": []string{"ja"}, "text": "Keep honorifics."}},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var updated promptResp
	testutil.DecodeJSON(t, rec, &updated)
	if len(updated.Data.LanguageAdditions) != 1 || updated.Data.LanguageAdditions[0].TargetLanguages[0] != "ja" {
		t.Errorf("update must replace the whole set, got %+v", updated.Data.LanguageAdditions)
	}

	// Omitting the field clears them — a replace-all write with none submitted.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		map[string]any{"name": "Reviewer", "prompt_text": "Be strict.", "out_status": true},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	updated = promptResp{}
	testutil.DecodeJSON(t, rec, &updated)
	if len(updated.Data.LanguageAdditions) != 0 {
		t.Errorf("got %+v, want none", updated.Data.LanguageAdditions)
	}

	// A partly-filled addition is rejected rather than stored as a rule that
	// silently never fires — or, worse, one that matches every job.
	for _, bad := range []map[string]any{
		{"target_languages": []string{}, "text": "orphan text"},
		{"target_languages": []string{"de"}, "text": "   "},
	} {
		rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
			map[string]any{
				"name": fmt.Sprintf("Bad %v", bad["text"]), "prompt_text": "x", "out_status": true,
				"language_additions": []map[string]any{bad},
			}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	}

	// An entirely blank row is someone changing their mind — dropped, not an error.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{
			"name": "Blank row", "prompt_text": "x", "out_status": true,
			"language_additions": []map[string]any{{"target_languages": []string{}, "text": "  "}},
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Deleting the prompt takes its additions with it (ON DELETE CASCADE).
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	var orphans int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ai_prompt_language_additions WHERE prompt_id = $1`, promptID).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d additions outlived their prompt", orphans)
	}
}

// The three language-addition queries are keyed on prompt_id alone, so the only
// thing standing between one tenant and another's rules is the tenant-scoped
// prompt lookup each write does first. This drives a write from the wrong
// tenant and checks both halves: the request is refused, AND the rows are still
// there afterwards. Asserting only the 404 would pass even if the write had
// already gone through.
func TestIntegrationAIPromptLanguageAdditionsTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "AdditionsTenantA")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "ownerA@test.com", "pass", "owner")
	tenantB := testutil.SeedTenant(t, q, "AdditionsTenantB")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@test.com", "pass", "owner")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts",
		map[string]any{
			"name": "A's reviewer", "prompt_text": "Be strict.", "out_status": true,
			"language_additions": []map[string]any{
				{"target_languages": []string{"de"}, "text": "Use formal address."},
			},
		}, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	promptID := created.Data.ID

	additions := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM ai_prompt_language_additions WHERE prompt_id = $1`, promptID).Scan(&n); err != nil {
			t.Fatalf("count additions: %v", err)
		}
		return n
	}
	if n := additions(); n != 1 {
		t.Fatalf("fixture: %d additions, want 1", n)
	}

	// B replaces A's set. Replace-all means a successful write would delete
	// A's rule and insert B's — the silent cross-tenant overwrite.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		map[string]any{
			"name": "B's edit", "prompt_text": "Be lenient.", "out_status": true,
			"language_additions": []map[string]any{
				{"target_languages": []string{"ja"}, "text": "Keep honorifics."},
			},
		}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if n := additions(); n != 1 {
		t.Errorf("after B's PATCH: %d additions, want A's 1 untouched", n)
	}

	// B empties A's set — the same write path with nothing submitted, which is
	// the delete half on its own.
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		map[string]any{"name": "B's edit", "prompt_text": "Be lenient.", "out_status": true},
		ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if n := additions(); n != 1 {
		t.Errorf("after B's clearing PATCH: %d additions, want A's 1 untouched", n)
	}

	// B deletes A's prompt, which would cascade the additions away.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/ai/prompts/%d", promptID),
		nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if n := additions(); n != 1 {
		t.Errorf("after B's DELETE: %d additions, want A's 1 untouched", n)
	}

	// A's own rule still reads back through the API, unchanged.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/prompts", nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			ID                int64 `json:"id"`
			LanguageAdditions []struct {
				TargetLanguages []string `json:"target_languages"`
				Text            string   `json:"text"`
			} `json:"language_additions"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listed)
	for _, p := range listed.Data {
		if p.ID != promptID {
			continue
		}
		if len(p.LanguageAdditions) != 1 || p.LanguageAdditions[0].Text != "Use formal address." {
			t.Errorf("A's addition = %+v, want the original", p.LanguageAdditions)
		}
	}
}

// The preview exists so the editor never re-implements the appended half of
// the prompt. It is only worth having if it returns the same bytes the runtime
// sends, so that is what this asserts — against ai's own builders, not against
// a copy of the expected text.
func TestIntegrationAIPromptPreview(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PreviewTenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")

	type previewResp struct {
		Data struct {
			Instruction  string `json:"instruction"`
			Appended     string `json:"appended"`
			SystemPrompt string `json:"system_prompt"`
			Mode         string `json:"mode"`
		} `json:"data"`
	}

	// Either provider resolves a schema without needing a model selection or key.
	schemas := map[string]string{}
	for _, provider := range []string{ai.ProviderOpenAI, ai.ProviderClaude} {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
			map[string]any{"provider": provider, "prompt_text": "Review.", "out_status": true},
			owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var response struct {
			Data struct {
				SchemaEnforced bool           `json:"schema_enforced"`
				OutputSchema   map[string]any `json:"output_schema"`
				OutputFormat   string         `json:"output_format"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &response)
		// Both vendors are held to the SAME schema — they differ only in the
		// request field it rides in, which is not something a prompt author
		// chooses. So the preview shows one shape, identical either way.
		if !response.Data.SchemaEnforced {
			t.Fatalf("%s default preview must report its schema as enforced: %+v", provider, response.Data)
		}
		if response.Data.OutputSchema["type"] != "object" {
			t.Fatalf("%s preview must carry the reply schema, got %+v", provider, response.Data.OutputSchema)
		}
		schemas[provider] = fmt.Sprint(response.Data.OutputSchema)
		if !strings.Contains(response.Data.OutputFormat, "Return exactly one result") {
			t.Fatalf("%s must retain result alignment instructions", provider)
		}
	}
	if schemas[ai.ProviderOpenAI] != schemas[ai.ProviderClaude] {
		t.Errorf("the two vendors must be shown the same schema:\n openai: %s\n claude: %s",
			schemas[ai.ProviderOpenAI], schemas[ai.ProviderClaude])
	}

	// QE+APE flags, concrete languages.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{
			"prompt_text": "Be strict.", "out_status": true, "out_text": true,
			"source_lang": "en", "target_lang": "de",
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got previewResp
	testutil.DecodeJSON(t, rec, &got)

	want := ai.AssemblePrompt(ai.ModeQEAPE, "Be strict.", "en", "de", "")
	if got.Data.SystemPrompt != want.SystemPrompt {
		t.Errorf("preview differs from the assembled prompt\n got: %q\nwant: %q", got.Data.SystemPrompt, want.SystemPrompt)
	}
	if got.Data.Instruction+got.Data.Appended != got.Data.SystemPrompt {
		t.Errorf("instruction + appended must rebuild the system prompt")
	}
	if got.Data.Mode != "qe_ape" {
		t.Errorf("mode = %q, want qe_ape", got.Data.Mode)
	}

	// The flags pick the mode: text-only with a comment is APE, and the
	// contract must offer a postedit.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{"prompt_text": "Fix it.", "out_text": true, "out_comments": true},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Mode != "ape" {
		t.Errorf("mode = %q, want ape", got.Data.Mode)
	}
	// The fields live in the schema the preview returns, not in the appended
	// prose — with no model in hand a schema is assumed, so printing them in
	// words too would describe the same contract twice.
	if strings.Contains(got.Data.Appended, `"postedit"`) {
		t.Errorf("the appended half must leave the fields to the schema:\n%s", got.Data.Appended)
	}
	// The language pair belongs to the instruction, so the appended half
	// never names it — with or without concrete languages.
	if strings.Contains(got.Data.Appended, "<source_lang>") || strings.Contains(got.Data.Appended, " to de") {
		t.Errorf("the appended half must not name the pair:\n%s", got.Data.Appended)
	}

	// …and a prompt that uses the tokens gets them resolved in its instruction.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{
			"prompt_text": "Review this <source_lang> to <target_lang> text.",
			"out_status":  true, "source_lang": "en", "target_lang": "de",
		}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if !strings.Contains(got.Data.Instruction, "Review this English (en) to German (de) text.") {
		t.Errorf("the language tokens must resolve in the instruction, got: %q", got.Data.Instruction)
	}

	// With a model in hand the preview also answers whether that model has the
	// reply shape enforced, which is what the engine row shows.
	type enforcedResp struct {
		Data struct {
			SchemaEnforced bool           `json:"schema_enforced"`
			OutputSchema   map[string]any `json:"output_schema"`
		} `json:"data"`
	}
	var enf enforcedResp
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{"prompt_text": "x", "out_status": true, "model": "claude-opus-5"},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &enf)
	if !enf.Data.SchemaEnforced || enf.Data.OutputSchema == nil {
		t.Errorf("claude-opus-5 takes a schema; got enforced=%v schema=%v", enf.Data.SchemaEnforced, enf.Data.OutputSchema != nil)
	}

	// A model that can't take one says so, and carries no schema — reporting a
	// schema we never send would be worse than reporting none.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{"prompt_text": "x", "out_status": true, "model": "gpt-4-turbo"},
		owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	enf = enforcedResp{}
	testutil.DecodeJSON(t, rec, &enf)
	if enf.Data.SchemaEnforced || enf.Data.OutputSchema != nil {
		t.Errorf("gpt-4-turbo takes no schema; got enforced=%v schema=%v", enf.Data.SchemaEnforced, enf.Data.OutputSchema != nil)
	}

	// Manager-read: a PM picks prompts, so a PM may preview one.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{"prompt_text": "x", "out_text": true}, pm.ID, tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// A linguist is not a manager.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/prompts/preview",
		map[string]any{"prompt_text": "x", "out_text": true}, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}
