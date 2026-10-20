package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type conceptOut struct {
	ConceptID int64 `json:"concept_id"`
	Terms     []struct {
		TermID   int64  `json:"term_id"`
		Language string `json:"language"`
		Text     string `json:"text"`
		Status   string `json:"status"`
	} `json:"terms"`
}

func TestIntegrationMCPTermBaseReadRenameExport(t *testing.T) {
	rf := newResourcesFixture(t)
	s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	base := testutil.SeedTermBase(t, rf.q, rf.tenantA.ID, "Product terms", "en", "")
	var c struct {
		ConceptID int64 `json:"concept_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_concept", map[string]any{
		"term_base_id": base.ID, "domain": "software",
		"terms": []map[string]any{
			{"language": "en", "text": "e-mail", "status": "approved"},
			{"language": "de", "text": "E-Mail", "status": "approved"},
		},
	}, &c); isErr {
		t.Fatalf("create_concept: %s", text)
	}
	callTool(t, s, "openloc_create_concept", map[string]any{
		"term_base_id": base.ID,
		"terms":        []map[string]any{{"language": "en", "text": "invoice", "status": "approved"}},
	}, nil)

	var bases struct {
		TermBases []struct {
			TermBaseID   int64    `json:"term_base_id"`
			Languages    []string `json:"languages"`
			ConceptCount int64    `json:"concept_count"`
		} `json:"term_bases"`
	}
	if isErr, text := callTool(t, s, "openloc_list_term_bases", map[string]any{}, &bases); isErr {
		t.Fatalf("list_term_bases: %s", text)
	}
	if len(bases.TermBases) != 1 || bases.TermBases[0].ConceptCount != 2 || len(bases.TermBases[0].Languages) != 2 {
		t.Fatalf("list_term_bases = %+v", bases.TermBases)
	}

	if _, err := rf.q.CreateSettingsTermAssignment(context.Background(), db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: rf.projectA.SettingsProfileID, TermBaseID: base.ID, SourceLanguage: "en", TargetLanguage: "de", Mode: "read",
	}); err != nil {
		t.Fatalf("assign base: %v", err)
	}
	var detail struct {
		Projects  int32 `json:"projects"`
		Templates int32 `json:"templates"`
	}
	callTool(t, s, "openloc_get_term_base", map[string]any{"term_base_id": base.ID}, &detail)
	if detail.Projects != 1 || detail.Templates != 0 {
		t.Errorf("get_term_base usage = %+v, want 1 project", detail)
	}

	var page struct {
		Concepts []conceptOut `json:"concepts"`
		Total    int64        `json:"total"`
	}
	callTool(t, s, "openloc_list_concepts", map[string]any{"term_base_id": base.ID, "query": "mail"}, &page)
	if page.Total != 1 || len(page.Concepts) != 1 || page.Concepts[0].ConceptID != c.ConceptID || len(page.Concepts[0].Terms) != 2 {
		t.Fatalf("list_concepts search = %+v", page)
	}
	callTool(t, s, "openloc_list_concepts", map[string]any{"term_base_id": base.ID, "languages": []string{"de"}}, &page)
	if page.Total != 1 || len(page.Concepts[0].Terms) != 1 || page.Concepts[0].Terms[0].Language != "de" {
		t.Errorf("language filter = %+v, want the one concept narrowed to its de term", page)
	}
	var got conceptOut
	if isErr, text := callTool(t, s, "openloc_get_concept", map[string]any{"term_base_id": base.ID, "concept_id": c.ConceptID}, &got); isErr || len(got.Terms) != 2 {
		t.Fatalf("get_concept: %v %s %+v", isErr, text, got)
	}

	if isErr, text := callTool(t, s, "openloc_rename_term_base", map[string]any{"term_base_id": base.ID, "name": "Product glossary"}, nil); isErr {
		t.Fatalf("rename_term_base: %s", text)
	}
	after, _ := rf.q.GetTermBase(context.Background(), db.GetTermBaseParams{ID: base.ID, TenantID: rf.tenantA.ID})
	if after.Name != "Product glossary" || after.Description != base.Description {
		t.Errorf("after rename = %+v", after)
	}

	var exp struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
	}
	callTool(t, s, "openloc_export_terms", map[string]any{"term_base_id": base.ID}, &exp)
	if exp.DownloadPath != "/api/terminology/"+itoa(base.ID)+"/export-tbx" || exp.Filename != "Product glossary.tbx" {
		t.Errorf("TBX export = %+v", exp)
	}
	callTool(t, s, "openloc_export_terms", map[string]any{"term_base_id": base.ID, "format": "csv", "target_languages": []string{"DE", "fr"}}, &exp)
	if exp.DownloadPath != "/api/terminology/"+itoa(base.ID)+"/export?target=de&target=fr" || exp.Filename != "Product glossary.csv" {
		t.Errorf("CSV export = %+v", exp)
	}
}

const importCSV = "source_term,target_term,status,domain,notes\nsave,speichern,approved,ui,\nopen,öffnen,forbidden,ui,do not use\n,missing source,approved,,\n"

const importTBX = `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
  <tbxHeader><fileDesc><sourceDesc><p>test</p></sourceDesc></fileDesc></tbxHeader>
  <text><body>
    <conceptEntry id="c1">
      <langSec xml:lang="en"><termSec><term>printer</term></termSec></langSec>
      <langSec xml:lang="fr"><termSec><term>imprimante</term></termSec></langSec>
      <langSec xml:lang="de"><termSec><term>Drucker</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`

func TestIntegrationMCPImportTerms(t *testing.T) {
	rf := newResourcesFixture(t)
	s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	base := testutil.SeedTermBase(t, rf.q, rf.tenantA.ID, "Imports", "en", "")

	type importOut struct {
		Format   string `json:"format"`
		Imported int    `json:"imported"`
		Terms    int    `json:"terms"`
		Skipped  int    `json:"skipped"`
	}
	var res importOut
	csvID := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "ui.csv", importCSV)
	if isErr, text := callTool(t, s, "openloc_import_terms", map[string]any{
		"term_base_id": base.ID, "upload_id": csvID, "target_language": "de",
	}, &res); isErr {
		t.Fatalf("CSV import: %s", text)
	}
	if res.Format != "csv" || res.Imported != 2 || res.Skipped != 1 {
		t.Errorf("CSV import = %+v, want 2 imported and the source-less row skipped", res)
	}
	var forbidden string
	if err := rf.pool.QueryRow(context.Background(), `
		SELECT t.status FROM term t JOIN term_concept c ON c.id = t.concept_id
		WHERE c.term_base_id = $1 AND t.text = 'öffnen' AND t.language = 'de'`, base.ID).Scan(&forbidden); err != nil || forbidden != "forbidden" {
		t.Errorf("CSV status column not honored: %q, %v", forbidden, err)
	}

	tbxID := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "printers.tbx", importTBX)
	if isErr, text := callTool(t, s, "openloc_import_terms", map[string]any{"term_base_id": base.ID, "upload_id": tbxID}, &res); isErr {
		t.Fatalf("TBX import: %s", text)
	}
	if res.Format != "tbx" || res.Imported != 1 || res.Terms != 3 {
		t.Errorf("TBX import = %+v, want 1 concept with 3 terms", res)
	}
	after, _ := rf.q.GetTermBase(context.Background(), db.GetTermBaseParams{ID: base.ID, TenantID: rf.tenantA.ID})
	if strings.Join(after.TargetLanguages, ",") != "de,fr" {
		t.Errorf("target languages not re-derived after import: %v", after.TargetLanguages)
	}

	// Caller mistakes carry the service's message.
	empty := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "empty.tbx", `<?xml version="1.0"?><tbx style="dca" type="TBX-Basic" xmlns="urn:iso:std:iso:30042:ed-2"><text><body></body></text></tbx>`)
	if isErr, text := callTool(t, s, "openloc_import_terms", map[string]any{"term_base_id": base.ID, "upload_id": empty}, nil); !isErr || !strings.Contains(text, "No terms found") {
		t.Errorf("empty TBX should be refused, got isErr=%v %q", isErr, text)
	}
	odd := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "terms.txt", "a,b\n")
	if isErr, text := callTool(t, s, "openloc_import_terms", map[string]any{"term_base_id": base.ID, "upload_id": odd}, nil); !isErr || !strings.Contains(text, "set format") {
		t.Errorf("unknown extension should ask for a format, got isErr=%v %q", isErr, text)
	}
}

func TestIntegrationMCPTermDeletesAreGuardedSoftDeletes(t *testing.T) {
	rf := newResourcesFixture(t)
	pm := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	owner := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	base := testutil.SeedTermBase(t, rf.q, rf.tenantA.ID, "Brand", "en", "")
	var c1, c2 struct {
		ConceptID int64 `json:"concept_id"`
	}
	callTool(t, pm, "openloc_create_concept", map[string]any{"term_base_id": base.ID, "terms": []map[string]any{
		{"language": "en", "text": "cloud", "status": "approved"},
		{"language": "de", "text": "Wolke", "status": "forbidden"},
	}}, &c1)
	callTool(t, pm, "openloc_create_concept", map[string]any{"term_base_id": base.ID, "terms": []map[string]any{
		{"language": "en", "text": "server", "status": "approved"},
	}}, &c2)
	var concept conceptOut
	callTool(t, pm, "openloc_get_concept", map[string]any{"term_base_id": base.ID, "concept_id": c1.ConceptID}, &concept)
	var wolke int64
	for _, term := range concept.Terms {
		if term.Text == "Wolke" {
			wolke = term.TermID
		}
	}

	// delete_term: the preview says QA stops flagging a forbidden term.
	termArgs := map[string]any{"term_base_id": base.ID, "concept_id": c1.ConceptID, "term_id": wolke}
	var g guardedOut
	callTool(t, pm, "openloc_delete_term", termArgs, &g)
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "Wolke") || len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], "forbidden") {
		t.Fatalf("delete_term preview = %+v", g.Preview)
	}
	if isErr, text := confirmedCall(t, pm, "openloc_delete_term", termArgs, nil); isErr {
		t.Fatalf("delete_term: %s", text)
	}
	assertSoftDeleted(t, rf, "term", wolke)
	after, _ := rf.q.GetTermBase(context.Background(), db.GetTermBaseParams{ID: base.ID, TenantID: rf.tenantA.ID})
	if len(after.TargetLanguages) != 0 {
		t.Errorf("target languages still list %v after the only de term went", after.TargetLanguages)
	}

	// delete_concept: the preview lists its terms.
	conceptArgs := map[string]any{"term_base_id": base.ID, "concept_id": c2.ConceptID}
	callTool(t, pm, "openloc_delete_concept", conceptArgs, &g)
	if g.Preview == nil || len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], `en "server"`) {
		t.Fatalf("delete_concept preview = %+v", g.Preview)
	}
	if isErr, text := confirmedCall(t, pm, "openloc_delete_concept", conceptArgs, nil); isErr {
		t.Fatalf("delete_concept: %s", text)
	}
	assertSoftDeleted(t, rf, "term_concept", c2.ConceptID)

	// delete_term_base: owner/admin only; the preview names where it is assigned.
	if _, err := rf.q.CreateSettingsTermAssignment(context.Background(), db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: rf.projectA.SettingsProfileID, TermBaseID: base.ID, SourceLanguage: "en", TargetLanguage: "de", Mode: "read",
	}); err != nil {
		t.Fatalf("assign base: %v", err)
	}
	baseArgs := map[string]any{"term_base_id": base.ID}
	if isErr, text := callTool(t, pm, "openloc_delete_term_base", baseArgs, nil); !isErr || !strings.Contains(text, "owner or admin") {
		t.Errorf("PM delete_term_base should be refused, got isErr=%v %q", isErr, text)
	}
	callTool(t, owner, "openloc_delete_term_base", baseArgs, &g)
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "1 concept(s)") || len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], "1 project(s)") {
		t.Fatalf("delete_term_base preview = %+v", g.Preview)
	}
	if isErr, text := confirmedCall(t, owner, "openloc_delete_term_base", baseArgs, nil); isErr {
		t.Fatalf("delete_term_base: %s", text)
	}
	assertSoftDeleted(t, rf, "term_bases", base.ID)
	assertSoftDeleted(t, rf, "term_concept", c1.ConceptID)
	if isErr, text := callTool(t, owner, "openloc_get_term_base", baseArgs, nil); !isErr {
		t.Error("get_term_base still finds a deleted base")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

// assertSoftDeleted checks the row is still there with deleted_at set.
func assertSoftDeleted(t *testing.T, rf *resourcesFixture, table string, id int64) {
	t.Helper()
	var deletedAt *time.Time
	if err := rf.pool.QueryRow(context.Background(), `SELECT deleted_at FROM `+table+` WHERE id = $1`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("%s %d is gone — the delete must be soft: %v", table, id, err)
	}
	if deletedAt == nil {
		t.Errorf("%s %d: deleted_at is not set", table, id)
	}
}

func TestIntegrationMCPTermToolsCrossTenant(t *testing.T) {
	rf := newResourcesFixture(t)
	a := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	intruder := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantB.ID, rf.ownerB.ID))
	base := testutil.SeedTermBase(t, rf.q, rf.tenantA.ID, "Tenant A terms", "en", "")
	var c struct {
		ConceptID int64 `json:"concept_id"`
	}
	callTool(t, a, "openloc_create_concept", map[string]any{"term_base_id": base.ID, "terms": []map[string]any{
		{"language": "en", "text": "secret", "status": "approved"},
	}}, &c)
	var concept conceptOut
	callTool(t, a, "openloc_get_concept", map[string]any{"term_base_id": base.ID, "concept_id": c.ConceptID}, &concept)
	upload := rf.stageUpload(t, rf.tenantB.ID, rf.ownerB.ID, "b.csv", importCSV)
	// Tenant B's own base and concept, to try reaching tenant A's concept and
	// term through them.
	ownBase := testutil.SeedTermBase(t, rf.q, rf.tenantB.ID, "Tenant B terms", "en", "")
	ownConcept, err := rf.q.CreateTermConcept(context.Background(), db.CreateTermConceptParams{TermBaseID: ownBase.ID})
	if err != nil {
		t.Fatalf("seed tenant B concept: %v", err)
	}

	// The concept writes, keyed by the base they name: tenant A's base tests
	// the base's tenant check, tenant B's own base the concept-in-base check.
	newConcept := map[string]any{"term_base_id": base.ID, "terms": []map[string]any{
		{"language": "de", "text": "Pwned", "status": "approved"},
	}}
	addTerm := func(baseID int64) map[string]any {
		return map[string]any{"term_base_id": baseID, "concept_id": c.ConceptID, "language": "de", "text": "Pwned", "status": "approved"}
	}
	updateConcept := func(baseID int64) map[string]any {
		return map[string]any{"term_base_id": baseID, "concept_id": c.ConceptID, "definition": "Pwned"}
	}
	updateTerm := func(baseID, conceptID int64) map[string]any {
		return map[string]any{"term_base_id": baseID, "concept_id": conceptID, "term_id": concept.Terms[0].TermID, "text": "Pwned"}
	}
	lookup := map[string]any{"term_base_id": base.ID, "text": "secret"}

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_get_term_base", map[string]any{"term_base_id": base.ID}},
		{"openloc_list_concepts", map[string]any{"term_base_id": base.ID}},
		{"openloc_get_concept", map[string]any{"term_base_id": base.ID, "concept_id": c.ConceptID}},
		{"openloc_lookup_concept", lookup},
		{"openloc_create_concept", newConcept},
		{"openloc_add_term_to_concept", addTerm(base.ID)},
		{"openloc_add_term_to_concept", addTerm(ownBase.ID)},
		{"openloc_update_concept", updateConcept(base.ID)},
		{"openloc_update_concept", updateConcept(ownBase.ID)},
		{"openloc_update_term", updateTerm(base.ID, c.ConceptID)},
		{"openloc_update_term", updateTerm(ownBase.ID, c.ConceptID)},
		{"openloc_update_term", updateTerm(ownBase.ID, ownConcept.ID)},
		{"openloc_rename_term_base", map[string]any{"term_base_id": base.ID, "name": "Pwned"}},
		{"openloc_import_terms", map[string]any{"term_base_id": base.ID, "upload_id": upload, "target_language": "de"}},
		{"openloc_export_terms", map[string]any{"term_base_id": base.ID}},
		{"openloc_delete_term_base", map[string]any{"term_base_id": base.ID}},
		{"openloc_delete_concept", map[string]any{"term_base_id": ownBase.ID, "concept_id": c.ConceptID}},
		{"openloc_delete_term", map[string]any{"term_base_id": ownBase.ID, "concept_id": c.ConceptID, "term_id": concept.Terms[0].TermID}},
	}
	// A fresh session per call: the calls outnumber one token's burst of 20.
	for _, call := range calls {
		s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantB.ID, rf.ownerB.ID))
		isErr, text := callTool(t, s, call.tool, call.args, nil)
		if !isErr {
			t.Errorf("%s %v on tenant A's term base succeeded", call.tool, call.args)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
	ctx := context.Background()
	got, err := rf.q.GetTermBase(ctx, db.GetTermBaseParams{ID: base.ID, TenantID: rf.tenantA.ID})
	if err != nil || got.Name != "Tenant A terms" {
		t.Errorf("tenant A's base changed: %+v, %v", got, err)
	}
	if n, err := rf.q.CountTermBaseEntries(ctx, base.ID); err != nil || n != 1 {
		t.Errorf("tenant A's base holds %d concepts (err %v), want its 1", n, err)
	}
	if got, err := rf.q.GetConcept(ctx, db.GetConceptParams{ID: c.ConceptID, TermBaseID: base.ID}); err != nil || got.Definition != "" {
		t.Errorf("tenant A's concept changed: %+v, %v", got, err)
	}
	if terms, err := rf.q.ListTermsForConcepts(ctx, []int64{c.ConceptID}); err != nil || len(terms) != 1 || terms[0].Text != "secret" {
		t.Errorf("tenant A's concept terms = %+v (err %v), want its 1 term %q", terms, err, "secret")
	}

	// The same calls succeed for tenant A, so each refusal above is the
	// tenant check and not a malformed call.
	var found struct {
		Count int `json:"count"`
	}
	if isErr, text := callTool(t, a, "openloc_lookup_concept", lookup, &found); isErr || found.Count != 1 {
		t.Errorf("lookup_concept as tenant A: isErr=%v %q %+v", isErr, text, found)
	}
	for _, own := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_create_concept", newConcept},
		{"openloc_add_term_to_concept", addTerm(base.ID)},
		{"openloc_update_concept", updateConcept(base.ID)},
		{"openloc_update_term", updateTerm(base.ID, c.ConceptID)},
	} {
		if isErr, text := callTool(t, a, own.tool, own.args, nil); isErr {
			t.Errorf("%s as tenant A: %s", own.tool, text)
		}
	}
	var list struct {
		Count int `json:"count"`
	}
	callTool(t, intruder, "openloc_list_term_bases", map[string]any{}, &list)
	if list.Count != 1 {
		t.Errorf("tenant B lists %d term bases, want only its own", list.Count)
	}
}
