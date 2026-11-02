package segmentationapi_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

// A new project or template starts with the built-in for its language, then
// for its base language, then the any-language built-in; the list for a
// language holds the profiles that suit it and marks that one.
func TestIntegrationProfiles_SuggestAndList(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegSuggest")
	german := builtinID(t, svc, tenant.ID, "builtin:de@1")
	anyLang := builtinID(t, svc, tenant.ID, segmentation.DefaultID)

	for lang, want := range map[string]int64{"de": german, "de-AT": german, "sw": anyLang, "": anyLang} {
		got, err := segmentationapi.Suggest(ctx, q, lang)
		if err != nil || got != want {
			t.Errorf("Suggest(%q) = %d (%v), want %d", lang, got, err, want)
		}
	}

	austrian := create(t, svc, tenant.ID, "Austrian", "de-at")
	french := create(t, svc, tenant.ID, "French", "fr")
	list, err := svc.List(ctx, tenant.ID, "de-AT")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, p := range list {
		seen[p.ID] = true
		if p.Suggested != (p.ID == german) {
			t.Errorf("%s suggested = %v", p.Name, p.Suggested)
		}
	}
	if !seen[austrian.ID] || !seen[german] || !seen[anyLang] || seen[french.ID] {
		t.Errorf("list for de-AT = %v, want the de-at and de profiles and the any-language ones, not French", seen)
	}
}

// Choose keeps what a project asks for when it suits the language, keeps the
// stored profile while it suits, and otherwise falls back to the suggestion.
func TestIntegrationProfiles_Choose(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegChoose")
	other := testutil.SeedTenant(t, q, "SegChooseOther")
	english := builtinID(t, svc, tenant.ID, "builtin:en@1")
	german := builtinID(t, svc, tenant.ID, "builtin:de@1")
	legal := create(t, svc, tenant.ID, "Legal", "en")
	theirs := create(t, svc, other.ID, "Theirs", "en")

	choose := func(requested, current *int64, lang string) (int64, error) {
		t.Helper()
		return segmentationapi.Choose(ctx, q, tenant.ID, requested, current, lang)
	}
	if got, err := choose(&legal.ID, nil, "en-GB"); err != nil || got != legal.ID {
		t.Errorf("requested en profile for en-GB = %d (%v), want it", got, err)
	}
	for name, id := range map[string]int64{"for another language": german, "of another org": theirs.ID, "missing": 999999999} {
		if _, err := choose(&id, nil, "en"); err == nil {
			t.Errorf("requested a profile %s: accepted", name)
		} else if _, ok := httputil.AsClientError(err); !ok {
			t.Errorf("requested a profile %s: err = %v, want a client error", name, err)
		}
	}
	if got, err := choose(nil, &legal.ID, "en"); err != nil || got != legal.ID {
		t.Errorf("stored en profile, language en = %d (%v), want it kept", got, err)
	}
	if got, err := choose(nil, &legal.ID, "de"); err != nil || got != german {
		t.Errorf("stored en profile, language now de = %d (%v), want the German built-in", got, err)
	}
	if got, err := choose(nil, nil, "en"); err != nil || got != english {
		t.Errorf("nothing stored = %d (%v), want the English built-in", got, err)
	}
	if got, err := choose(nil, &legal.ID, ""); err != nil || got != legal.ID {
		t.Errorf("stored profile, no language = %d (%v), want it kept", got, err)
	}
}

// An upload is split with the project's profile, one deleted after the
// project's settings were read included; a project without one is refused.
func TestIntegrationProfiles_Resolve(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegResolve")
	other := testutil.SeedTenant(t, q, "SegResolveOther")
	legal := create(t, svc, tenant.ID, "Legal", "en")

	snap, err := segmentationapi.Resolve(ctx, q, tenant.ID, &legal.ID, "en-GB")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source.Kind != segmentation.SourceKindOrg || snap.Source.ID != strconv.FormatInt(legal.ID, 10) || snap.ProfileID != legal.ID ||
		snap.Language != "en-gb" || len(snap.RuleSet.Rules) != 1 || snap.RuleSet.Rules[0].Before != `[.!?]` {
		t.Errorf("snapshot = %+v, want the Legal profile's rules for en-gb", snap)
	}
	if err := svc.Delete(ctx, tenant.ID, legal.ID); err != nil {
		t.Fatal(err)
	}
	if snap, err := segmentationapi.Resolve(ctx, q, tenant.ID, &legal.ID, "en"); err != nil || snap.ProfileID != legal.ID {
		t.Errorf("deleted profile = %+v (%v), want still its rules", snap.Source, err)
	}
	if _, err := segmentationapi.Resolve(ctx, q, tenant.ID, nil, "de"); !errors.Is(err, segmentationapi.ErrNoProfile) {
		t.Errorf("no profile: err = %v, want ErrNoProfile", err)
	}
	if _, err := segmentationapi.Resolve(ctx, q, other.ID, &legal.ID, "en"); err == nil {
		t.Error("another org resolved the profile")
	}
}

