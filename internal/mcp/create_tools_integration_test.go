// Creation tools over the real protocol: create_project (args +
// template paths), create_customer, create_term_base, add_term, create_concept,
// add_term_to_concept — plus the durable-audit contract for writes.
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	mcppkg "openloc/internal/mcp"

	"openloc/internal/auth"
	"openloc/internal/dashboard"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationMCPCreateProjectWithArgs(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out struct {
		ProjectID       int64    `json:"project_id"`
		SourceLanguage  string   `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
		TemplateApplied bool     `json:"template_applied"`
		Note            string   `json:"note"`
	}
	// Messy language codes on purpose: normalization is the service's job.
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name":             "MCP Created",
		"source_language":  "EN-US",
		"target_languages": []string{"DE-de", "fr"},
		"deadline":         "2026-09-01T17:00:00Z",
	}, &out); isErr {
		t.Fatalf("create_project failed: %s", text)
	}
	if out.ProjectID == 0 || out.SourceLanguage != "en-us" || out.TargetLanguages[0] != "de-de" {
		t.Fatalf("languages not normalized: %+v", out)
	}
	if out.TemplateApplied {
		t.Fatal("template_applied true without a template")
	}
	if !strings.Contains(out.Note, "navigate_to") {
		t.Fatalf("note missing the navigate handoff: %q", out.Note)
	}

	// Verify in the DB: project + settings profile + the mandatory default
	// workflow step (must hold on this path too).
	p, err := f.q.GetProject(context.Background(), db.GetProjectParams{ID: out.ProjectID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("created project not found in tenant: %v", err)
	}
	steps, err := f.q.ListSettingsWorkflowSteps(context.Background(), p.SettingsProfileID)
	if err != nil || len(steps) == 0 {
		t.Fatalf("default workflow step missing: %v %v", err, steps)
	}
	if p.Deadline.Time.IsZero() {
		t.Fatal("deadline not persisted")
	}

	// Validation rejections are model-visible and specific.
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Bad", "source_language": "en", "target_languages": []string{"en"},
	}, nil); !isErr || !strings.Contains(text, "cannot be the same as source") {
		t.Fatalf("same-language project accepted: %v %s", isErr, text)
	}
}

func TestIntegrationMCPCreateProjectWithTemplate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A template with a distinctive fuzzy threshold to prove the copy ran.
	tmplProfile, err := f.q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID: f.tenantA.ID, FuzzyThreshold: 0.91, MtOnUpload: true, TmOnUpload: true,
	})
	if err != nil {
		t.Fatalf("seed template profile: %v", err)
	}
	tmpl, err := f.q.CreateTemplate(ctx, db.CreateTemplateParams{
		TenantID: f.tenantA.ID, Name: "MCP Template", SettingsProfileID: tmplProfile.ID,
	})
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}

	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var out struct {
		ProjectID       int64 `json:"project_id"`
		TemplateApplied bool  `json:"template_applied"`
	}
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Templated", "source_language": "en", "target_languages": []string{"de"},
		"template_id": tmpl.ID,
	}, &out); isErr {
		t.Fatalf("create with template failed: %s", text)
	}
	if !out.TemplateApplied {
		t.Fatal("template_applied not reported")
	}
	p, err := f.q.GetProject(ctx, db.GetProjectParams{ID: out.ProjectID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	profile, err := f.q.GetSettingsProfile(ctx, p.SettingsProfileID)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if profile.FuzzyThreshold != 0.91 || !profile.MtOnUpload {
		t.Fatalf("template settings not copied: %+v", profile)
	}

	// Tenant B's template must be invisible: the template is validated BEFORE
	// the project is created, so a foreign ID fails clean — clear error and
	// no orphan project.
	profB, _ := f.q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID: f.tenantB.ID, FuzzyThreshold: 0.5, TmOnUpload: true,
	})
	tmplB, err := f.q.CreateTemplate(ctx, db.CreateTemplateParams{
		TenantID: f.tenantB.ID, Name: "Foreign", SettingsProfileID: profB.ID,
	})
	if err != nil {
		t.Fatalf("seed foreign template: %v", err)
	}
	isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Cross", "source_language": "en", "target_languages": []string{"de"},
		"template_id": tmplB.ID,
	}, nil)
	if !isErr || !strings.Contains(text, "template not found") {
		t.Fatalf("foreign template silently accepted: %v %s", isErr, text)
	}
	projects, err := f.q.ListProjects(ctx, f.tenantA.ID)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	for _, pr := range projects {
		if pr.Name == "Cross" {
			t.Fatal("orphan project created despite invalid template_id")
		}
	}
}

func TestIntegrationMCPCreateCustomerAndTermTools(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	ctx := context.Background()

	var cust struct {
		CustomerID int64  `json:"customer_id"`
		Name       string `json:"name"`
	}
	if isErr, text := callTool(t, s, "openloc_create_customer", map[string]any{"name": "  MCP Corp  "}, &cust); isErr {
		t.Fatalf("create_customer: %s", text)
	}
	if cust.Name != "MCP Corp" {
		t.Fatalf("name not trimmed: %q", cust.Name)
	}
	if _, err := f.q.GetCustomer(ctx, db.GetCustomerParams{ID: cust.CustomerID, TenantID: f.tenantA.ID}); err != nil {
		t.Fatalf("customer not in tenant A: %v", err)
	}

	var base struct {
		TermBaseID     int64  `json:"term_base_id"`
		SourceLanguage string `json:"source_language"`
	}
	if isErr, text := callTool(t, s, "openloc_create_term_base", map[string]any{
		"name": "MCP Glossary", "source_language": "EN",
	}, &base); isErr {
		t.Fatalf("create_term_base: %s", text)
	}
	if base.SourceLanguage != "en" {
		t.Fatalf("term base language not normalized: %+v", base)
	}
	// Attribution parity with the REST handler: a base created over MCP must
	// carry the calling identity so the terminology list can show its creator.
	if gb, err := f.q.GetTermBase(ctx, db.GetTermBaseParams{ID: base.TermBaseID, TenantID: f.tenantA.ID}); err != nil {
		t.Fatalf("term base not in tenant A: %v", err)
	} else if gb.CreatedBy == nil || *gb.CreatedBy != f.ownerA.ID {
		t.Fatalf("created_by = %v, want %d", gb.CreatedBy, f.ownerA.ID)
	}

	var entry struct {
		Status    string `json:"status"`
		ConceptID int64  `json:"concept_id"`
	}
	if isErr, text := callTool(t, s, "openloc_add_term", map[string]any{
		"term_base_id": base.TermBaseID, "source_term": "password", "target_term": "contraseña", "target_language": "es-AR",
	}, &entry); isErr {
		t.Fatalf("add_term: %s", text)
	}
	if entry.Status != "approved" {
		t.Fatalf("default status = %q, want approved", entry.Status)
	}
	if entry.ConceptID == 0 {
		t.Fatal("add_term did not return a concept_id")
	}

	// Bad status is a model-visible instruction, not a 500.
	if isErr, text := callTool(t, s, "openloc_add_term", map[string]any{
		"term_base_id": base.TermBaseID, "source_term": "x", "target_term": "y", "target_language": "es", "status": "banana",
	}, nil); !isErr || !strings.Contains(text, "approved, pending, or forbidden") {
		t.Fatalf("bad status accepted: %v %s", isErr, text)
	}

	// Cross-tenant term base: opaque not-found.
	sB := connect(t, f.url, testutil.SeedAPIToken(t, f.q, f.tenantB.ID, f.ownerB.ID, auth.CapabilityWrite, time.Hour))
	if isErr, text := callTool(t, sB, "openloc_add_term", map[string]any{
		"term_base_id": base.TermBaseID, "source_term": "a", "target_term": "b", "target_language": "es",
	}, nil); !isErr {
		t.Fatalf("tenant B wrote into tenant A's term base: %s", text)
	} else {
		assertOpaqueNotFound(t, text)
	}

	// --- create_concept: a full multilingual concept (concept-native path) ---
	var concept struct {
		ConceptID int64 `json:"concept_id"`
		Terms     int   `json:"terms"`
	}
	if isErr, text := callTool(t, s, "openloc_create_concept", map[string]any{
		"term_base_id": base.TermBaseID,
		"definition":   "a secret authentication string",
		"terms": []map[string]any{
			{"language": "EN", "text": "passphrase", "status": "approved"},
			{"language": "de", "text": "Passphrase", "status": "approved"},
			{"language": "fr", "text": "phrase de passe", "status": "pending"},
		},
	}, &concept); isErr {
		t.Fatalf("create_concept: %s", text)
	}
	if concept.ConceptID == 0 || concept.Terms != 3 {
		t.Fatalf("create_concept result: %+v (want a concept id + 3 terms)", concept)
	}
	// The base's derived target languages picked up de + fr from the new concept.
	reloaded, err := f.q.GetTermBase(ctx, db.GetTermBaseParams{ID: base.TermBaseID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("reload base: %v", err)
	}
	if !contains(reloaded.TargetLanguages, "de") || !contains(reloaded.TargetLanguages, "fr") {
		t.Fatalf("target_languages not synced from concept: %v", reloaded.TargetLanguages)
	}

	// Status is required per term — an empty or unrecognized value is rejected with
	// a model-visible instruction, never silently coerced to approved.
	for _, bad := range []string{"", "banana"} {
		if isErr, text := callTool(t, s, "openloc_create_concept", map[string]any{
			"term_base_id": base.TermBaseID,
			"terms":        []map[string]any{{"language": "en", "text": "x", "status": bad}},
		}, nil); !isErr || !strings.Contains(text, "approved, pending, or forbidden") {
			t.Fatalf("create_concept status %q accepted: %v %s", bad, isErr, text)
		}
	}

	// --- add_term_to_concept: append one term to the concept above ---
	var added struct {
		TermID   int64  `json:"term_id"`
		Language string `json:"language"`
		Status   string `json:"status"`
	}
	if isErr, text := callTool(t, s, "openloc_add_term_to_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID,
		"language": "IT", "text": "parola d'ordine", "status": "approved",
	}, &added); isErr {
		t.Fatalf("add_term_to_concept: %s", text)
	}
	if added.TermID == 0 || added.Language != "it" {
		t.Fatalf("add_term_to_concept result: %+v", added)
	}

	// Idempotent: re-adding the same language+text returns the SAME term, no duplicate.
	var again struct {
		TermID int64 `json:"term_id"`
	}
	if isErr, text := callTool(t, s, "openloc_add_term_to_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID,
		"language": "it", "text": "parola d'ordine", "status": "approved",
	}, &again); isErr {
		t.Fatalf("idempotent add_term_to_concept: %s", text)
	}
	if again.TermID != added.TermID {
		t.Fatalf("idempotent add created a new term: %d vs %d", again.TermID, added.TermID)
	}

	// Blank status rejected here too.
	if isErr, text := callTool(t, s, "openloc_add_term_to_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID,
		"language": "pt", "text": "senha", "status": "",
	}, nil); !isErr || !strings.Contains(text, "approved, pending, or forbidden") {
		t.Fatalf("add_term_to_concept blank status accepted: %v %s", isErr, text)
	}

	// A concept id that isn't in this base is an opaque not-found.
	if isErr, text := callTool(t, s, "openloc_add_term_to_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": int64(999999),
		"language": "pt", "text": "senha", "status": "approved",
	}, nil); !isErr {
		t.Fatalf("nonexistent concept accepted: %s", text)
	} else {
		assertOpaqueNotFound(t, text)
	}

	// --- update_concept: edit definition/domain of an existing concept ---
	var upd struct {
		Definition string `json:"definition"`
		Domain     string `json:"domain"`
	}
	// Change only the domain; the definition set at creation must be left intact
	// (omitted field = unchanged, not cleared).
	if isErr, text := callTool(t, s, "openloc_update_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "domain": "security",
	}, &upd); isErr {
		t.Fatalf("update_concept: %s", text)
	}
	if upd.Domain != "security" || upd.Definition != "a secret authentication string" {
		t.Fatalf("update_concept partial update wrong: %+v", upd)
	}
	got, err := f.q.GetConcept(ctx, db.GetConceptParams{ID: concept.ConceptID, TermBaseID: base.TermBaseID})
	if err != nil {
		t.Fatalf("reload concept: %v", err)
	}
	if got.Domain != "security" || got.Definition != "a secret authentication string" {
		t.Fatalf("update_concept not persisted: domain=%q def=%q", got.Domain, got.Definition)
	}
	// Nothing to update is a model-visible error, not a silent no-op.
	if isErr, text := callTool(t, s, "openloc_update_concept", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID,
	}, nil); !isErr || !strings.Contains(text, "nothing to update") {
		t.Fatalf("empty update accepted: %v %s", isErr, text)
	}

	// --- update_term: edit a term's status/pos/notes (partial; text+language kept) ---
	var ut struct {
		Language     string `json:"language"`
		Text         string `json:"text"`
		Status       string `json:"status"`
		PartOfSpeech string `json:"part_of_speech"`
		Notes        string `json:"notes"`
	}
	if isErr, text := callTool(t, s, "openloc_update_term", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "term_id": added.TermID,
		"status": "forbidden", "part_of_speech": "noun", "notes": "regional",
	}, &ut); isErr {
		t.Fatalf("update_term: %s", text)
	}
	if ut.Status != "forbidden" || ut.PartOfSpeech != "noun" || ut.Notes != "regional" {
		t.Fatalf("update_term result wrong: %+v", ut)
	}
	// Fields not provided are untouched — text and (immutable) language.
	if ut.Text != "parola d'ordine" || ut.Language != "it" {
		t.Fatalf("update_term changed untouched fields: %+v", ut)
	}
	gotTerm, err := f.q.GetTerm(ctx, added.TermID)
	if err != nil {
		t.Fatalf("reload term: %v", err)
	}
	if gotTerm.Status != "forbidden" || gotTerm.PartOfSpeech != "noun" || gotTerm.Notes != "regional" || gotTerm.Text != "parola d'ordine" {
		t.Fatalf("update_term not persisted: %+v", gotTerm)
	}
	// Empty text, bad status, and no-op updates are all model-visible errors.
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "term_id": added.TermID, "text": "   "}, "text cannot be empty"},
		{map[string]any{"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "term_id": added.TermID, "status": "banana"}, "approved, pending, or forbidden"},
		{map[string]any{"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "term_id": added.TermID}, "nothing to update"},
	} {
		if isErr, text := callTool(t, s, "openloc_update_term", tc.args, nil); !isErr || !strings.Contains(text, tc.want) {
			t.Fatalf("update_term should reject (%s): %v %s", tc.want, isErr, text)
		}
	}
	// A term_id not in this concept is an opaque not-found (ownership chain).
	if isErr, text := callTool(t, s, "openloc_update_term", map[string]any{
		"term_base_id": base.TermBaseID, "concept_id": concept.ConceptID, "term_id": int64(999999), "status": "approved",
	}, nil); !isErr {
		t.Fatalf("nonexistent term accepted: %s", text)
	} else {
		assertOpaqueNotFound(t, text)
	}
}

// add_term for the same source term across languages must build ONE multilingual
// concept, not a duplicate per call (INC: the concept identity is source term +
// domain). A different domain is a different meaning and stays separate.
func TestIntegrationMCPAddTermMergesConcepts(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	ctx := context.Background()

	var base struct {
		TermBaseID int64 `json:"term_base_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_term_base", map[string]any{
		"name": "Merge Base", "source_language": "en",
	}, &base); isErr {
		t.Fatalf("create_term_base: %s", text)
	}

	// Two adds for the SAME source term "to read", different target languages.
	var conceptIDs []int64
	for _, tc := range []struct{ tgt, lang string }{{"читать", "ru"}, {"読む", "ja-JP"}} {
		var res struct {
			ConceptID int64 `json:"concept_id"`
			TermID    int64 `json:"term_id"`
		}
		if isErr, text := callTool(t, s, "openloc_add_term", map[string]any{
			"term_base_id": base.TermBaseID, "source_term": "to read",
			"target_term": tc.tgt, "target_language": tc.lang,
		}, &res); isErr {
			t.Fatalf("add_term %s: %s", tc.lang, text)
		}
		if res.ConceptID == 0 || res.TermID == 0 {
			t.Fatalf("add_term %s did not return concept_id/term_id: %+v", tc.lang, res)
		}
		conceptIDs = append(conceptIDs, res.ConceptID)
	}
	// Both adds report the SAME concept_id — proof they merged, and that add_term
	// hands back a concept_id a model can chain into update_concept / add_term_to_concept.
	if conceptIDs[0] != conceptIDs[1] {
		t.Fatalf("add_term returned different concept_ids for one source term: %v", conceptIDs)
	}

	// Exactly ONE concept so far, holding the source term + both targets (3 terms).
	n, err := f.q.CountConcepts(ctx, db.CountConceptsParams{TermBaseID: base.TermBaseID})
	if err != nil {
		t.Fatalf("count concepts: %v", err)
	}
	if n != 1 {
		t.Fatalf("add_term should have merged into one concept, got %d concepts", n)
	}
	concept, err := f.q.FindConceptBySourceTerm(ctx, db.FindConceptBySourceTermParams{
		TermBaseID: base.TermBaseID, Domain: "", Language: "en", Text: "to read",
	})
	if err != nil {
		t.Fatalf("source concept not found: %v", err)
	}
	terms, err := f.q.ListTermsForConcepts(ctx, []int64{concept.ID})
	if err != nil {
		t.Fatalf("list terms: %v", err)
	}
	if len(terms) != 3 {
		t.Fatalf("expected 3 terms merged in one concept, got %d: %+v", len(terms), terms)
	}

	// Same source term but a DIFFERENT domain is a different meaning — new concept.
	if isErr, text := callTool(t, s, "openloc_add_term", map[string]any{
		"term_base_id": base.TermBaseID, "source_term": "to read",
		"target_term": "interpretar", "target_language": "es", "domain": "music",
	}, nil); isErr {
		t.Fatalf("add_term music: %s", text)
	}
	n2, err := f.q.CountConcepts(ctx, db.CountConceptsParams{TermBaseID: base.TermBaseID})
	if err != nil {
		t.Fatalf("count concepts: %v", err)
	}
	if n2 != 2 {
		t.Fatalf("a different domain should create a new concept, got %d concepts", n2)
	}

	// --- lookup_concept: resolve concept_ids by EXACT term text, project-independent ---
	type matchList struct {
		Concepts []struct {
			ConceptID int64  `json:"concept_id"`
			Domain    string `json:"domain"`
			Terms     []struct {
				Language string `json:"language"`
				Text     string `json:"text"`
			} `json:"terms"`
		} `json:"concepts"`
		Count int `json:"count"`
	}
	// Exact "to read" matches BOTH meanings (domainless + music), each with its full
	// term set so the model can tell the homographs apart.
	var all matchList
	if isErr, text := callTool(t, s, "openloc_lookup_concept", map[string]any{
		"term_base_id": base.TermBaseID, "text": "to read",
	}, &all); isErr {
		t.Fatalf("lookup_concept: %s", text)
	}
	if all.Count != 2 {
		t.Fatalf("lookup_concept(to read) = %d matches, want 2", all.Count)
	}
	for _, c := range all.Concepts {
		if len(c.Terms) == 0 {
			t.Fatalf("match %d returned no terms", c.ConceptID)
		}
	}
	// domain narrows to exactly one meaning.
	var music matchList
	if isErr, text := callTool(t, s, "openloc_lookup_concept", map[string]any{
		"term_base_id": base.TermBaseID, "text": "to read", "domain": "music",
	}, &music); isErr {
		t.Fatalf("lookup_concept music: %s", text)
	}
	if music.Count != 1 || music.Concepts[0].Domain != "music" {
		t.Fatalf("lookup_concept domain=music wrong: %+v", music)
	}
	// domain="" targets only the domainless concept — distinct from omitting domain.
	var domainless matchList
	if isErr, text := callTool(t, s, "openloc_lookup_concept", map[string]any{
		"term_base_id": base.TermBaseID, "text": "to read", "domain": "",
	}, &domainless); isErr {
		t.Fatalf("lookup_concept domainless: %s", text)
	}
	if domainless.Count != 1 || domainless.Concepts[0].Domain != "" {
		t.Fatalf("lookup_concept domain='' wrong: %+v", domainless)
	}
	// EXACT match only — a substring must NOT match (no ILIKE).
	var none matchList
	if isErr, text := callTool(t, s, "openloc_lookup_concept", map[string]any{
		"term_base_id": base.TermBaseID, "text": "to re",
	}, &none); isErr {
		t.Fatalf("lookup_concept substring: %s", text)
	}
	if none.Count != 0 {
		t.Fatalf("lookup_concept 'to re' matched %d — exact match must not do substrings", none.Count)
	}
	// language narrows the text match.
	var ru matchList
	if isErr, text := callTool(t, s, "openloc_lookup_concept", map[string]any{
		"term_base_id": base.TermBaseID, "text": "читать", "language": "ru",
	}, &ru); isErr {
		t.Fatalf("lookup_concept ru: %s", text)
	}
	if ru.Count != 1 {
		t.Fatalf("lookup_concept(читать, ru) = %d, want 1", ru.Count)
	}
}

