// Package flowtest contains Go API flow tests — full user journeys tested via HTTP.
// These are NOT the Playwright E2E tests (those live in /e2e/).
package flowtest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationFullTranslationFlow tests the complete user journey:
// register → create TM → create template → assign TM → create project → create file/segments →
// MT-apply → approve → complete → verify TM populated.
//
// "Full" refers to the user journey (register all the way through TM populated), NOT to
// exercising all pipeline phases at once. The TM is empty at the MT-apply step (it only
// gets populated later by job completion), so what we exercise here is the MT phase in
// isolation via /api/jobs/{jobID}/mt-apply.
//
// If you want a regression guard for tm-apply or for the upload-hook combined phases,
// add a new test — don't repurpose this one.
func TestIntegrationFullTranslationFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Step 1: Register
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "flow")

	// Step 2: Create TM collection
	rec := testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{
		"name": "Flow TM", "source_language": "en", "target_languages": []string{"fr"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmResp)
	tmID := tmResp.Data.ID

	// Step 3: Create template
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Flow Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	// Step 4: Assign TM to template (en → fr, read_write)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", templateID), map[string]any{
		"assignments": []map[string]any{
			{
				"tm_id":           tmID,
				"source_language": "en",
				"target_language": "fr",
				"mode":            "read_write",
				"priority":        1,
			},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 4b: Seed a Mock MT engine, put it behind an MT router and point
	// the template at the router. The pipeline skips MT entirely when the
	// profile has no router — so this test must wire one to exercise the MT
	// phase. Goes through the HTTP API to mirror how the UI would set up
	// the template.
	mtEngine := testutil.SeedMTEngine(t, q, tenantID, "Mock", "mock")
	rec = testutil.DoRequest(t, router, "POST", "/api/mt-routers", map[string]any{
		"name": "Flow MT router", "default": map[string]any{"mt_engine_id": mtEngine.ID},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var flowRouterResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &flowRouterResp)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", templateID), map[string]any{
		"mt_router_id": flowRouterResp.Data.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 5: Create project with template. template_id on POST /api/projects
	// copies the template's assignments (TMs, MT engines, terms, workflow
	// steps) into the project's own settings profile at creation — templates
	// are create-time-only. Without this, the project has no TM assignment
	// and the completion write-back's LookupSettingsWriteTMs returns zero.
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Flow Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
		"template_id":      templateID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// Step 6: Create file + segments + translations directly in DB
	// (Skipping S3 upload since Storage is nil in test router)
	file := testutil.SeedFile(t, q, projectID, "flow.json", db.FileFormatJsonI18n)
	seg1 := testutil.SeedSegment(t, q, file.ID, "Welcome", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Thank you", 2)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Create untranslated translations
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "fr", db.TranslationStatusNeedsReview)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "fr", db.TranslationStatusNeedsReview)

	// Step 7: Trigger MT-apply on the job (manual triggers are
	// per-job, single-phase) to fill translations with MT output.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Verify translations got MT text (Mock engine returns "[FR] ..." prefix)
	got1, err := q.GetTranslation(ctx, tr1.ID)
	if err != nil {
		t.Fatalf("GetTranslation: %v", err)
	}
	if got1.ActiveText == nil {
		t.Error("expected MT text on translation 1 after translate")
	} else if *got1.ActiveText != "[FR] Welcome" {
		t.Errorf("expected '[FR] Welcome', got %q", *got1.ActiveText)
	}

	got2, err := q.GetTranslation(ctx, tr2.ID)
	if err != nil {
		t.Fatalf("GetTranslation: %v", err)
	}
	if got2.ActiveText == nil {
		t.Error("expected MT text on translation 2 after translate")
	}

	// Step 8: Simulate human approval
	humanText1 := "Bienvenue"
	humanText2 := "Merci"
	_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr1.ID,
		HumanText: &humanText1,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}
	_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr2.ID,
		HumanText: &humanText2,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}

	// Step 9: Complete the job via API
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/complete", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var fbResp struct {
		ApprovedCount int    `json:"approved_count"`
		TMSavedCount  int    `json:"tm_saved_count"`
		Warning       string `json:"warning"`
	}
	testutil.DecodeJSON(t, rec, &fbResp)

	// approved_count is the human-approved segment count.
	if fbResp.ApprovedCount != 2 {
		t.Errorf("expected 2 approved, got %d", fbResp.ApprovedCount)
	}
	if fbResp.TMSavedCount != 2 {
		t.Errorf("expected 2 TM saved, got %d", fbResp.TMSavedCount)
	}
	if fbResp.Warning != "" {
		t.Errorf("expected no warning, got %q", fbResp.Warning)
	}

	// Step 10: Verify TM entries exist in the database
	entry, err := q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Welcome",
	})
	if err != nil {
		t.Fatalf("LookupTM 'Welcome': %v (TM not populated!)", err)
	}
	if entry.TargetText != "Bienvenue" {
		t.Errorf("expected 'Bienvenue', got %q", entry.TargetText)
	}

	entry, err = q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Thank you",
	})
	if err != nil {
		t.Fatalf("LookupTM 'Thank you': %v (TM not populated!)", err)
	}
	if entry.TargetText != "Merci" {
		t.Errorf("expected 'Merci', got %q", entry.TargetText)
	}

	t.Log("Full translation flow completed successfully: register → TM → template → project → translate → approve → complete → TM verified")
}

