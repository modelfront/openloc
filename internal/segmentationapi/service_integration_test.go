package segmentationapi_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/httputil"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

var simpleSet = segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `[.!?]`, After: `\s`, Break: true}}}

func create(t *testing.T, svc *segmentationapi.Service, tenantID int64, name, lang string) segmentationapi.Profile {
	t.Helper()
	rs := simpleSet
	set, err := svc.Create(context.Background(), tenantID, 0, segmentationapi.Input{Name: name, SourceLanguages: []string{lang}, Definition: &rs})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return set
}

// builtinID returns the id of the built-in with key.
func builtinID(t *testing.T, svc *segmentationapi.Service, tenantID int64, key string) int64 {
	t.Helper()
	sets, err := svc.List(context.Background(), tenantID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sets {
		if s.BuiltinKey == key {
			return s.ID
		}
	}
	t.Fatalf("no built-in %s", key)
	return 0
}

// The built-in rows the migration inserts are the embedded catalog, file for
// file: the parser splits files without a snapshot by the catalog, the API
// lists the rows.
func TestIntegrationBuiltinRowsMatchTheCatalog(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegBuiltins")
	sets, err := svc.List(context.Background(), tenant.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]segmentationapi.Profile{}
	for _, s := range sets {
		if s.Builtin {
			rows[s.BuiltinKey] = s
		}
	}
	catalog := segmentation.Builtins()
	if len(rows) != len(catalog) {
		t.Errorf("built-in rows = %d, catalog = %d", len(rows), len(catalog))
	}
	for _, b := range catalog {
		row, ok := rows[b.ID]
		if !ok {
			t.Errorf("no row for %s: add a migration that inserts it", b.ID)
			continue
		}
		wantLangs := []string{}
		if b.SourceLanguage != "" {
			wantLangs = []string{b.SourceLanguage}
		}
		if row.Name != b.Name || !reflect.DeepEqual(row.SourceLanguages, wantLangs) || row.Description != b.Description {
			t.Errorf("%s row = %q/%q/%q, catalog = %q/%q/%q", b.ID, row.Name, row.SourceLanguages, row.Description, b.Name, wantLangs, b.Description)
		}
		if !reflect.DeepEqual(row.Definition, segmentation.Normalize(b.RuleSet)) {
			t.Errorf("%s: the row's rules differ from the catalog file", b.ID)
		}
	}
}

func TestIntegrationProfiles_BuiltinsAreReadOnly(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegReadOnly")
	rs := simpleSet
	now := time.Now()
	en := builtinID(t, svc, tenant.ID, "builtin:en@1")

	checks := map[string]error{}
	_, checks["update"] = svc.Update(ctx, tenant.ID, en, segmentationapi.Input{Name: "x", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &now})
	checks["delete"] = svc.Delete(ctx, tenant.ID, en)
	for name, err := range checks {
		if _, ok := httputil.AsConflictError(err); !ok {
			t.Errorf("%s of a built-in: err = %v, want a conflict", name, err)
		}
	}

	// Copying one is how to change it.
	copied, err := svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "My English", SourceLanguages: []string{"en"}, From: &en})
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := segmentation.BuiltinByID("builtin:en@1")
	if copied.BasedOn == nil || *copied.BasedOn != en || len(copied.Definition.Abbreviations) != len(catalog.RuleSet.Abbreviations) {
		t.Errorf("copy = based on %v with %d abbreviations, want builtin:en@1's %d", copied.BasedOn, len(copied.Definition.Abbreviations), len(catalog.RuleSet.Abbreviations))
	}
}

func TestIntegrationProfiles_UpdateDetectsStaleSavesAndNameClashes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegStale")
	set := create(t, svc, tenant.ID, "Legal", "en")
	create(t, svc, tenant.ID, "Marketing", "en")
	rs := simpleSet

	first, err := svc.Update(ctx, tenant.ID, set.ID, segmentationapi.Input{Name: "Legal 2", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &set.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "Legal 2" {
		t.Errorf("name = %q", first.Name)
	}
	_, err = svc.Update(ctx, tenant.ID, set.ID, segmentationapi.Input{Name: "Legal 3", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &set.UpdatedAt})
	if _, ok := httputil.AsConflictError(err); !ok {
		t.Errorf("save with a stale updated_at: err = %v, want a conflict", err)
	}
	_, err = svc.Update(ctx, tenant.ID, set.ID, segmentationapi.Input{Name: "marketing", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &first.UpdatedAt})
	if _, ok := httputil.AsConflictError(err); !ok {
		t.Errorf("rename onto a taken name (any case): err = %v, want a conflict", err)
	}
	_, err = svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "LEGAL 2", SourceLanguages: []string{"de"}, Definition: &rs})
	if _, ok := httputil.AsConflictError(err); !ok {
		t.Errorf("create with a taken name: err = %v, want a conflict", err)
	}
}

func TestIntegrationProfiles_TenantIsolationAndValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := segmentationapi.NewService(q, pool)
	tenant := testutil.SeedTenant(t, q, "SegIsoA")
	other := testutil.SeedTenant(t, q, "SegIsoB")
	theirs := create(t, svc, other.ID, "Theirs", "en")
	rs := simpleSet
	now := time.Now()

	if _, err := svc.Get(ctx, tenant.ID, theirs.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get another org's profile: %v, want not found", err)
	}
	if _, err := svc.Update(ctx, tenant.ID, theirs.ID, segmentationapi.Input{Name: "x", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &now}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update another org's profile: %v, want not found", err)
	}
	if err := svc.Delete(ctx, tenant.ID, theirs.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("delete another org's profile: %v, want not found", err)
	}
	if _, err := svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "Copy", SourceLanguages: []string{"en"}, From: &theirs.ID}); err == nil {
		t.Error("copied another org's profile")
	}
	list, err := svc.List(ctx, tenant.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == theirs.ID {
			t.Error("list shows another org's profile")
		}
	}

	bad := segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `(?<=a)`, Break: true}}}
	invalid := map[string]segmentationapi.Input{
		"no name":      {Name: " ", SourceLanguages: []string{"en"}, Definition: &rs},
		"bad language": {Name: "x", SourceLanguages: []string{"zzzz-not"}, Definition: &rs},
		"no rules":     {Name: "x", SourceLanguages: []string{"en"}},
		"empty code":   {Name: "x", SourceLanguages: []string{"en", " "}, Definition: &rs},
		"bad pattern":  {Name: "x", SourceLanguages: []string{"en"}, Definition: &bad},
	}
	for name, in := range invalid {
		if _, err := svc.Create(ctx, tenant.ID, 0, in); err == nil {
			t.Errorf("%s: created", name)
		} else if _, ok := httputil.AsClientError(err); !ok {
			t.Errorf("%s: err = %v, want a client error", name, err)
		}
	}
	tooMany := make([]string, segmentationapi.MaxSourceLanguages+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("x%d", i)
	}
	if _, err := svc.Create(ctx, tenant.ID, 0, segmentationapi.Input{Name: "x", SourceLanguages: tooMany, Definition: &rs}); err == nil ||
		!strings.Contains(err.Error(), "at most 50 source languages") {
		t.Errorf("51 languages: err = %v, want the cap", err)
	}
	if err := svc.Delete(ctx, tenant.ID, 999999999); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("delete a missing set: %v", err)
	}
}