// contains reports whether s is in xs.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// failingAudit wraps the real querier but fails audit inserts — the durable
// write-audit contract: the mutation happens, the tool reports it
// as applied-but-unaudited.
type failingAudit struct{}

func (failingAudit) InsertMCPToolCall(context.Context, db.InsertMCPToolCallParams) error {
	return errors.New("simulated audit outage")
}

func TestIntegrationMCPWriteAuditIsDurable(t *testing.T) {
	f := newFixture(t)
	srv, err := mcppkg.NewServer(mcppkg.Deps{
		Backend: mcppkg.NewDirectBackend(f.q),
		Audit:   failingAudit{},
		Version: "test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx := auth.WithWriteGrant(auth.WithClaims(context.Background(), &auth.Claims{
		TenantID: f.tenantA.ID, UserID: f.ownerA.ID, Role: "owner",
	}))
	cs, err := srv.ConnectInProcess(ctx, "owner")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	// A read still works with a broken audit (fire-and-forget is fine there)…
	if isErr, text := callInProcess(t, cs, "openloc_list_projects", map[string]any{}); isErr {
		t.Fatalf("read failed under audit outage: %s", text)
	}
	// …but a WRITE reports the audit failure — and the write itself landed.
	isErr, text := callInProcess(t, cs, "openloc_create_customer", map[string]any{"name": "Audited Corp"})
	if !isErr || !strings.Contains(text, "could not be recorded in the audit log") {
		t.Fatalf("write did not surface the audit failure: %v %s", isErr, text)
	}
	customers, err := f.q.ListCustomers(context.Background(), f.tenantA.ID)
	if err != nil {
		t.Fatalf("list customers: %v", err)
	}
	var found bool
	for _, c := range customers {
		if c.Name == "Audited Corp" {
			found = true
		}
	}
	if !found {
		t.Fatal("the write itself should have landed (audit failed AFTER execution)")
	}
}