// A template must move to another profile before its profile is deleted; a
// project loses a deleted profile, and the template the refused delete
// left alone keeps its own.
func TestIntegrationProfiles_DeleteWhileInUse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegDeleteUsed")
	used := create(t, svc, tenant.ID, "Used", "en")
	setProfile := func(settingsProfileID int64) {
		t.Helper()
		if _, err := q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: settingsProfileID, SegmentationProfileID: &used.ID}); err != nil {
			t.Fatal(err)
		}
	}
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	setProfile(project.SettingsProfileID)
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	setProfile(tmpl.SettingsProfileID)

	if projects, templates, err := svc.Uses(ctx, tenant.ID, used.ID); err != nil || projects != 1 || templates != 1 {
		t.Errorf("uses = %d projects, %d templates (%v), want 1 and 1", projects, templates, err)
	}
	err := svc.Delete(ctx, tenant.ID, used.ID)
	if _, ok := httputil.AsConflictError(err); !ok {
		t.Fatalf("delete while a template uses it: err = %v, want a conflict", err)
	}
	other := create(t, svc, tenant.ID, "Other", "en")
	if _, err := q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: tmpl.SettingsProfileID, SegmentationProfileID: &other.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, tenant.ID, used.ID); err != nil {
		t.Fatalf("delete used by a project only: %v", err)
	}
	sp, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil || sp.SegmentationProfileID != nil {
		t.Errorf("project's profile after the delete = %v (%v), want none", sp.SegmentationProfileID, err)
	}
	if sp, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID); err != nil || sp.SegmentationProfileID == nil || *sp.SegmentationProfileID != other.ID {
		t.Errorf("template's profile after the delete = %v (%v), want Other", sp.SegmentationProfileID, err)
	}
}

// A profile's language cannot move to one that a template using it does not
// suit; with no such template it can.
func TestIntegrationProfiles_LanguageChangeWhileTemplatesUseIt(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegLangChange")
	used := create(t, svc, tenant.ID, "Used", "en")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "T")
	if _, err := pool.Exec(ctx, `UPDATE templates SET source_language = 'en-gb' WHERE id = $1`, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: tmpl.SettingsProfileID, SegmentationProfileID: &used.ID}); err != nil {
		t.Fatal(err)
	}
	rs := simpleSet
	update := func(lang string) error {
		t.Helper()
		cur, err := svc.Get(ctx, tenant.ID, used.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.Update(ctx, tenant.ID, used.ID, segmentationapi.Input{Name: "Used", SourceLanguages: []string{lang}, Definition: &rs, UpdatedAt: &cur.UpdatedAt})
		return err
	}
	if _, ok := httputil.AsConflictError(update("de")); !ok {
		t.Error("move to German while an en-GB template uses it: want a conflict")
	}
	if err := update("en-gb"); err != nil {
		t.Errorf("move to en-GB, which the template suits: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE templates SET source_language = NULL WHERE id = $1`, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if err := update("de"); err != nil {
		t.Errorf("move to German while only a template without a language uses it: %v", err)
	}
}

// A profile for several languages suits each of them and their regional
// variants, and no other; one for no languages suits every language. The
// list is stored normalized, without repeats.
func TestIntegrationProfiles_SeveralLanguages(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegSeveral")
	rs := simpleSet
	both, err := svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "German and French", SourceLanguages: []string{"de", "FR", "de"}, Definition: &rs})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(both.SourceLanguages, []string{"de", "fr"}) {
		t.Errorf("languages = %q, want de and fr", both.SourceLanguages)
	}
	anyLang, err := svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "Every language", Definition: &rs})
	if err != nil {
		t.Fatal(err)
	}
	if anyLang.SourceLanguages == nil || len(anyLang.SourceLanguages) != 0 {
		t.Errorf("languages of a profile for any language = %#v, want empty", anyLang.SourceLanguages)
	}

	for lang, suits := range map[string]bool{"de": true, "de-AT": true, "fr-CA": true, "it": false} {
		list, err := svc.List(ctx, tenant.ID, lang)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[int64]bool{}
		for _, p := range list {
			listed[p.ID] = true
		}
		if listed[both.ID] != suits || !listed[anyLang.ID] {
			t.Errorf("list for %s: German and French listed = %v (want %v), any language listed = %v", lang, listed[both.ID], suits, listed[anyLang.ID])
		}
		if err := segmentationapi.Check(ctx, q, tenant.ID, both.ID, lang); (err == nil) != suits {
			t.Errorf("Check German and French for %s: %v, want suits = %v", lang, err, suits)
		}
		if err := segmentationapi.Check(ctx, q, tenant.ID, anyLang.ID, lang); err != nil {
			t.Errorf("Check any language for %s: %v", lang, err)
		}
	}
}

// A settings row names only a live profile or a built-in: a write that names
// a profile deleted since the caller checked it stores nothing, so a save
// racing a delete never leaves a project on a deleted profile.
func TestIntegrationProfiles_WriteRefusesADeletedProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegWriteDeleted")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	live, gone := create(t, svc, tenant.ID, "Live", "en"), create(t, svc, tenant.ID, "Gone", "en")
	if err := svc.Delete(ctx, tenant.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	write := func(id int64) error {
		_, err := q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: project.SettingsProfileID, SegmentationProfileID: &id})
		return err
	}
	if err := write(gone.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("write a deleted profile: err = %v, want no row", err)
	}
	for name, id := range map[string]int64{"live": live.ID, "built-in": builtinID(t, svc, tenant.ID, "builtin:de@1")} {
		if err := write(id); err != nil {
			t.Errorf("write a %s profile: %v", name, err)
		}
	}
}
