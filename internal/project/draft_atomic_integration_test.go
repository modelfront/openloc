package project_test

import (
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A draft save is one transaction: a setup refused after the draft row is
// written (another org's TM in tm_assignments, which only the assignment
// write checks) leaves the name, the template stamp, the TM profile link and
// the settings as they were. The same save with the caller's own TM is the
// positive control.
func TestIntegrationDraftSave_RefusedSetupChangesNothing(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftAtomic")
	defer cleanup()
	ctx := testutil.Ctx()
	ownProfile := testutil.SeedTMProfile(t, f.q, f.tenant.ID, "Own profile", 0.8)
	ownTM := testutil.SeedTM(t, f.q, f.tenant.ID, "Own TM")
	tmpl := testutil.SeedTemplate(t, f.q, f.tenant.ID, "Template")
	other := testutil.SeedTenant(t, f.q, "DraftAtomicOther")
	foreignTM := testutil.SeedTM(t, f.q, other.ID, "Foreign TM")

	id := f.createDraft(t, map[string]any{"name": "P", "settings": map[string]any{"tm_profile_id": ownProfile.ID}})
	before := f.project(t, id)
	beforeSettings, err := f.q.GetSettingsProfile(ctx, before.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	save := func(tmID int64) int {
		t.Helper()
		return f.saveDraft(t, id, map[string]any{
			"name": "Renamed", "template_id": tmpl.ID,
			"settings":       map[string]any{"fuzzy_threshold": 0.95},
			"tm_assignments": []map[string]any{{"tm_id": tmID, "mode": "read", "priority": 1}},
		}).Code
	}

	if code := save(foreignTM.ID); code != http.StatusBadRequest {
		t.Fatalf("foreign TM: status = %d, want 400", code)
	}
	p := f.project(t, id)
	if p.Name != "P" {
		t.Errorf("refused save renamed the draft to %q", p.Name)
	}
	if p.TemplateID != nil {
		t.Errorf("refused save stamped template %d", *p.TemplateID)
	}
	s, err := f.q.GetSettingsProfile(ctx, p.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if s.TmProfileID == nil || *s.TmProfileID != ownProfile.ID {
		t.Errorf("refused save changed the TM profile link to %s, want %d", testutil.Show(s.TmProfileID), ownProfile.ID)
	}
	if s.FuzzyThreshold != beforeSettings.FuzzyThreshold {
		t.Errorf("refused save changed fuzzy_threshold %v→%v", beforeSettings.FuzzyThreshold, s.FuzzyThreshold)
	}

	if code := save(ownTM.ID); code != http.StatusOK {
		t.Fatalf("own TM: status = %d, want 200", code)
	}
	p = f.project(t, id)
	if p.Name != "Renamed" || p.TemplateID == nil || *p.TemplateID != tmpl.ID {
		t.Errorf("own-TM save: name %q template %s, want Renamed / %d", p.Name, testutil.Show(p.TemplateID), tmpl.ID)
	}
	if s, _ := f.q.GetSettingsProfile(ctx, p.SettingsProfileID); s.TmProfileID != nil || s.FuzzyThreshold != 0.95 {
		t.Errorf("own-TM save: TM profile link %s, fuzzy_threshold %v; want none / 0.95", testutil.Show(s.TmProfileID), s.FuzzyThreshold)
	}
	if tms, err := f.q.ListSettingsTMAssignments(ctx, p.SettingsProfileID); err != nil || len(tms) != 1 || tms[0].TmID != ownTM.ID {
		t.Errorf("own-TM save: TM rows %v (%v), want the one own TM", tms, err)
	}
}

// A save refused at a later step keeps what earlier steps wrote out too: the
// TM rows are replaced before the term rows are checked, so another org's
// term base must roll the new TM rows back with it.
func TestIntegrationDraftSave_RefusalAfterTMReplaceKeepsOldTMRows(t *testing.T) {
	f, cleanup := newDraftFixture(t, "DraftAtomicLate")
	defer cleanup()
	ctx := testutil.Ctx()
	tmA := testutil.SeedTM(t, f.q, f.tenant.ID, "TM A")
	tmB := testutil.SeedTM(t, f.q, f.tenant.ID, "TM B")
	other := testutil.SeedTenant(t, f.q, "DraftAtomicLateOther")
	foreignBase := testutil.SeedTermBase(t, f.q, other.ID, "Foreign base", "en", "fr")
	tmRows := func(id int64) []int64 {
		t.Helper()
		tms, err := f.q.ListSettingsTMAssignments(ctx, f.project(t, id).SettingsProfileID)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(tms))
		for i, a := range tms {
			ids[i] = a.TmID
		}
		return ids
	}

	id := f.createDraft(t, map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
		"tm_assignments": []map[string]any{{"tm_id": tmA.ID, "mode": "read", "priority": 1}},
	})
	if got := tmRows(id); len(got) != 1 || got[0] != tmA.ID {
		t.Fatalf("setup: TM rows %v, want [%d]", got, tmA.ID)
	}

	rec := f.saveDraft(t, id, map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
		"tm_assignments": []map[string]any{{"tm_id": tmB.ID, "mode": "read", "priority": 1}},
		"term_assignments": []map[string]any{{
			"term_base_id": foreignBase.ID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 1,
		}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign term base: status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if got := tmRows(id); len(got) != 1 || got[0] != tmA.ID {
		t.Errorf("refused save left TM rows %v, want the old [%d]", got, tmA.ID)
	}
}