// --- create_chart ----------------------------------------

func TestIntegrationMCPCreateChart(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	// First chart creates the dashboard.
	var res struct {
		ChartID          int64  `json:"chart_id"`
		DashboardID      int64  `json:"dashboard_id"`
		DashboardName    string `json:"dashboard_name"`
		DashboardCreated bool   `json:"dashboard_created"`
	}
	if isErr, text := callTool(t, s, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "MT by engine",
		"measure": "mt_engine_usage", "dimensions": []string{"mt_engine"},
		"last_days": 60, "chart_type": "bar", "x_key": "mt_engine", "series": []string{"words"},
	}, &res); isErr {
		t.Fatalf("create_chart: %s", text)
	}
	if !res.DashboardCreated || res.DashboardID == 0 || res.ChartID == 0 {
		t.Fatalf("first chart should create the dashboard: %+v", res)
	}

	// Second chart on "ops" (different case) reuses it — find-or-create is
	// case-insensitive and returns the canonical name.
	var res2 struct {
		DashboardID      int64  `json:"dashboard_id"`
		DashboardName    string `json:"dashboard_name"`
		DashboardCreated bool   `json:"dashboard_created"`
	}
	if isErr, text := callTool(t, s, "openloc_create_chart", map[string]any{
		"dashboard": "ops", "title": "Words per week",
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}, &res2); isErr {
		t.Fatalf("second create_chart: %s", text)
	}
	if res2.DashboardCreated || res2.DashboardID != res.DashboardID || res2.DashboardName != "Ops" {
		t.Fatalf("second chart should reuse dashboard 'Ops': %+v", res2)
	}

	// Bad definitions fail at creation, model-visibly — nothing persisted.
	for name, args := range map[string]map[string]any{
		"unknown measure": {
			"dashboard": "Ops", "title": "x", "measure": "revenue",
			"last_days": 30, "chart_type": "bar", "x_key": "week", "series": []string{"words"},
			"dimensions": []string{"week"},
		},
		"x_key not a dimension": {
			"dashboard": "Ops", "title": "x", "measure": "qa_issues",
			"dimensions": []string{"severity"},
			"last_days":  30, "chart_type": "bar", "x_key": "check_type", "series": []string{"issues"},
		},
		"bad chart type": {
			"dashboard": "Ops", "title": "x", "measure": "qa_issues",
			"dimensions": []string{"severity"},
			"last_days":  30, "chart_type": "donut", "x_key": "severity", "series": []string{"issues"},
		},
		"bad range": {
			"dashboard": "Ops", "title": "x", "measure": "qa_issues",
			"dimensions": []string{"severity"},
			"last_days":  9999, "chart_type": "bar", "x_key": "severity", "series": []string{"issues"},
		},
	} {
		if isErr, text := callTool(t, s, "openloc_create_chart", args, nil); !isErr {
			t.Errorf("%s: expected tool error, got success: %s", name, text)
		}
	}

	// Read-only token refuses.
	ro := connect(t, f.url, f.tokenA)
	if isErr, text := callTool(t, ro, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "x", "measure": "qa_issues",
		"dimensions": []string{"severity"},
		"last_days":  30, "chart_type": "bar", "x_key": "severity", "series": []string{"issues"},
	}, nil); !isErr || !strings.Contains(text, "write") {
		t.Errorf("read-only token must refuse create_chart: isErr=%v text=%s", isErr, text)
	}

	// Tenant B sees no dashboard named Ops — find-or-create is tenant-scoped,
	// so the same name yields a NEW dashboard, not tenant A's.
	sb := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	var resB struct {
		DashboardID      int64 `json:"dashboard_id"`
		DashboardCreated bool  `json:"dashboard_created"`
	}
	if isErr, text := callTool(t, sb, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "B chart", "measure": "qa_issues",
		"dimensions": []string{"severity"},
		"last_days":  30, "chart_type": "bar", "x_key": "severity", "series": []string{"issues"},
	}, &resB); isErr {
		t.Fatalf("tenant B create_chart: %s", text)
	}
	if !resB.DashboardCreated || resB.DashboardID == res.DashboardID {
		t.Fatalf("tenant B must get its OWN 'Ops' dashboard: %+v vs A's %d", resB, res.DashboardID)
	}
}