// TestIntegrationTerminologyE2EFlow tests the full terminology user journey:
// create term base → add terms → assign to template → assign template to project →
// upload file → lookup terms in editor → edit term → lookup again (sees update).
//
// This is the flow a localization manager follows: set up terminology, translator sees
// terms highlighted in editor, manager updates a term, translator sees the change.
func TestIntegrationTerminologyE2EFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Step 1: Register user
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "terminology")

	// Step 2: Create a term base (en → de)
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Tech Terms",
		"source_language": "en",
		"target_language": "de",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	termBaseID := baseResp.Data.ID

	// Concept-native helpers: each "term" is a concept with an en source term +
	// a de target term. createConcept returns the concept id; deTermID finds the
	// de term id for in-place edits.
	createConcept := func(src, tgt, status, notes string) int64 {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", termBaseID), map[string]any{
			"terms": []map[string]string{
				{"language": "en", "text": src},
				{"language": "de", "text": tgt, "status": status, "notes": notes},
			},
		}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var r struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &r)
		return r.Data.ID
	}
	deTermID := func(conceptID int64) int64 {
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", termBaseID, conceptID), nil, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var r struct {
			Data struct {
				Terms []struct {
					ID       int64  `json:"id"`
					Language string `json:"language"`
				} `json:"terms"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &r)
		for _, tm := range r.Data.Terms {
			if tm.Language == "de" {
				return tm.ID
			}
		}
		t.Fatalf("de term not found for concept %d", conceptID)
		return 0
	}

	// Step 3: Add terms (as concepts).
	pwConcept := createConcept("password", "Passwort", "approved", "")
	createConcept("email", "E-Mail", "approved", "")

	// Step 4: Create template and assign term base
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Terminology Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", templateID), map[string]any{
		"assignments": []map[string]any{
			{
				"term_base_id":    termBaseID,
				"source_language": "en",
				"target_language": "de",
				"mode":            "read",
				"priority":        1,
			},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 5: Verify template GET includes term assignments
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", templateID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var tmplDetail struct {
		Data struct {
			TermAssignments []struct {
				TermBaseID int64 `json:"term_base_id"`
			} `json:"term_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplDetail)
	if len(tmplDetail.Data.TermAssignments) != 1 {
		t.Fatalf("expected 1 term assignment, got %d", len(tmplDetail.Data.TermAssignments))
	}

	// Step 6: Create project with template (applied at creation)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Terminology Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"template_id":      templateID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// Step 7: Create file + segments (simulating what an uploaded file produces)
	file := testutil.SeedFile(t, q, projectID, "terms.txt", db.FileFormatTxt)
	testutil.SeedSegment(t, q, file.ID, "Reset your password now", 1)
	testutil.SeedSegment(t, q, file.ID, "Enter your email address", 2)
	testutil.SeedSegment(t, q, file.ID, "Welcome to our platform", 3) // no term base match

	// Step 8: Lookup terms for segment 1 — "Reset your password now" should match "password" → "Passwort"
	url := fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Reset+your+password+now", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var lookupResp struct {
		Data []struct {
			SourceTerm string `json:"source_term"`
			TargetTerm string `json:"target_term"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 1 {
		t.Fatalf("expected 1 term match for 'Reset your password now', got %d", len(lookupResp.Data))
	}
	if lookupResp.Data[0].TargetTerm != "Passwort" {
		t.Errorf("expected 'Passwort', got %q", lookupResp.Data[0].TargetTerm)
	}

	// Step 9: Lookup terms for segment 2 — "Enter your email address" should match "email" → "E-Mail"
	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 1 {
		t.Fatalf("expected 1 term match for 'Enter your email address', got %d", len(lookupResp.Data))
	}
	if lookupResp.Data[0].TargetTerm != "E-Mail" {
		t.Errorf("expected 'E-Mail', got %q", lookupResp.Data[0].TargetTerm)
	}

	// Step 10: Lookup terms for segment 3 — no match expected
	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Welcome+to+our+platform", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 0 {
		t.Errorf("expected 0 term matches for 'Welcome to our platform', got %d", len(lookupResp.Data))
	}

	// Step 11: Edit term — change the de term "Passwort" to "Kennwort"
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", termBaseID, pwConcept, deTermID(pwConcept)), map[string]any{
		"language": "de",
		"text":     "Kennwort",
		"status":   "approved",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Step 12: Lookup again — should see "Kennwort" (live update, no cache)
	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Reset+your+password+now", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 1 {
		t.Fatalf("after edit, expected 1 term match, got %d", len(lookupResp.Data))
	}
	if lookupResp.Data[0].TargetTerm != "Kennwort" {
		t.Fatalf("after edit, expected 'Kennwort', got %q", lookupResp.Data[0].TargetTerm)
	}

	// Step 13: Delete the password concept — lookup should return nothing for "password"
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d", termBaseID, pwConcept), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Reset+your+password+now", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 0 {
		t.Fatalf("after deleting term, expected 0 matches for 'password', got %d", len(lookupResp.Data))
	}

	// "email" term should still work (only "password" was deleted)
	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 1 || lookupResp.Data[0].TargetTerm != "E-Mail" {
		t.Fatalf("after deleting 'password', 'email' should still match, got %v", lookupResp.Data)
	}

	// Step 14: Add a forbidden term — should appear in lookup with forbidden status
	createConcept("email", "Elektronische Post", "forbidden", "Do not use this translation")

	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 2 {
		t.Fatalf("expected 2 matches for 'email' (approved + forbidden), got %d", len(lookupResp.Data))
	}
	// Verify both statuses are present
	statuses := map[string]bool{}
	for _, d := range lookupResp.Data {
		statuses[d.Status] = true
	}
	if !statuses["approved"] || !statuses["forbidden"] {
		t.Fatalf("expected both approved and forbidden statuses, got %v", lookupResp.Data)
	}

	// Step 15: Multiple terms matching in one segment
	createConcept("address", "Adresse", "approved", "")

	// "Enter your email address" now matches both "email" and "address"
	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) < 3 {
		t.Fatalf("expected at least 3 matches (email approved + email forbidden + address), got %d", len(lookupResp.Data))
	}

	// Step 16: Unassign term base from template — all lookups should return nothing
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", templateID), map[string]any{
		"assignments": []map[string]any{},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) != 0 {
		t.Fatalf("after unassigning term base, expected 0 matches, got %d", len(lookupResp.Data))
	}

	// Step 17: Re-assign and verify it works again (round-trip)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", templateID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": termBaseID, "source_language": "en", "target_language": "de", "mode": "read", "priority": 1},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	url = fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=de&source_text=Enter+your+email+address", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &lookupResp)
	if len(lookupResp.Data) < 2 {
		t.Fatalf("after re-assigning, expected matches to return, got %d", len(lookupResp.Data))
	}

	t.Log("Terminology E2E flow completed: create → terms → assign → lookup → edit → live update → delete term → forbidden term → multi-match → unassign → re-assign")
}

// TestIntegrationTermAssignmentFanOut verifies the multi-target payoff: a single
// multilingual term base, assigned to a project with one row per shared target
// (as the FE fan-out produces), enforces terminology across EVERY shared target —
// not just the first.
//
// Base has one concept: en "password" + de "Passwort" + fr "mot de passe" (no es
// term). Project is en → {de, fr, es}, assigned with two rows en→de and en→fr.
// Lookup must surface the de term for a de job and the fr term for an fr job; the
// es job (no assignment, no term) must return nothing.
func TestIntegrationTermAssignmentFanOut(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "term-fanout")

	// Term base (source en). target_languages derive from terms.
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Multilingual TB",
		"source_language": "en",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	termBaseID := baseResp.Data.ID

	// One concept with en + de + fr terms (no es).
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", termBaseID), map[string]any{
		"terms": []map[string]string{
			{"language": "en", "text": "password"},
			{"language": "de", "text": "Passwort", "status": "approved"},
			{"language": "fr", "text": "mot de passe", "status": "approved"},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Template + project en → {de, fr, es}.
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Fan-out Template"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	// The fan-out: one assignment row per shared target (en→de, en→fr). es is not
	// assigned because the base has no es term.
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", templateID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": termBaseID, "source_language": "en", "target_language": "de", "mode": "read", "priority": 0},
			{"term_base_id": termBaseID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 1},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	lookup := func(target string) []struct {
		TargetTerm string `json:"target_term"`
	} {
		url := fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=%s&source_text=Reset+your+password+now", templateID, target)
		rec := testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				TargetTerm string `json:"target_term"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data
	}

	// de job → German term surfaces.
	if de := lookup("de"); len(de) != 1 || de[0].TargetTerm != "Passwort" {
		t.Fatalf("de lookup: expected [Passwort], got %v", de)
	}
	// fr job → French term surfaces (the whole point: not just de).
	if fr := lookup("fr"); len(fr) != 1 || fr[0].TargetTerm != "mot de passe" {
		t.Fatalf("fr lookup: expected [mot de passe], got %v", fr)
	}
	// es job → nothing (no es assignment, no es term).
	if es := lookup("es"); len(es) != 0 {
		t.Fatalf("es lookup: expected no matches, got %v", es)
	}
}

// TestIntegrationTermLookupDedupsMultipleAssignments guards against term
// duplication under fan-out: when a term base has MORE THAN ONE assignment row
// that both match the same job language pair (e.g. an en→fr and an en→fr-fr
// row, both base-subtag-matching an fr job), a naive assignment JOIN multiplies
// every term row by the number of matching assignments. Duplicate term.id
// values break the CAT sidebar, which keys its list on them, so stale terms
// leak across segments.
//
// Each concept term must appear exactly ONCE per lookup regardless of how many
// assignment rows cover it.
func TestIntegrationTermLookupDedupsMultipleAssignments(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "term-dedup")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Dup TB", "source_language": "en",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	termBaseID := baseResp.Data.ID

	// One concept, one fr target term.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", termBaseID), map[string]any{
		"terms": []map[string]string{
			{"language": "en", "text": "widget"},
			{"language": "fr", "text": "bidule", "status": "approved"},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Dup Template"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	// TWO assignment rows for the same base that both match an fr job: en→fr and
	// en→fr-fr (fr-fr base-subtag-matches the fr job target).
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", templateID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": termBaseID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 0},
			{"term_base_id": termBaseID, "source_language": "en", "target_language": "fr-fr", "mode": "read", "priority": 1},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	url := fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=fr&source_text=Insert+the+widget+here", templateID)
	rec = testutil.DoRequest(t, router, "GET", url, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			ID         int64  `json:"id"`
			TargetTerm string `json:"target_term"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("expected the term exactly once despite two matching assignments, got %d: %+v", len(resp.Data), resp.Data)
	}
	if resp.Data[0].TargetTerm != "bidule" {
		t.Errorf("expected 'bidule', got %q", resp.Data[0].TargetTerm)
	}
}

// TestIntegrationQACheckFlow tests the QA check journey:
// register → create QA profile → add rules → create template (with QA profile) →
// create project → upload file → run QA → verify results → check report.
func TestIntegrationQACheckFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Step 1: Register
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "qa")

	// Step 2: Create QA profile
	rec := testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name":        "Strict QA",
		"description": "All checks enabled",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var profileResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profileResp)
	profileID := profileResp.Data.ID
	t.Logf("Step 2: Created QA profile ID=%d", profileID)

	// Step 3: Add QA rules
	for _, checkType := range []string{"tag_checks", "number_consistency", "empty_target", "punctuation"} {
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", profileID), map[string]any{
			"check_type": checkType,
			"enabled":    true,
			"severity":   "error",
		}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	t.Log("Step 3: Added 4 QA rules")

	// Step 4: Verify rules via GET
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/qa-profiles/%d", profileID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var getProfileResp struct {
		Data struct {
			Rules []struct {
				CheckType string `json:"check_type"`
				Enabled   bool   `json:"enabled"`
			} `json:"rules"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &getProfileResp)
	if len(getProfileResp.Data.Rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(getProfileResp.Data.Rules))
	}
	t.Log("Step 4: Verified 4 rules via GET")

	// Step 5: Create template with QA profile
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name":            "QA Template",
		"mt_on_upload":    false,
		"fuzzy_threshold": 0.5,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	// Assign QA profile to template
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", templateID), map[string]any{
		"name":          "QA Template",
		"qa_profile_id": profileID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	t.Logf("Step 5: Created template ID=%d with QA profile", templateID)

	// Step 6: Create project with template (copies QA profile, settings, etc.
	// at creation — templates are create-time-only)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "QA Test Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"template_id":      templateID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID
	t.Logf("Step 6: Created project ID=%d with template applied", projectID)

	// Step 7: Create file, segments, job, and translations directly via DB
	// (file upload requires S3 which isn't available in test env)
	ctx := context.Background()
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID:   projectID,
		Name:        "test_qa.txt",
		Format:      "txt",
		StoragePath: "test/qa.txt",
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	fileID := file.ID

	sources := []string{
		"<b>Welcome</b> to our platform.",
		"You have 42 items in your cart.",
		"Thank you!",
	}
	var segmentIDs []int64
	for i, src := range sources {
		seg, err := q.CreateSegment(ctx, db.CreateSegmentParams{
			FileID:     fileID,
			SourceText: src,
			Position:   int32(i + 1),
		})
		if err != nil {
			t.Fatalf("create segment %d: %v", i, err)
		}
		segmentIDs = append(segmentIDs, seg.ID)
	}

	// Create job
	job, err := q.CreateJob(ctx, db.CreateJobParams{
		FileID:         fileID,
		TargetLanguage: "de",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	jobID := job.ID

	// Create translations with deliberate QA issues
	targets := []string{
		"Willkommen auf unserer Plattform.",     // missing <b></b> tags
		"Sie haben Artikel in Ihrem Warenkorb.", // missing number 42
		"Danke!",                                // OK
	}
	var translationIDs []int64
	for i, tgt := range targets {
		trans, err := q.CreateTranslation(ctx, db.CreateTranslationParams{
			SegmentID:      segmentIDs[i],
			TargetLanguage: "de",
			Status:         "approved",
		})
		if err != nil {
			t.Fatalf("create translation %d: %v", i, err)
		}
		// Set human_text via update
		_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
			ID:        trans.ID,
			HumanText: &tgt,
		})
		if err != nil {
			t.Fatalf("update translation %d human text: %v", i, err)
		}
		translationIDs = append(translationIDs, trans.ID)
	}

	t.Logf("Step 7: Created file ID=%d, 3 segments, job ID=%d, 3 translations", fileID, jobID)

	// Step 10: Run QA
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var qaRunResp struct {
		Data struct {
			Issues  int    `json:"issues"`
			Message string `json:"message"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &qaRunResp)
	if qaRunResp.Data.Issues == 0 {
		t.Fatal("expected QA to find issues, got 0")
	}
	t.Logf("Step 10: QA found %d issues", qaRunResp.Data.Issues)

	// Step 11: Get QA report
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/qa-report", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var reportResp struct {
		Data struct {
			Summary map[string]int `json:"summary"`
			Results []struct {
				CheckType string `json:"check_type"`
				Severity  string `json:"severity"`
				Message   string `json:"message"`
			} `json:"results"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &reportResp)

	if reportResp.Data.Summary["error"] == 0 {
		t.Fatal("expected error-level issues in report")
	}

	// Verify specific issues exist
	checkTypes := make(map[string]bool)
	for _, r := range reportResp.Data.Results {
		checkTypes[r.CheckType] = true
	}
	if !checkTypes["tag_missing"] {
		t.Error("expected tag_missing issue (missing <b></b>)")
	}
	if !checkTypes["number_consistency"] {
		t.Error("expected number_consistency issue (missing 42)")
	}
	t.Logf("Step 11: QA report has %d errors, %d warnings", reportResp.Data.Summary["error"], reportResp.Data.Summary["warning"])

	// Step 12: Fix translation 1 (add tags back) and re-run QA
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, translationIDs[0]),
		map[string]string{"human_text": "<b>Willkommen</b> auf unserer Plattform."},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", jobID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaRunResp)

	// Should have fewer issues now
	t.Logf("Step 12: After fix, QA found %d issues (was %d)", qaRunResp.Data.Issues, reportResp.Data.Summary["error"]+reportResp.Data.Summary["warning"])

	// Step 13: Per-translation QA check
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/translations/%d/qa", translationIDs[1]),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var perTransResp struct {
		Data []struct {
			CheckType string `json:"check_type"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &perTransResp)
	if len(perTransResp.Data) == 0 {
		t.Error("expected QA issues on translation 2 (missing number)")
	}
	t.Logf("Step 13: Translation 2 has %d QA issues", len(perTransResp.Data))

	t.Log("QA E2E flow completed: create profile → add rules → assign to template → upload → translate → run QA → verify report → fix → re-run → per-translation check")
}

// TestIntegrationDraftSaveRichTextQA tests that saving a draft with XLIFF tag removal
// is detected by QA: a draft save must update human_rich_text as well as human_text,
// or QA checks a stale active_rich_text.
func TestIntegrationDraftSaveRichTextQA(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Register user
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "draftqa")

	// Create QA profile with tag_checks rule
	rec := testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name": "Tag QA Profile",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var profileResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profileResp)
	profileID := profileResp.Data.ID

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", profileID), map[string]any{
		"check_type": "tag_checks",
		"enabled":    true,
		"severity":   "error",
		"config":     map[string]bool{"missing": true, "extra": true, "unclosed": true, "unopened": true},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Create template with QA profile
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "DraftQA Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID

	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", templateID), map[string]any{
		"name":          "DraftQA Template",
		"qa_profile_id": profileID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Create project with template (applied at creation)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "DraftQA Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
		"template_id":      templateID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// Create file + segment with XLIFF rich text
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID: projectID, Name: "draft_qa.xliff", Format: "xliff", StoragePath: "test/draft_qa.xliff",
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}

	sourceRich := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue.`
	seg, err := q.CreateSegment(ctx, db.CreateSegmentParams{
		FileID: file.ID, SourceText: "Click here to continue.", Position: 1,
	})
	if err != nil {
		t.Fatalf("create segment: %v", err)
	}
	// Set source_rich_text directly
	_, err = pool.Exec(ctx, "UPDATE segments SET source_rich_text = $1 WHERE id = $2", sourceRich, seg.ID)
	if err != nil {
		t.Fatalf("set source_rich_text: %v", err)
	}

	// Create job
	job, err := q.CreateJob(ctx, db.CreateJobParams{
		FileID: file.ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Create translation with full XLIFF tags in active_rich_text
	targetRich := `Haga clic <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept> para continuar.`
	trans, err := q.CreateTranslation(ctx, db.CreateTranslationParams{
		SegmentID: seg.ID, TargetLanguage: "es", Status: "needs_review",
	})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	_, err = pool.Exec(ctx,
		"UPDATE translations SET active_text = $1, active_rich_text = $2 WHERE id = $3",
		"Haga clic aquí para continuar.", targetRich, trans.ID)
	if err != nil {
		t.Fatalf("set active_rich_text: %v", err)
	}

	// Step 1: QA with all tags present — should find 0 issues
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var qaResp struct {
		Data struct {
			Issues int `json:"issues"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 0 {
		t.Fatalf("expected 0 issues with all tags present, got %d", qaResp.Data.Issues)
	}
	t.Log("Step 1: QA with all tags present — 0 issues ✓")

	// Step 2: Save draft removing the closing tag (simulate user editing in TipTap).
	// Draft saves human_text (plain) and human_rich_text (XLIFF with tag removed).
	draftRich := `Haga clic <bpt id="1"><b></bpt>aquí para continuar.` // ept removed
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar.",
			"human_rich_text": draftRich,
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 3: Run QA again — should now detect the missing tag
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues == 0 {
		t.Fatal("BUG: QA should detect missing tag after draft save, but found 0 issues")
	}
	t.Logf("Step 3: QA detected %d issue(s) after draft save with removed tag ✓", qaResp.Data.Issues)

	// Verify the issue is tag_missing
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/qa-report", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var reportResp struct {
		Data struct {
			Results []struct {
				CheckType string `json:"check_type"`
				Message   string `json:"message"`
			} `json:"results"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &reportResp)
	foundTagMissing := false
	for _, r := range reportResp.Data.Results {
		if r.CheckType == "tag_missing" {
			foundTagMissing = true
			t.Logf("Tag missing issue: %s", r.Message)
		}
	}
	if !foundTagMissing {
		t.Fatal("expected tag_missing issue in QA report")
	}

	t.Log("Draft save rich text QA flow completed: setup → QA clean → draft remove tag → QA detects → verify report")

	// Step 4: Revert the edit — save draft with original text (simulating cmd+Z).
	// This tests that QA clears issues when the text is restored.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar.",
			"human_rich_text": targetRich, // original rich text with all tags
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 5: Run QA — should find 0 issues (text restored)
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 0 {
		t.Fatalf("BUG: QA should find 0 issues after restoring original text, got %d", qaResp.Data.Issues)
	}
	t.Log("Step 5: QA found 0 issues after restoring text ✓")

	// Step 6: Multi-edit sequence (punctuation + tag removal).
	// Remove period AND tag, run QA — should find exactly 2 issues.
	draftNoPeriod := `Haga clic <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept> para continuar` // no period
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar", // no period
			"human_rich_text": draftNoPeriod,
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Add punctuation rule
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", profileID), map[string]any{
		"check_type": "punctuation",
		"enabled":    true,
		"severity":   "warning",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 1 {
		t.Fatalf("expected 1 issue (punctuation only, tags present), got %d", qaResp.Data.Issues)
	}
	t.Log("Step 6: Punctuation-only issue detected ✓")

	// Now also remove the closing tag
	draftNoPeriodNoTag := `Haga clic <bpt id="1"><b></bpt>aquí para continuar` // no period, no ept
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar",
			"human_rich_text": draftNoPeriodNoTag,
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 2 {
		t.Fatalf("expected 2 issues (punctuation + tag_missing), got %d", qaResp.Data.Issues)
	}
	t.Log("Step 6b: Both punctuation and tag_missing detected ✓")

	// Step 7: Fix tag (bring it back), re-run QA — should drop to 1 issue (punctuation only)
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar",
			"human_rich_text": draftNoPeriod, // tags back, period still missing
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 1 {
		t.Fatalf("expected 1 issue after fixing tag (punctuation remains), got %d", qaResp.Data.Issues)
	}
	t.Log("Step 7: After fixing tag, only punctuation issue remains ✓")

	// Step 8: Fix period too, re-run QA — should be 0
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, trans.ID),
		map[string]any{
			"human_text":      "Haga clic aquí para continuar.",
			"human_rich_text": targetRich, // fully restored
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &qaResp)
	if qaResp.Data.Issues != 0 {
		t.Fatalf("expected 0 issues after fully restoring text, got %d", qaResp.Data.Issues)
	}
	t.Log("Step 8: Fully restored — 0 issues ✓")

	// Verify QA report also shows 0
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/qa-report", job.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &reportResp)
	totalIssues := len(reportResp.Data.Results)
	if totalIssues != 0 {
		t.Fatalf("QA report should have 0 results after full restore, got %d", totalIssues)
	}
	t.Log("Step 8b: QA report confirmed 0 results ✓")

	t.Log("Draft save rich text QA flow completed: full edit/undo/re-run cycle verified")
}

// TestIntegrationWorkflowFlow tests the multi-step workflow journey:
// register → create template with workflow steps → advance → reject → reassign.
func TestIntegrationWorkflowFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Step 1: Register two users (translator + reviewer)
	translatorID, tenantID := testutil.RegisterUser(t, db.New(pool), "translator")

	// Create reviewer user directly in DB
	reviewer := testutil.SeedUserWithRole(t, q, tenantID, "reviewer@test.com", "pw", "member")
	reviewerID := reviewer.ID
	t.Logf("Step 1: Created translator (ID=%d) and reviewer (ID=%d) in tenant %d", translatorID, reviewerID, tenantID)

	// Step 2: Create template
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name":            "WF Template",
		"mt_on_upload":    false,
		"fuzzy_threshold": 0.5,
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID
	t.Logf("Step 2: Created template ID=%d", templateID)

	// Step 3: Set workflow steps on template
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "member"}),
		},
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var stepsResp struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			StepOrder int32  `json:"step_order"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &stepsResp)
	if len(stepsResp.Data) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(stepsResp.Data))
	}
	translateStepID := stepsResp.Data[0].ID
	reviewStepID := stepsResp.Data[1].ID
	t.Logf("Step 3: Set workflow steps: translate (ID=%d), review (ID=%d)", translateStepID, reviewStepID)

	// Step 4: Verify steps via GET
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d/workflow-steps", templateID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &stepsResp)
	if len(stepsResp.Data) != 2 || stepsResp.Data[0].Name != "translate" || stepsResp.Data[1].Name != "Review" {
		t.Fatalf("step verification failed: %+v", stepsResp.Data)
	}
	t.Log("Step 4: Verified steps via GET")

	// Step 5: Create project with template (applied at creation)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "WF Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"template_id":      templateID,
	}, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID
	t.Logf("Step 5: Created project ID=%d with template", projectID)

	// Step 6: Create file + job + segments directly
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID: projectID, Name: "wf_test.txt", Format: "txt", StoragePath: "test/wf.txt",
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	seg, err := q.CreateSegment(ctx, db.CreateSegmentParams{
		FileID: file.ID, SourceText: "Hello world.", Position: 1,
	})
	if err != nil {
		t.Fatalf("create segment: %v", err)
	}
	job, err := q.CreateJob(ctx, db.CreateJobParams{
		FileID: file.ID, TargetLanguage: "de",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	_, err = q.CreateTranslation(ctx, db.CreateTranslationParams{
		SegmentID: seg.ID, TargetLanguage: "de", Status: "needs_review",
	})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	jobID := job.ID

	// Create step assignments for the job
	translateAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: translateStepID, AssignedTo: &translatorID, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("create translate step assignment: %v", err)
	}
	reviewAssign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: jobID, WorkflowStepID: reviewStepID, AssignedTo: &reviewerID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create review step assignment: %v", err)
	}
	// Set current step
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &translateAssign.ID}); err != nil {
		t.Fatalf("set job current step: %v", err)
	}
	t.Logf("Step 6: Created job ID=%d with step assignments (translate=%d, review=%d)", jobID, translateAssign.ID, reviewAssign.ID)
	_ = reviewAssign

	// Step 7: List job steps
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", jobID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var jobStepsResp struct {
		Data []struct {
			ID           int64  `json:"id"`
			StepName     string `json:"step_name"`
			Status       string `json:"status"`
			AssigneeName string `json:"assignee_name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if len(jobStepsResp.Data) != 2 {
		t.Fatalf("expected 2 step assignments, got %d", len(jobStepsResp.Data))
	}
	if jobStepsResp.Data[0].Status != "in_progress" {
		t.Fatalf("expected translate step in_progress, got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "pending" {
		t.Fatalf("expected review step pending, got %s", jobStepsResp.Data[1].Status)
	}
	t.Log("Step 7: Listed job steps — translate=in_progress, review=pending")

	// Step 8: Advance from translate → review (as translator)
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var advanceResp struct {
		Data struct {
			Status     string `json:"status"`
			NextStepID int64  `json:"next_step_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &advanceResp)
	if advanceResp.Data.Status != "advanced" {
		t.Fatalf("expected status 'advanced', got %s", advanceResp.Data.Status)
	}
	t.Logf("Step 8: Advanced to next step (ID=%d)", advanceResp.Data.NextStepID)

	// Step 9: Verify steps updated — translate=completed, review=in_progress
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", jobID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if jobStepsResp.Data[0].Status != "completed" {
		t.Fatalf("expected translate step completed, got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "in_progress" {
		t.Fatalf("expected review step in_progress, got %s", jobStepsResp.Data[1].Status)
	}
	t.Log("Step 9: Verified — translate=completed, review=in_progress")

	// Step 10: Translator (a linguist, not the assignee) tries to advance the
	// review step → 403. Sent with the linguist role explicitly: the default
	// test JWT is an owner, and owners may complete any step.
	rec = testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, translatorID, tenantID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	t.Log("Step 10: Translator correctly blocked from advancing review step (403)")

	// Step 11: Reviewer rejects back to translate
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/reject", jobID), nil, reviewerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var rejectResp struct {
		Data struct {
			Status         string `json:"status"`
			ReopenedStepID int64  `json:"reopened_step_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &rejectResp)
	if rejectResp.Data.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %s", rejectResp.Data.Status)
	}
	t.Logf("Step 11: Reviewer rejected — reopened step ID=%d", rejectResp.Data.ReopenedStepID)

	// Step 12: Verify — translate=in_progress (reopened), review=rejected
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/workflow/steps", jobID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &jobStepsResp)
	if jobStepsResp.Data[0].Status != "in_progress" {
		t.Fatalf("expected translate step reopened (in_progress), got %s", jobStepsResp.Data[0].Status)
	}
	if jobStepsResp.Data[1].Status != "rejected" {
		t.Fatalf("expected review step rejected, got %s", jobStepsResp.Data[1].Status)
	}
	t.Log("Step 12: Verified — translate=in_progress (reopened), review=rejected")

	// Step 13: Translator re-advances → review step reopened
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, translatorID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 14: Reviewer advances (final step) → workflow complete
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", jobID), nil, reviewerID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &advanceResp)
	if advanceResp.Data.Status != "workflow_completed" {
		t.Fatalf("expected 'workflow_completed', got %s", advanceResp.Data.Status)
	}
	t.Log("Step 14: Workflow completed — all steps done")

	t.Log("Workflow E2E flow completed: register → steps → assign → advance → 403 → reject → reopen → re-advance → complete")
}

// TestIntegrationAnalyticsV2Flow tests the analytics v2 journey:
// register → create project → upload segments → configure weights →
// verify analysis includes weighted words → edit translation → verify
// productivity.
func TestIntegrationAnalyticsV2Flow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	// Step 1: Register
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "analytics")
	t.Logf("Step 1: Registered user %d, tenant %d", userID, tenantID)

	// Step 2: Get default weights (should return all 8 bands with defaults)
	rec := testutil.DoRequest(t, router, "GET", "/api/analytics/weights", nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var weightsResp struct {
		Data []struct {
			Band      string `json:"band"`
			WeightPct int32  `json:"weight_pct"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &weightsResp)
	if len(weightsResp.Data) != 8 {
		t.Fatalf("expected 8 weight bands, got %d", len(weightsResp.Data))
	}
	// Verify first band is tm_exact with default 0%
	if weightsResp.Data[0].Band != "tm_exact" || weightsResp.Data[0].WeightPct != 0 {
		t.Fatalf("expected tm_exact=0, got %s=%d", weightsResp.Data[0].Band, weightsResp.Data[0].WeightPct)
	}
	t.Log("Step 2: Got default weights — 8 bands, tm_exact=0%")

	// Step 3: Update weights (set tm_exact to 10%)
	rec = testutil.DoRequest(t, router, "PUT", "/api/analytics/weights", map[string]any{
		"weights": []map[string]any{
			{"band": "tm_exact", "weight_pct": 10},
			{"band": "new", "weight_pct": 90},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &weightsResp)

	// Find tm_exact in response
	found := false
	for _, w := range weightsResp.Data {
		if w.Band == "tm_exact" {
			if w.WeightPct != 10 {
				t.Fatalf("expected tm_exact=10 after update, got %d", w.WeightPct)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("tm_exact not in update response")
	}
	t.Log("Step 3: Updated weights — tm_exact=10%, new=90%")

	// Step 4: Create project with file + segment for analysis
	project := testutil.SeedProject(t, q, tenantID, "Analytics Project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "test.txt", db.FileFormatJsonI18n)
	testutil.SeedJob(t, q, file.ID, "es")
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world.", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	_, _ = pool.Exec(ctx, `UPDATE translations SET origin = 'mt', active_text = 'Hola mundo test.' WHERE id = $1`, tr.ID)
	t.Log("Step 4: Created project with file + segment + mt translation")

	// Step 5: Get analysis v2 — should include weighted_words
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", project.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var analysisResp struct {
		Data struct {
			Total struct {
				New struct {
					Words         int32 `json:"words"`
					WeightedWords int32 `json:"weighted_words"`
					WeightPct     int32 `json:"weight_pct"`
				} `json:"new"`
			} `json:"total"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &analysisResp)

	// New band should have our custom weight (90%)
	if analysisResp.Data.Total.New.WeightPct != 90 {
		t.Fatalf("expected new weight_pct=90, got %d", analysisResp.Data.Total.New.WeightPct)
	}
	t.Logf("Step 5: Analysis v2 — new band weight_pct=%d, weighted_words=%d",
		analysisResp.Data.Total.New.WeightPct,
		analysisResp.Data.Total.New.WeightedWords)

	// Step 6: Edit a translation with timing data
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":      "Hola mundo.",
			"editing_time_ms": 5000,
		}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	t.Log("Step 6: Saved human edit with 5s editing time")

	// Step 7: Verify editing metrics stored
	row, err := q.GetTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("get translation: %v", err)
	}
	if row.EditingTimeMs != 5000 {
		t.Fatalf("expected editing_time_ms=5000, got %d", row.EditingTimeMs)
	}
	if row.EditDistance <= 0 {
		t.Fatalf("expected edit_distance > 0, got %d", row.EditDistance)
	}
	if row.EditedBy == nil || *row.EditedBy != userID {
		t.Fatalf("expected edited_by=%d, got %v", userID, row.EditedBy)
	}
	t.Logf("Step 7: Verified editing metrics — time=%dms, distance=%d, edited_by=%d",
		row.EditingTimeMs, row.EditDistance, *row.EditedBy)

	// Step 8: Get project productivity
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/productivity", project.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var prodResp struct {
		Data []struct {
			UserName       string  `json:"user_name"`
			SegmentsEdited int32   `json:"segments_edited"`
			WordsEdited    int32   `json:"words_edited"`
			TotalTimeMs    int64   `json:"total_editing_time_ms"`
			WordsPerHour   float64 `json:"words_per_hour"`
			AvgEditDist    float64 `json:"avg_edit_distance"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &prodResp)
	if len(prodResp.Data) != 1 {
		t.Fatalf("expected 1 productivity entry, got %d", len(prodResp.Data))
	}
	if prodResp.Data[0].SegmentsEdited != 1 {
		t.Fatalf("expected 1 segment edited, got %d", prodResp.Data[0].SegmentsEdited)
	}
	t.Logf("Step 8: Productivity — %s: %d segments, %d words, %.0f words/hr",
		prodResp.Data[0].UserName, prodResp.Data[0].SegmentsEdited,
		prodResp.Data[0].WordsEdited, prodResp.Data[0].WordsPerHour)

	t.Log("Analytics v2 flow completed: weights → analysis w/ weighted words → edit w/ timing → productivity")
}

// TestIntegrationOrgSettingsFlow tests:
// register → get org settings (default off) → toggle open_registration → verify →
// create second user → change their role → verify → prevent self-role-change.
func TestIntegrationOrgSettingsFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Step 1: Register admin user
	adminID, tenantID := testutil.RegisterUser(t, db.New(pool), "admin")
	t.Logf("Step 1: Registered admin user %d, tenant %d", adminID, tenantID)

	// Step 2: Get org settings — should default to open_registration=false
	rec := testutil.DoRequest(t, router, "GET", "/api/org/settings", nil, adminID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var settings struct {
		OpenRegistration bool    `json:"open_registration"`
		Domain           *string `json:"domain"`
	}
	testutil.DecodeJSON(t, rec, &settings)
	if settings.OpenRegistration {
		t.Fatal("expected open_registration=false by default")
	}
	t.Log("Step 2: Org settings default — open_registration=false")

	// Step 3: Enable open registration
	rec = testutil.DoRequest(t, router, "PUT", "/api/org/settings", map[string]any{
		"open_registration": true,
	}, adminID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settings)
	if !settings.OpenRegistration {
		t.Fatal("expected open_registration=true after update")
	}
	t.Log("Step 3: Enabled open registration")

	// Step 4: Disable it again
	rec = testutil.DoRequest(t, router, "PUT", "/api/org/settings", map[string]any{
		"open_registration": false,
	}, adminID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settings)
	if settings.OpenRegistration {
		t.Fatal("expected open_registration=false after disabling")
	}
	t.Log("Step 4: Disabled open registration")

	// Step 5: Create a second user (translator) directly in DB
	translator := testutil.SeedUserWithRole(t, q, tenantID, "translator@orgtest.com", "pw", "linguist")
	t.Logf("Step 5: Created translator user %d", translator.ID)

	// Step 6: Change translator's role to admin
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", translator.ID), map[string]string{
		"role": "admin",
	}, adminID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var roleResp struct {
		Role string `json:"role"`
	}
	testutil.DecodeJSON(t, rec, &roleResp)
	if roleResp.Role != "admin" {
		t.Fatalf("expected role=admin, got %s", roleResp.Role)
	}
	t.Log("Step 6: Changed translator → admin")

	// Step 7: Verify in members list
	rec = testutil.DoRequest(t, router, "GET", "/api/members", nil, adminID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var membersResp struct {
		Data []struct {
			ID   int64  `json:"id"`
			Role string `json:"role"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &membersResp)
	found := false
	for _, m := range membersResp.Data {
		if m.ID != translator.ID {
			continue
		}
		found = true
		if m.Role != "admin" {
			t.Fatalf("expected translator role=admin in list, got %s", m.Role)
		}
	}
	if !found {
		t.Fatalf("translator %d missing from members list: %+v", translator.ID, membersResp.Data)
	}
	t.Log("Step 7: Verified role in members list")

	// Step 8: Try to change own role — should fail
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", adminID), map[string]string{
		"role": "linguist",
	}, adminID, tenantID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-role-change, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot change your own role") {
		t.Fatalf("expected the self-change refusal, got %s", rec.Body.String())
	}
	t.Log("Step 8: Self-role-change correctly blocked")

	// Step 9: Try invalid role — should fail
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/members/%d/role", translator.ID), map[string]string{
		"role": "superadmin",
	}, adminID, tenantID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d", rec.Code)
	}
	t.Log("Step 9: Invalid role correctly rejected")

	t.Log("Org settings flow completed: get settings → toggle open_registration → change role → self-change blocked → invalid role blocked")
}

