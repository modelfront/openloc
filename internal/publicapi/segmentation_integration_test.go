package publicapi_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

func TestIntegrationV1_SegmentationProfiles(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Segmentation")
	c := f.client
	pm := func() string { return f.key(t, "project_manager") }
	linguist := func() string { return f.key(t, "linguist") }

	// Every member lists: the built-ins first by id, then the org's profile; a
	// language keeps its own, its base language's and the any-language ones.
	rec := c.do(http.MethodGet, "/api/v1/segmentation-profiles?source_language=en-GB&limit=100", nil, linguist())
	wantStatus(t, rec, http.StatusOK)
	profiles := testutil.DecodeData[[]publicapi.SegmentationProfile](t, rec)
	var english, house *publicapi.SegmentationProfile
	for i, s := range profiles {
		switch {
		case s.BuiltinKey != nil && *s.BuiltinKey == "builtin:en@1":
			english = &profiles[i]
		case s.ID == f.ids["profileID"]:
			house = &profiles[i]
		case len(s.SourceLanguages) > 0 && !slices.Contains(s.SourceLanguages, "en"):
			t.Errorf("an en-GB list holds %s", s.SourceLanguages)
		}
	}
	if english == nil || !english.Builtin || house == nil || house.Builtin {
		t.Fatalf("list = %+v", profiles)
	}
	rec = c.do(http.MethodGet, "/api/v1/segmentation-profiles?limit=2", nil, linguist())
	wantStatus(t, rec, http.StatusOK)
	if page := testutil.DecodeData[[]publicapi.SegmentationProfile](t, rec); len(page) != 2 || !strings.Contains(rec.Body.String(), `"has_more":true`) {
		t.Errorf("first page of 2 = %d items: %s", len(page), rec.Body.String())
	}

	// A profile for several languages is listed for each of them (and a
	// regional variant of each), and for no other.
	rec = c.do(http.MethodPost, "/api/v1/segmentation-profiles", map[string]any{"name": "German and French", "source_languages": []string{"de", "fr"}, "from_id": english.ID}, pm())
	wantStatus(t, rec, http.StatusCreated)
	both := testutil.DecodeData[publicapi.SegmentationProfile](t, rec)
	for lang, listed := range map[string]bool{"de": true, "fr-CA": true, "it": false} {
		rec = c.do(http.MethodGet, "/api/v1/segmentation-profiles?limit=100&source_language="+lang, nil, linguist())
		wantStatus(t, rec, http.StatusOK)
		found := false
		for _, p := range testutil.DecodeData[[]publicapi.SegmentationProfile](t, rec) {
			found = found || p.ID == both.ID
		}
		if found != listed {
			t.Errorf("list for %s holds German and French = %v, want %v", lang, found, listed)
		}
	}

	// Copy the built-in and replace the copy.
	rec = c.do(http.MethodPost, "/api/v1/segmentation-profiles", map[string]any{"name": "Legal", "source_languages": []string{"en"}, "from_id": english.ID}, pm())
	wantStatus(t, rec, http.StatusCreated)
	legal := testutil.DecodeData[publicapi.SegmentationProfile](t, rec)
	if legal.BasedOnID == nil || *legal.BasedOnID != english.ID || len(legal.Definition.Abbreviations) == 0 || legal.Builtin {
		t.Fatalf("copy = %+v", legal)
	}
	path := fmt.Sprintf("/api/v1/segmentation-profiles/%d", legal.ID)
	replace := map[string]any{"name": "Legal v2", "source_languages": []string{"en"}, "updated_at": legal.UpdatedAt,
		"definition": map[string]any{"mode": "paragraph", "rules": []any{}, "abbreviations": []string{"Art"}, "number_abbreviations": []string{}}}
	rec = c.do(http.MethodPut, path, replace, pm())
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.SegmentationProfile](t, rec); got.Name != "Legal v2" || got.Definition.Mode != "paragraph" {
		t.Fatalf("replaced = %+v", got)
	}
	rec = c.do(http.MethodPut, path, replace, pm())
	wantStatus(t, rec, http.StatusConflict)
	rec = c.do(http.MethodGet, path, nil, linguist())
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodGet, path+"/srx", nil, linguist())
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `<languagerule languagerulename="Legal v2">`) {
		t.Errorf("SRX = %s", rec.Body.String())
	}

	// A project names the profile it is created with; one for another
	// language is refused.
	rec = c.do(http.MethodPost, "/api/v1/projects", map[string]any{"name": "Legal project", "source_language": "en-GB",
		"target_languages": []string{"de"}, "segmentation_profile_id": legal.ID, "workflow_id": f.ids["workflowID"]}, pm())
	wantStatus(t, rec, http.StatusCreated)
	created := testutil.DecodeData[publicapi.Project](t, rec)
	project, err := q.GetProject(context.Background(), db.GetProjectParams{ID: created.ID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := q.GetSettingsProfile(context.Background(), project.SettingsProfileID)
	if err != nil || settings.SegmentationProfileID == nil || *settings.SegmentationProfileID != legal.ID {
		t.Errorf("project profile = %v (%v), want %d", settings.SegmentationProfileID, err, legal.ID)
	}
	rec = c.do(http.MethodPost, "/api/v1/projects", map[string]any{"name": "German project", "source_language": "de",
		"target_languages": []string{"fr"}, "segmentation_profile_id": legal.ID, "workflow_id": f.ids["workflowID"]}, pm())
	wantStatus(t, rec, http.StatusBadRequest)

	// Built-ins stay read-only.
	builtin := fmt.Sprintf("/api/v1/segmentation-profiles/%d", english.ID)
	rec = c.do(http.MethodDelete, builtin, nil, pm())
	wantStatus(t, rec, http.StatusConflict)

	rec = c.do(http.MethodDelete, path, nil, pm())
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, path, nil, pm())
	wantStatus(t, rec, http.StatusNotFound)
}