// create_project with workflow_id copies the library workflow's steps;
// a foreign workflow fails clean with no orphan project.
func TestIntegrationMCPCreateProjectWithWorkflow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	proofread := testutil.SeedStepDefinition(t, f.q, f.tenantA.ID, "Proofread", "human", "", nil)
	wf, err := f.q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: f.tenantA.ID, Name: "Proof only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{
		WorkflowID: wf.ID, StepDefinitionID: proofread.ID, StepOrder: 0,
	}); err != nil {
		t.Fatal(err)
	}

	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var out struct {
		ProjectID       int64 `json:"project_id"`
		WorkflowApplied bool  `json:"workflow_applied"`
	}
	if isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Workflowed", "source_language": "en", "target_languages": []string{"de"},
		"workflow_id": wf.ID,
	}, &out); isErr {
		t.Fatalf("create with workflow failed: %s", text)
	}
	if !out.WorkflowApplied {
		t.Fatal("workflow_applied not reported")
	}
	p, err := f.q.GetProject(ctx, db.GetProjectParams{ID: out.ProjectID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := f.q.ListSettingsWorkflowSteps(ctx, p.SettingsProfileID)
	if err != nil || len(steps) != 1 || steps[0].Name != "Proofread" || steps[0].StepDefinitionID == nil {
		t.Fatalf("expected the workflow's single Proofread step, got %+v (err=%v)", steps, err)
	}
	profile, _ := f.q.GetSettingsProfile(ctx, p.SettingsProfileID)
	if profile.WorkflowID == nil || *profile.WorkflowID != wf.ID {
		t.Errorf("provenance not stamped: %v", profile.WorkflowID)
	}

	foreign, err := f.q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: f.tenantB.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	countBefore, _ := f.q.ListProjects(ctx, f.tenantA.ID)
	isErr, text := callTool(t, s, "openloc_create_project", map[string]any{
		"name": "Cross", "source_language": "en", "target_languages": []string{"de"}, "workflow_id": foreign.ID,
	}, nil)
	if !isErr || !strings.Contains(text, "workflow not found") {
		t.Fatalf("foreign workflow accepted: %v %s", isErr, text)
	}
	countAfter, _ := f.q.ListProjects(ctx, f.tenantA.ID)
	if len(countAfter) != len(countBefore) {
		t.Fatalf("foreign workflow left an orphan project: %d → %d", len(countBefore), len(countAfter))
	}
}

// --- set_dashboard_filters --------------------------------

func TestIntegrationMCPSetDashboardFilters(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	// The tool configures an EXISTING dashboard, so make one first.
	var chart struct {
		DashboardID int64 `json:"dashboard_id"`
	}
	if isErr, text := callTool(t, s, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "Words per week",
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}, &chart); isErr {
		t.Fatalf("create_chart: %s", text)
	}

	var res struct {
		DashboardID   int64    `json:"dashboard_id"`
		DashboardName string   `json:"dashboard_name"`
		Controls      []string `json:"controls"`
	}
	// Matched case-insensitively, like create_chart's find-or-create.
	if isErr, text := callTool(t, s, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "ops",
		"controls":  []string{"date_range", "unit", "granularity", "customer"},
	}, &res); isErr {
		t.Fatalf("set_dashboard_filters: %s", text)
	}
	if res.DashboardID != chart.DashboardID || res.DashboardName != "Ops" || len(res.Controls) != 4 {
		t.Fatalf("result = %+v, want the existing 'Ops' dashboard with 4 controls", res)
	}

	// The config is what the dashboard endpoint will serve back.
	stored, err := f.q.GetDashboardTenant(context.Background(), db.GetDashboardTenantParams{
		ID: chart.DashboardID, TenantID: f.tenantA.ID, UserID: &f.ownerA.ID,
	})
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	var cfg dashboard.FilterConfig
	if err := json.Unmarshal(stored.Filters, &cfg); err != nil {
		t.Fatalf("stored filters unparseable: %v — %s", err, stored.Filters)
	}
	if !slices.Contains(cfg.Controls, "customer") {
		t.Errorf("stored controls = %v", cfg.Controls)
	}

	// Replaces rather than merges: an empty list removes the control row.
	if isErr, text := callTool(t, s, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Ops", "controls": []string{},
	}, &res); isErr {
		t.Fatalf("clearing controls: %s", text)
	}
	if len(res.Controls) != 0 {
		t.Errorf("controls after clear = %v, want empty", res.Controls)
	}

	// Closed vocabulary: a narrow dimension is a real metrics dimension but not
	// a dashboard control, and the model has to learn that here.
	if isErr, _ := callTool(t, s, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Ops", "controls": []string{"severity"},
	}, &res); !isErr {
		t.Error("unknown control should fail")
	}

	// Never creates a dashboard — configuring a name that doesn't exist is a typo.
	if isErr, _ := callTool(t, s, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Nope", "controls": []string{"unit"},
	}, &res); !isErr {
		t.Error("unknown dashboard should fail, not create one")
	}
}