// TestIntegrationProjectAsCore tests create-with-template and save-as-template flows.
func TestIntegrationProjectAsCore(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Step 1: Register
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "core")

	// Step 2: Create TM + MT engine + Term base
	rec := testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{"name": "Core TM", "source_language": "en", "target_languages": []string{"es"}}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmResp)
	tmID := tmResp.Data.ID

	// `mock` rather than `libretranslate`: this flow only needs SOME engine to
	// route to, and a type is buildable only where the deployment supplies it.
	// Mock is available outside production with
	// no environment set up; LibreTranslate would need LIBRETRANSLATE_URL.
	rec = testutil.DoRequest(t, router, "POST", "/api/mt-engines", map[string]any{
		"name": "Test MT", "engine_type": "mock",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var mtResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &mtResp)
	mtID := mtResp.Data.ID

	rec = testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]any{
		"name": "Core Terms", "description": "test", "source_language": "en", "target_language": "es",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tbResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tbResp)
	tbID := tbResp.Data.ID
	t.Log("Step 2: Created TM, MT engine, Term base")

	// Step 3: Create template with settings + assignments
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Core Template", "mt_on_upload": true, "fuzzy_threshold": 0.85,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	tmplID := tmplResp.Data.ID

	// Add template TM assignment
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/tms", tmplID), map[string]any{
		"assignments": []map[string]any{
			{"tm_id": tmID, "source_language": "en", "target_language": "es", "mode": "read_write", "priority": 0},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Point the template at an MT router defaulting to the engine
	rec = testutil.DoRequest(t, router, "POST", "/api/mt-routers", map[string]any{
		"name": "Core MT router", "default": map[string]any{"mt_engine_id": mtID},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var mtRouterResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &mtRouterResp)
	mtRouterID := mtRouterResp.Data.ID
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", tmplID), map[string]any{
		"mt_router_id": mtRouterID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Add template Term assignment
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", tmplID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": tbID, "source_language": "en", "target_language": "es", "mode": "read", "priority": 0},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Add template workflow steps
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/workflow-steps", tmplID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "member"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "reviewer"}),
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	t.Log("Step 3: Created template with all assignments")

	// Step 4: Create project WITHOUT a template — gets defaults, no assignments
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "Core Plain Project", "source_language": "en", "target_languages": []string{"es"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var plainResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &plainResp)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", plainResp.Data.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var settingsResp struct {
		Data struct {
			Project struct {
				FuzzyThreshold float64 `json:"fuzzy_threshold"`
				MtOnUpload     bool    `json:"mt_on_upload"`
				TemplateID     *int64  `json:"template_id"`
				MTRouterID     *int64  `json:"mt_router_id"`
				MTRouterName   *string `json:"mt_router_name"`
			} `json:"project"`
			TmAssignments   []any `json:"tm_assignments"`
			TermAssignments []any `json:"term_assignments"`
			WorkflowSteps   []any `json:"workflow_steps"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &settingsResp)
	if settingsResp.Data.Project.FuzzyThreshold != 0.75 {
		t.Fatalf("expected default fuzzy_threshold 0.75, got %f", settingsResp.Data.Project.FuzzyThreshold)
	}
	if len(settingsResp.Data.TmAssignments) != 0 {
		t.Fatalf("expected no TM assignments, got %d", len(settingsResp.Data.TmAssignments))
	}
	if settingsResp.Data.Project.MTRouterID != nil {
		t.Fatalf("expected no MT router on template-less project, got %d", *settingsResp.Data.Project.MTRouterID)
	}
	if settingsResp.Data.Project.TemplateID != nil {
		t.Fatal("expected nil template_id on template-less project")
	}
	t.Log("Step 4: Created project with default settings, no assignments")

	// Step 5: Create project WITH the template — settings + assignments are
	// copied at creation (templates are create-time-only; there is no
	// post-create apply endpoint)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "Core Project", "source_language": "en", "target_languages": []string{"es"},
		"template_id": tmplID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projID := projResp.Data.ID

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settingsResp)
	if settingsResp.Data.Project.FuzzyThreshold < 0.84 || settingsResp.Data.Project.FuzzyThreshold > 0.86 {
		t.Fatalf("expected fuzzy_threshold ~0.85 after create-with-template, got %f", settingsResp.Data.Project.FuzzyThreshold)
	}
	if !settingsResp.Data.Project.MtOnUpload {
		t.Fatal("expected mt_on_upload=true after create-with-template")
	}
	if settingsResp.Data.Project.TemplateID == nil || *settingsResp.Data.Project.TemplateID != tmplID {
		t.Fatalf("expected template_id %d stamped on project, got %v", tmplID, settingsResp.Data.Project.TemplateID)
	}
	if len(settingsResp.Data.TmAssignments) != 1 {
		t.Fatalf("expected 1 TM assignment after create-with-template, got %d", len(settingsResp.Data.TmAssignments))
	}
	if settingsResp.Data.Project.MTRouterID == nil || *settingsResp.Data.Project.MTRouterID != mtRouterID {
		t.Fatalf("expected MT router %d after create-with-template, got %v", mtRouterID, settingsResp.Data.Project.MTRouterID)
	}
	if settingsResp.Data.Project.MTRouterName == nil || *settingsResp.Data.Project.MTRouterName != "Core MT router" {
		t.Fatalf("expected mt_router_name resolved after create-with-template, got %v", settingsResp.Data.Project.MTRouterName)
	}
	if len(settingsResp.Data.TermAssignments) != 1 {
		t.Fatalf("expected 1 term assignment after create-with-template, got %d", len(settingsResp.Data.TermAssignments))
	}
	if len(settingsResp.Data.WorkflowSteps) != 2 {
		t.Fatalf("expected 2 workflow steps after create-with-template, got %d", len(settingsResp.Data.WorkflowSteps))
	}
	t.Log("Step 5: Created project with template — all settings + assignments copied")

	// Step 6: Customize project settings (different from template)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projID), map[string]any{
		"fuzzy_threshold": 0.6,
		"mt_on_upload":    false,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settingsResp)
	if settingsResp.Data.Project.FuzzyThreshold < 0.59 || settingsResp.Data.Project.FuzzyThreshold > 0.61 {
		t.Fatalf("expected fuzzy_threshold ~0.6, got %f", settingsResp.Data.Project.FuzzyThreshold)
	}
	if settingsResp.Data.Project.MtOnUpload {
		t.Fatal("expected mt_on_upload=false after update")
	}
	t.Log("Step 6: Customized project settings independently from template")

	// Step 7: Save a form with the customized settings as a new template (the
	// New project page sends the same payload POST /api/projects takes).
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name":             "From Project",
		"settings":         map[string]any{"fuzzy_threshold": 0.6, "mt_on_upload": false, "mt_router_id": mtRouterID},
		"tm_assignments":   []map[string]any{{"tm_id": tmID, "mode": "read_write", "priority": 0}},
		"term_assignments": []map[string]any{{"term_base_id": tbID, "source_language": "en", "target_language": "es", "mode": "read", "priority": 0}},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var newTmplResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &newTmplResp)
	newTmplID := newTmplResp.Data.ID

	// Verify the new template has the project's customized settings
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", newTmplID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var tmplDetail struct {
		Data struct {
			Template struct {
				FuzzyThreshold float64 `json:"fuzzy_threshold"`
				MtOnUpload     bool    `json:"mt_on_upload"`
				MTRouterID     *int64  `json:"mt_router_id"`
			} `json:"template"`
			TmAssignments   []any `json:"tm_assignments"`
			TermAssignments []any `json:"term_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplDetail)
	if tmplDetail.Data.Template.FuzzyThreshold < 0.59 || tmplDetail.Data.Template.FuzzyThreshold > 0.61 {
		t.Fatalf("expected new template fuzzy_threshold ~0.6, got %f", tmplDetail.Data.Template.FuzzyThreshold)
	}
	if tmplDetail.Data.Template.MtOnUpload {
		t.Fatal("expected new template mt_on_upload=false")
	}
	if len(tmplDetail.Data.TmAssignments) != 1 {
		t.Fatalf("expected 1 TM assignment in new template, got %d", len(tmplDetail.Data.TmAssignments))
	}
	if tmplDetail.Data.Template.MTRouterID == nil || *tmplDetail.Data.Template.MTRouterID != mtRouterID {
		t.Fatalf("expected MT router %d in new template, got %v", mtRouterID, tmplDetail.Data.Template.MTRouterID)
	}
	if len(tmplDetail.Data.TermAssignments) != 1 {
		t.Fatalf("expected 1 term assignment in new template, got %d", len(tmplDetail.Data.TermAssignments))
	}
	t.Log("Step 7: Saved the form as a new template — settings + assignments preserved")

	// Create a second TM — assignments are per-TM, so two rows require
	// two DISTINCT TMs (the same tm_id would dedupe to one).
	rec = testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{
		"name": "Core TM 2", "source_language": "en", "target_languages": []string{"es"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tm2Resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tm2Resp)

	// Step 8: Verify project-level assignment CRUD (set TM assignments)
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/tms", projID), map[string]any{
		"assignments": []map[string]any{
			{"tm_id": tmID, "mode": "read", "priority": 1},
			{"tm_id": tm2Resp.Data.ID, "mode": "read_write", "priority": 0},
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settingsResp)
	if len(settingsResp.Data.TmAssignments) != 2 {
		t.Fatalf("expected 2 TM assignments after set, got %d", len(settingsResp.Data.TmAssignments))
	}
	t.Log("Step 8: Project TM assignment CRUD works")

	// Step 9: Set project workflow steps
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/workflow-steps", projID), map[string]any{
		"steps": []map[string]any{
			testutil.LibraryStep(t, q, tenantID, "translate", map[string]any{"role_required": "translator"}),
			testutil.LibraryStep(t, q, tenantID, "review", map[string]any{"role_required": "reviewer"}),
			testutil.LibraryStep(t, q, tenantID, "proofread", map[string]any{"role_required": "reviewer"}),
		},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", projID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &settingsResp)
	if len(settingsResp.Data.WorkflowSteps) != 3 {
		t.Fatalf("expected 3 workflow steps, got %d", len(settingsResp.Data.WorkflowSteps))
	}
	t.Log("Step 9: Project workflow steps CRUD works")

	t.Log("Project-as-core flow completed: create-with-template → customize → save-as-template → assignment CRUD → workflow steps")
}
