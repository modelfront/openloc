package publicapi_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// The segment and comment routes, end to end. Every response is checked
// against the spec by the client.
func TestIntegrationV1_SegmentsAndComments(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Segments")
	c := f.client
	base := f.path("/jobs/{jobID}/segments")

	rec := c.do(http.MethodGet, base, nil, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)
	segs := testutil.DecodeData[[]publicapi.Segment](t, rec)
	if len(segs) != 1 || segs[0].ID != f.ids["translationID"] || segs[0].SourceText != "Hello world." {
		t.Fatalf("segments = %+v", segs)
	}
	seg := fmt.Sprintf("%s/%d", base, segs[0].ID)

	rec = c.do(http.MethodPut, seg+"/draft", map[string]any{"text": "Hallo Welt.", "expected_updated_at": segs[0].UpdatedAt}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)
	saved := testutil.DecodeData[publicapi.SegmentState](t, rec)
	if saved.TargetText == nil || *saved.TargetText != "Hallo Welt." {
		t.Fatalf("draft = %+v", saved)
	}

	// The version read before the draft is stale now.
	rec = c.do(http.MethodPut, seg+"/draft", map[string]any{"text": "Older.", "expected_updated_at": segs[0].UpdatedAt}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "conflict" {
		t.Fatalf("stale draft code = %q, want conflict", code)
	}

	rec = c.do(http.MethodPost, seg+"/confirm", map[string]any{"expected_updated_at": saved.UpdatedAt}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.SegmentState](t, rec); got.Status != "approved" || *got.TargetText != "Hallo Welt." {
		t.Fatalf("confirmed = %+v", got)
	}

	rec = c.do(http.MethodGet, base+"?status=approved", nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Segment](t, rec); len(got) != 1 {
		t.Fatalf("approved filter = %+v", got)
	}

	rec = c.do(http.MethodPost, seg+"/comments", map[string]any{"content": "Is this formal enough?"}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusCreated)
	mine := testutil.DecodeData[publicapi.Comment](t, rec)

	rec = c.do(http.MethodGet, seg+"/comments", nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Comment](t, rec); len(got) != 2 {
		t.Fatalf("comments = %+v", got)
	}

	// Someone else's comment cannot be deleted; your own can.
	rec = c.do(http.MethodDelete, fmt.Sprintf("%s/comments/%d", seg, f.ids["commentID"]), nil, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusNotFound)
	rec = c.do(http.MethodDelete, fmt.Sprintf("%s/comments/%d", seg, mine.ID), nil, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusNoContent)
}

// A segment id is only valid under the job it belongs to.
func TestIntegrationV1_SegmentMustBelongToJob(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "SegJob")
	// Same tenant, a job of another project.
	rec := f.client.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Second", "source_language": "en", "target_languages": []string{"fr"},
	}, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.Project](t, rec)
	rec = f.client.upload(fmt.Sprintf("/api/v1/projects/%d/files", p.ID), "a.txt", "One.\n", f.key(t, "owner"))
	wantStatus(t, rec, http.StatusCreated)
	rec = f.client.do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/jobs", p.ID), nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	jobs := testutil.DecodeData[[]publicapi.Job](t, rec)

	path := fmt.Sprintf("/api/v1/jobs/%d/segments/%d/comments", jobs[0].ID, f.ids["translationID"])
	rec = f.client.do(http.MethodGet, path, nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusNotFound)
}

const sampleTMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4"><header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="test" creationtoolversion="1" o-tmf="test"/>
<body><tu><tuv xml:lang="en"><seg>Hello world.</seg></tuv><tuv xml:lang="de"><seg>Hallo Welt.</seg></tuv></tu></body></tmx>`

func TestIntegrationV1_TranslationMemories(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Memories")
	c := f.client

	rec := c.do(http.MethodPost, "/api/v1/tms", map[string]any{"name": "Marketing", "source_language": "en", "target_languages": []string{"de"}}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusCreated)
	tm := testutil.DecodeData[publicapi.TM](t, rec)
	base := fmt.Sprintf("/api/v1/tms/%d", tm.ID)

	rec = c.do(http.MethodPatch, base, map[string]any{"name": "Marketing 2026"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.TM](t, rec); got.Name != "Marketing 2026" {
		t.Fatalf("renamed = %+v", got)
	}

	rec = c.upload(base+"/imports", "memory.tmx", sampleTMX, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusAccepted)
	imp := testutil.DecodeData[publicapi.TMImport](t, rec)
	if imp.ID == 0 || imp.FileName != "memory.tmx" {
		t.Fatalf("import = %+v", imp)
	}
	rec = c.do(http.MethodGet, base+"/imports", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.TMImport](t, rec); len(got) != 1 || got[0].ID != imp.ID {
		t.Fatalf("imports = %+v", got)
	}

	rec = c.do(http.MethodGet, "/api/v1/tms", nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.TM](t, rec); len(got) != 2 {
		t.Fatalf("tms = %+v", got)
	}
	rec = c.do(http.MethodGet, base, nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.TM](t, rec); got.ID != tm.ID || got.Name != "Marketing 2026" {
		t.Fatalf("tm = %+v", got)
	}

	// The import above only queues; the fixture's memory holds one entry.
	fixtureTM := f.path("/tms/{tmID}")
	rec = c.do(http.MethodGet, fixtureTM+"/entries?q=morning", nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.TMEntry](t, rec); len(got) != 1 || got[0].SourceText != "Good morning." || got[0].TargetText != "Guten Morgen." {
		t.Fatalf("entries matching morning = %+v", got)
	}
	rec = c.do(http.MethodGet, fixtureTM+"/entries?q=Hello", nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.TMEntry](t, rec); len(got) != 0 {
		t.Fatalf("entries matching Hello = %+v", got)
	}
	rec = c.do(http.MethodGet, fixtureTM+"/export", nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusOK)
	if !containsAll(rec.Body.String(), "<tmx", "Good morning.", "Guten Morgen.") {
		t.Fatalf("TMX export = %s", rec.Body.String())
	}

	// Project managers may not delete a memory; owners may.
	rec = c.do(http.MethodDelete, base, nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = c.do(http.MethodDelete, base, nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, base, nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationV1_Glossaries(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Glossaries")
	c := f.client

	rec := c.do(http.MethodPost, "/api/v1/glossaries", map[string]any{"name": "Product terms", "source_language": "en"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusCreated)
	g := testutil.DecodeData[publicapi.Glossary](t, rec)
	base := fmt.Sprintf("/api/v1/glossaries/%d", g.ID)
	rec = c.do(http.MethodGet, "/api/v1/glossaries", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	listed := map[int64]bool{}
	for _, item := range testutil.DecodeData[[]publicapi.Glossary](t, rec) {
		listed[item.ID] = true
	}
	if !listed[g.ID] || !listed[f.ids["glossaryID"]] {
		t.Fatalf("glossaries listed = %v, want %d and %d", listed, g.ID, f.ids["glossaryID"])
	}

	rec = c.do(http.MethodPost, base+"/concepts", map[string]any{
		"definition": "The device a user holds",
		"terms": []map[string]any{
			{"language": "en", "text": "handset"},
			{"language": "de", "text": "Mobilteil", "status": "approved"},
			{"language": "de", "text": "Handy", "status": "forbidden"},
		},
	}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusCreated)
	concept := testutil.DecodeData[publicapi.Concept](t, rec)
	if len(concept.Terms) != 3 || concept.Terms[0].ID == 0 {
		t.Fatalf("concept = %+v", concept)
	}

	rec = c.do(http.MethodGet, base, nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Glossary](t, rec); got.ConceptCount != 1 || len(got.TargetLanguages) != 1 {
		t.Fatalf("glossary = %+v", got)
	}

	rec = c.do(http.MethodGet, base+"/concepts?q=handset", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Concept](t, rec); len(got) != 1 {
		t.Fatalf("concepts = %+v", got)
	}

	rec = c.do(http.MethodGet, base+"/export", nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	tbx := rec.Body.String()
	if !containsAll(tbx, "Mobilteil", "Handy") {
		t.Fatalf("TBX export is missing terms: %s", tbx)
	}

	// The export imports back into a second glossary.
	rec = c.do(http.MethodPost, "/api/v1/glossaries", map[string]any{"name": "Copy", "source_language": "en"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusCreated)
	copyID := testutil.DecodeData[publicapi.Glossary](t, rec).ID
	rec = c.upload(fmt.Sprintf("/api/v1/glossaries/%d/imports", copyID), "terms.tbx", tbx, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodGet, fmt.Sprintf("/api/v1/glossaries/%d/concepts", copyID), nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	copied := testutil.DecodeData[[]publicapi.Concept](t, rec)
	if len(copied) != 1 {
		t.Fatalf("imported concepts = %+v", copied)
	}
	gotTerms := map[string]string{}
	for _, term := range copied[0].Terms {
		gotTerms[term.Language+":"+term.Text] = term.Status
	}
	if len(gotTerms) != 3 || gotTerms["de:Handy"] != "forbidden" || gotTerms["de:Mobilteil"] != "approved" {
		t.Fatalf("imported terms = %v", gotTerms)
	}
	if _, ok := gotTerms["en:handset"]; !ok {
		t.Fatalf("imported terms miss the source term: %v", gotTerms)
	}

	rec = c.do(http.MethodPatch, base, map[string]any{"description": "Hardware"}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Glossary](t, rec); got.Name != "Product terms" || got.Description != "Hardware" {
		t.Fatalf("partial update = %+v", got)
	}

	rec = c.do(http.MethodDelete, fmt.Sprintf("%s/concepts/%d", base, concept.ID), nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodDelete, base, nil, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusForbidden)
	rec = c.do(http.MethodDelete, base, nil, f.key(t, "admin"))
	wantStatus(t, rec, http.StatusNoContent)
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// A listed segment carries how the last finished workflow step left it.
func TestIntegrationV1_SegmentsPreviousStep(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "PrevStep")
	c := f.client
	base := f.path("/jobs/{jobID}/segments")
	ctx := context.Background()

	rec := c.do(http.MethodGet, base, nil, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)
	segs := testutil.DecodeData[[]publicapi.Segment](t, rec)
	if len(segs) != 1 || segs[0].PreviousStep != nil {
		t.Fatalf("before any step finished, previous_step must be null: %+v", segs)
	}

	rec = c.do(http.MethodPut, fmt.Sprintf("%s/%d/draft", base, segs[0].ID), map[string]any{"text": "Hallo Welt."}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodPost, fmt.Sprintf("%s/%d/confirm", base, segs[0].ID), map[string]any{}, f.key(t, "linguist"))
	wantStatus(t, rec, http.StatusOK)

	active, err := q.GetActiveStepForJob(ctx, f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := stepround.Close(ctx, q, active.ID, stepround.Completed); err != nil {
		t.Fatal(err)
	}

	rec = c.do(http.MethodGet, base, nil, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	segs = testutil.DecodeData[[]publicapi.Segment](t, rec)
	p := segs[0].PreviousStep
	if p == nil || p.Status != "approved" || !p.Edited || p.EndReason != "completed" || p.StepName != active.StepName {
		t.Fatalf("previous_step = %+v, want %s: approved, edited, completed", p, active.StepName)
	}

	// Another organization's key sees nothing of this job.
	other := newFixture(t, q, router, "PrevStepOther")
	rec = c.do(http.MethodGet, base, nil, other.key(t, "owner"))
	wantStatus(t, rec, http.StatusNotFound)
}