// Ownership decides the gate, matching dashboard.mayWrite: a SHARED dashboard
// needs owner/admin because its controls change what the whole org sees, while
// a personal one is its owner's to configure even as a project_manager.
func TestIntegrationMCPSetDashboardFiltersOwnershipGate(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-pm@test.com", "pass", "project_manager")
	pmSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	ownerSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	chartArgs := func(dash string) map[string]any {
		return map[string]any{
			"dashboard": dash, "title": "Words per week",
			"measure": "words_processed", "dimensions": []string{"week"},
			"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
		}
	}
	var chart struct {
		DashboardID int64 `json:"dashboard_id"`
		Personal    bool  `json:"personal"`
	}
	var res struct{}

	// A chart created in conversation lands on a PERSONAL dashboard, so the PM
	// owns this one and may configure it.
	if isErr, text := callTool(t, pmSession, "openloc_create_chart", chartArgs("PM Ops"), &chart); isErr {
		t.Fatalf("PM create_chart: %s", text)
	}
	if !chart.Personal {
		t.Fatal("a chat-created dashboard should be personal")
	}
	if isErr, text := callTool(t, pmSession, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "PM Ops", "controls": []string{"unit"},
	}, &res); isErr {
		t.Fatalf("PM should configure their OWN dashboard: %s", text)
	}

	// A shared dashboard is different: the PM can see and use it, but its
	// controls are org-wide, so configuring it needs owner/admin.
	if isErr, text := callTool(t, ownerSession, "openloc_create_chart", chartArgs("Shared Ops"), &chart); isErr {
		t.Fatalf("owner create_chart: %s", text)
	}
	if _, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
		ID: chart.DashboardID, TenantID: f.tenantA.ID, OwnerID: nil,
	}); err != nil {
		t.Fatalf("share dashboard: %v", err)
	}

	isErr, text := callTool(t, pmSession, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Shared Ops", "controls": []string{"unit"},
	}, &res)
	if !isErr {
		t.Fatal("project_manager must not configure a SHARED dashboard")
	}
	if !strings.Contains(text, "shared") {
		t.Errorf("denial should explain it's the sharing that's gated, got %q", text)
	}

	if isErr, text := callTool(t, ownerSession, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Shared Ops", "controls": []string{"unit"},
	}, &res); isErr {
		t.Fatalf("owner should configure a shared dashboard: %s", text)
	}
}

// Leaving `shared` unset must not be a way onto the org's dashboards. The
// name lookup falls back to a SHARED dashboard of that name, and adding a
// chart to one is a write to what the whole org sees — owner/admin, exactly
// as the HTTP builder enforces through mayWrite.
func TestIntegrationMCPCreateChartOnAnExistingSharedDashboardIsAdminOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-pm3@test.com", "pass", "project_manager")
	orgWide, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Org Overview"})
	if err != nil {
		t.Fatalf("seed shared dashboard: %v", err)
	}

	args := map[string]any{
		"dashboard": "Org Overview", "title": "Sneaked in",
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}
	pmSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	isErr, text := callTool(t, pmSession, "openloc_create_chart", args, nil)
	if !isErr {
		t.Fatal("project_manager added a chart to a shared dashboard by leaving `shared` unset")
	}
	if !strings.Contains(text, "owner or admin") {
		t.Errorf("refusal should name the gate, got %q", text)
	}
	charts, err := f.q.ListDashboardCharts(ctx, db.ListDashboardChartsParams{
		DashboardID: orgWide.ID, TenantID: f.tenantA.ID, UserID: &pm.ID,
	})
	if err != nil {
		t.Fatalf("list charts: %v", err)
	}
	if len(charts) != 0 {
		t.Fatalf("the shared dashboard gained %d chart(s) from a project_manager", len(charts))
	}

	// An owner still adds to it the same way.
	ownerSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, ownerSession, "openloc_create_chart", args, nil); isErr {
		t.Fatalf("owner adding to the shared dashboard: %s", text)
	}
}

// Creating a SHARED dashboard is owner/admin, matching every other write to
// one. A project manager gets a refusal rather than a quietly-private dashboard
// they would only discover when a colleague couldn't see it.
func TestIntegrationMCPCreateChartSharedIsAdminOnly(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-pm2@test.com", "pass", "project_manager")

	args := func(dash string, shared bool) map[string]any {
		return map[string]any{
			"dashboard": dash, "title": "Words per week", "shared": shared,
			"measure": "words_processed", "dimensions": []string{"week"},
			"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
		}
	}
	var res struct {
		DashboardID int64 `json:"dashboard_id"`
		Personal    bool  `json:"personal"`
	}

	pmSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	isErr, text := callTool(t, pmSession, "openloc_create_chart", args("PM Shared", true), &res)
	if !isErr {
		t.Fatal("project_manager must not create a shared dashboard")
	}
	if !strings.Contains(text, "owner or admin") {
		t.Errorf("refusal should name the gate, got %q", text)
	}
	// Without the flag the same call succeeds, and lands on their own.
	if isErr, text := callTool(t, pmSession, "openloc_create_chart", args("PM Own", false), &res); isErr {
		t.Fatalf("PM should create a personal dashboard: %s", text)
	}
	if !res.Personal {
		t.Error("a chart created without shared should be personal")
	}

	// An owner gets the shared dashboard, and the result says so.
	ownerSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, ownerSession, "openloc_create_chart", args("Org Wide", true), &res); isErr {
		t.Fatalf("owner shared create: %s", text)
	}
	if res.Personal {
		t.Error("shared:true should produce an org-wide dashboard")
	}
	stored, err := f.q.GetDashboardTenant(context.Background(), db.GetDashboardTenantParams{
		ID: res.DashboardID, TenantID: f.tenantA.ID, UserID: &f.ownerA.ID,
	})
	if err != nil || stored.OwnerID != nil {
		t.Errorf("owner_id should be NULL for a shared dashboard: %+v %v", stored.OwnerID, err)
	}
}

// From the Shared tab, "add it to Operations" means the ORG's Operations — a
// personal dashboard of the same name must not shadow it.
func TestIntegrationMCPCreateChartSharedIgnoresPersonalNamesake(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	personal, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Operations", OwnerID: &f.ownerA.ID,
	})
	if err != nil {
		t.Fatalf("seed personal: %v", err)
	}
	shared, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Operations", OwnerID: nil,
	})
	if err != nil {
		t.Fatalf("seed shared: %v", err)
	}

	var res struct {
		DashboardID int64 `json:"dashboard_id"`
		Personal    bool  `json:"personal"`
	}
	args := map[string]any{
		"dashboard": "Operations", "title": "Words per week", "shared": true,
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}
	if isErr, text := callTool(t, s, "openloc_create_chart", args, &res); isErr {
		t.Fatalf("shared create: %s", text)
	}
	if res.DashboardID != shared.ID {
		t.Errorf("landed on dashboard %d, want the SHARED one (%d), not the personal namesake (%d)",
			res.DashboardID, shared.ID, personal.ID)
	}

	// Without the flag, the caller's own wins.
	args["shared"] = false
	if isErr, text := callTool(t, s, "openloc_create_chart", args, &res); isErr {
		t.Fatalf("personal create: %s", text)
	}
	if res.DashboardID != personal.ID {
		t.Errorf("landed on %d, want the caller's own (%d)", res.DashboardID, personal.ID)
	}
}

// Pins that a chart-level control row can be set through MCP.
func TestIntegrationMCPSetChartFilters(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	if isErr, text := callTool(t, s, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "Volume over time by segment type",
		"measure": "segments_by_origin", "dimensions": []string{"week", "kind"},
		"last_days": 30, "chart_type": "bar", "x_key": "week", "series": []string{"words"},
	}, &struct{}{}); isErr {
		t.Fatalf("create_chart: %s", text)
	}

	var res struct {
		ChartTitle    string   `json:"chart_title"`
		DashboardName string   `json:"dashboard_name"`
		Controls      []string `json:"controls"`
	}
	// Granularity on ONE chart.
	if isErr, text := callTool(t, s, "openloc_set_chart_filters", map[string]any{
		"dashboard": "ops", "chart": "volume over time by segment type",
		"controls": []string{"granularity"},
	}, &res); isErr {
		t.Fatalf("set_chart_filters: %s", text)
	}
	if res.ChartTitle != "Volume over time by segment type" || !slices.Equal(res.Controls, []string{"granularity"}) {
		t.Fatalf("result = %+v", res)
	}

	// It must not disturb the DASHBOARD's row — they are separate lists.
	var dash struct {
		Controls []string `json:"controls"`
	}
	if isErr, text := callTool(t, s, "openloc_set_dashboard_filters", map[string]any{
		"dashboard": "Ops", "controls": []string{"date_range"},
	}, &dash); isErr {
		t.Fatalf("set_dashboard_filters: %s", text)
	}
	var again struct {
		Controls []string `json:"controls"`
	}
	if isErr, text := callTool(t, s, "openloc_set_chart_filters", map[string]any{
		"dashboard": "Ops", "chart": "Volume over time by segment type",
		"controls": []string{"granularity", "customer"},
	}, &again); isErr {
		t.Fatalf("set_chart_filters again: %s", text)
	}
	if !slices.Equal(again.Controls, []string{"granularity", "customer"}) {
		t.Errorf("controls = %v", again.Controls)
	}
	// What is stored, not what the tools echoed back: two separate lists.
	dashStored, chartStored := f.storedControls(t, f.tenantA.ID, f.ownerA.ID, "Ops", "Volume over time by segment type")
	if !slices.Equal(dashStored, []string{"date_range"}) {
		t.Errorf("stored dashboard controls = %v, want [date_range]", dashStored)
	}
	if !slices.Equal(chartStored, []string{"granularity", "customer"}) {
		t.Errorf("stored chart controls = %v, want [granularity customer]", chartStored)
	}
}

// storedControls reads a dashboard's and one of its charts' control lists
// straight from the rows.
func (f *fixture) storedControls(t *testing.T, tenantID, userID int64, dashboard, chart string) (dash, own []string) {
	t.Helper()
	ctx := context.Background()
	d, err := f.q.FindDashboardByName(ctx, db.FindDashboardByNameParams{TenantID: tenantID, Name: dashboard, UserID: &userID})
	if err != nil {
		t.Fatalf("find dashboard %q: %v", dashboard, err)
	}
	row, err := f.q.GetDashboardTenant(ctx, db.GetDashboardTenantParams{ID: d.ID, TenantID: tenantID, UserID: &userID})
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	c, err := f.q.FindChartByTitle(ctx, db.FindChartByTitleParams{TenantID: tenantID, DashboardID: d.ID, Title: chart, UserID: &userID})
	if err != nil {
		t.Fatalf("find chart %q: %v", chart, err)
	}
	var dc, cc struct {
		Controls []string `json:"controls"`
	}
	if err := json.Unmarshal(row.Filters, &dc); err != nil {
		t.Fatalf("decode dashboard filters: %v", err)
	}
	if err := json.Unmarshal(c.Controls, &cc); err != nil {
		t.Fatalf("decode chart controls: %v", err)
	}
	return dc.Controls, cc.Controls
}

// Both filter tools resolve by NAME, so another organisation's "Ops" is only a
// name away. Tenant B's session reaches nothing, and tenant A's rows stay put.
func TestIntegrationMCPFilterToolsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	a := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, a, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "Words per week",
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}, &struct{}{}); isErr {
		t.Fatalf("create_chart: %s", text)
	}
	dashBefore, chartBefore := f.storedControls(t, f.tenantA.ID, f.ownerA.ID, "Ops", "Words per week")

	b := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_set_chart_filters", map[string]any{"dashboard": "Ops", "chart": "Words per week", "controls": []string{"granularity"}}},
		{"openloc_set_dashboard_filters", map[string]any{"dashboard": "Ops", "controls": []string{"customer"}}},
	} {
		if isErr, text := callTool(t, b, tc.tool, tc.args, &struct{}{}); !isErr || !strings.Contains(strings.ToLower(text), "not found") {
			t.Errorf("tenant B %s: isErr=%v text=%s, want a not-found error", tc.tool, isErr, text)
		}
	}
	dashAfter, chartAfter := f.storedControls(t, f.tenantA.ID, f.ownerA.ID, "Ops", "Words per week")
	if !slices.Equal(dashAfter, dashBefore) || !slices.Equal(chartAfter, chartBefore) {
		t.Errorf("tenant B changed tenant A's controls: dashboard %v → %v, chart %v → %v", dashBefore, dashAfter, chartBefore, chartAfter)
	}
}

func TestIntegrationMCPSetChartFiltersRejectsUnknownNames(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, s, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "Words per week",
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}, &struct{}{}); isErr {
		t.Fatalf("create_chart: %s", text)
	}
	for _, tc := range []struct{ name, dashboard, chart string }{
		{"unknown dashboard", "Nope", "Words per week"},
		{"unknown chart", "Ops", "No such chart"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isErr, text := callTool(t, s, "openloc_set_chart_filters", map[string]any{
				"dashboard": tc.dashboard, "chart": tc.chart, "controls": []string{"granularity"},
			}, &struct{}{})
			if !isErr {
				t.Errorf("expected a not-found error, got success")
			}
			_ = text
		})
	}
	// A control name the compiler has never heard of fails where the model can
	// read the message and correct itself.
	if isErr, _ := callTool(t, s, "openloc_set_chart_filters", map[string]any{
		"dashboard": "Ops", "chart": "Words per week", "controls": []string{"colour"},
	}, &struct{}{}); !isErr {
		t.Error("an unknown control was accepted")
	}
}

// Configuring a chart on a SHARED dashboard changes what the whole org reads,
// so it follows the same gate as the dashboard's own row.
func TestIntegrationMCPSetChartFiltersOwnershipGate(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, owner, "openloc_create_chart", map[string]any{
		"dashboard": "Ops", "title": "Words per week", "shared": true,
		"measure": "words_processed", "dimensions": []string{"week"},
		"last_days": 30, "chart_type": "area", "x_key": "week", "series": []string{"words"},
	}, &struct{}{}); isErr {
		t.Fatalf("create_chart: %s", text)
	}
	pmUser := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-chartpm@test.com", "pass", "project_manager")
	pm := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pmUser.ID))
	if isErr, _ := callTool(t, pm, "openloc_set_chart_filters", map[string]any{
		"dashboard": "Ops", "chart": "Words per week", "controls": []string{"granularity"},
	}, &struct{}{}); !isErr {
		t.Error("a project manager configured a chart on a SHARED dashboard")
	}
}
