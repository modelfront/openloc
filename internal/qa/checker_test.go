package qa

import (
	"strings"
	"testing"
)

// --- CheckEmpty ---

func TestCheckEmpty_EmptyTarget(t *testing.T) {
	issue := CheckEmpty("", "error")
	if issue == nil {
		t.Fatal("expected issue for empty target")
	}
	if issue.CheckType != "empty_target" {
		t.Fatalf("expected check_type=empty_target, got %s", issue.CheckType)
	}
}

func TestCheckEmpty_WhitespaceOnly(t *testing.T) {
	issue := CheckEmpty("   \t\n  ", "error")
	if issue == nil {
		t.Fatal("expected issue for whitespace-only target")
	}
}

func TestCheckEmpty_HasContent(t *testing.T) {
	issue := CheckEmpty("Hello world", "error")
	if issue != nil {
		t.Fatal("expected no issue for non-empty target")
	}
}

// --- CheckTagsDetailed ---

func allTagChecks() TagChecksConfig {
	return DefaultTagChecksConfig()
}

func TestCheckTagsDetailed_AllPresent(t *testing.T) {
	issues := CheckTagsDetailed("<b>Hello</b> world", "<b>Hola</b> mundo", "error", allTagChecks())
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckTagsDetailed_MissingTag(t *testing.T) {
	issues := CheckTagsDetailed("<b>Hello</b> world", "Hola mundo", "error", allTagChecks())
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (tag_missing), got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "tag_missing" {
		t.Fatalf("expected tag_missing, got %s", issues[0].CheckType)
	}
}

func TestCheckTagsDetailed_ExtraTag(t *testing.T) {
	issues := CheckTagsDetailed("Hello world", "<b>Hola</b> mundo", "warning", allTagChecks())
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (tag_extra), got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "tag_extra" {
		t.Fatalf("expected tag_extra, got %s", issues[0].CheckType)
	}
}

func TestCheckTagsDetailed_UnclosedHTML(t *testing.T) {
	// <b> without </b> where source also has <b> — this is "missing </b>" not "unclosed <b>"
	// because the root cause is the missing tag.
	issues := CheckTagsDetailed("<b>Hello</b>", "<b>Hola", "error", allTagChecks())
	types := issueTypes(issues)
	if !types["tag_missing"] {
		t.Error("expected tag_missing (</b> missing from target)")
	}
	if types["tag_unclosed"] {
		t.Error("should NOT have tag_unclosed — already reported as tag_missing")
	}
}

func TestCheckTagsDetailed_PureUnclosed(t *testing.T) {
	// Target has <b> without </b>, but source has neither — so it's extra + unclosed
	issues := CheckTagsDetailed("Hello", "<b>Hola", "error", allTagChecks())
	types := issueTypes(issues)
	if !types["tag_extra"] {
		t.Error("expected tag_extra (<b> not in source)")
	}
	// <b> is already reported as extra, so unclosed should be suppressed
	if types["tag_unclosed"] {
		t.Error("should NOT have tag_unclosed — already reported as tag_extra")
	}
}

func TestCheckTagsDetailed_StandaloneUnclosed(t *testing.T) {
	// Both source and target have <b>, but only source has </b>.
	// Source: <b>X</b>, Target: <b>X — the </b> is missing.
	// This should be tag_missing only (not tag_unclosed since the root cause is missing).
	issues := CheckTagsDetailed("<b>Hello</b>", "<b>Hola", "error", allTagChecks())
	types := issueTypes(issues)
	if !types["tag_missing"] {
		t.Error("expected tag_missing for </b>")
	}
	if types["tag_unclosed"] {
		t.Error("should NOT have tag_unclosed — </b> missing is the root cause")
	}
}

func TestCheckTagsDetailed_XLIFFBptEpt(t *testing.T) {
	src := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>`
	tgt := `Haga clic <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept>`
	issues := CheckTagsDetailed(src, tgt, "error", allTagChecks())
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

func TestCheckTagsDetailed_XLIFFMissingEpt(t *testing.T) {
	src := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>`
	tgt := `Haga clic <bpt id="1"><b></bpt>aquí`
	issues := CheckTagsDetailed(src, tgt, "error", allTagChecks())
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "tag_missing" {
		t.Fatalf("expected tag_missing, got %s", issues[0].CheckType)
	}
}

func TestCheckTagsDetailed_NoCascade(t *testing.T) {
	// Removing </b> from target should produce exactly 1 issue (missing), not 3
	src := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>`
	tgt := `Haga clic <bpt id="1"><b></bpt>aquí`
	issues := CheckTagsDetailed(src, tgt, "error", allTagChecks())
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue (no cascading), got %d: %+v", len(issues), issues)
	}
}

func TestCheckTagsDetailed_SelfClosingIgnored(t *testing.T) {
	issues := CheckTagsDetailed("Line one<br/>Line two", "Línea uno<br/>Línea dos", "error", allTagChecks())
	if len(issues) != 0 {
		t.Fatalf("expected no issues for self-closing, got %d: %+v", len(issues), issues)
	}
}

func TestCheckTagsDetailed_NoTags(t *testing.T) {
	issues := CheckTagsDetailed("Hello", "Hola", "error", allTagChecks())
	if len(issues) != 0 {
		t.Fatal("expected no issues for plain text")
	}
}

func TestCheckTagsDetailed_ConfigDisablesMissing(t *testing.T) {
	cfg := TagChecksConfig{Missing: false, Extra: true, Unclosed: true, Unopened: true}
	issues := CheckTagsDetailed("<b>Hello</b>", "Hola", "error", cfg)
	types := issueTypes(issues)
	if types["tag_missing"] {
		t.Error("tag_missing should be disabled")
	}
}

func TestCheckTagsDetailed_Unopened(t *testing.T) {
	// Target has </b> without <b>, and source has neither
	issues := CheckTagsDetailed("Hello", "Hola</b>", "error", allTagChecks())
	types := issueTypes(issues)
	if !types["tag_extra"] {
		t.Error("expected tag_extra for </b>")
	}
}

// --- displayTag ---

func TestDisplayTag_BPT(t *testing.T) {
	if got := displayTag(`<bpt id="1"><b></bpt>`); got != "<1>" {
		t.Errorf("displayTag(bpt) = %q, want <1>", got)
	}
}

func TestDisplayTag_EPT(t *testing.T) {
	if got := displayTag(`<ept id="1"></b></ept>`); got != "</1>" {
		t.Errorf("displayTag(ept) = %q, want </1>", got)
	}
}

func TestDisplayTag_PH(t *testing.T) {
	if got := displayTag(`<ph id="2">{key}</ph>`); got != "{2}" {
		t.Errorf("displayTag(ph) = %q, want {2}", got)
	}
}

func TestDisplayTag_HTML(t *testing.T) {
	if got := displayTag("<b>"); got != "<b>" {
		t.Errorf("displayTag(<b>) = %q, want <b>", got)
	}
}

func TestDisplayTag_ClosingEpt(t *testing.T) {
	// </ept> is the closing tag of an XLIFF ept element — should not appear in user-facing messages
	got := displayTag("</ept>")
	if got == "</ept>" {
		t.Errorf("displayTag(</ept>) should NOT return raw </ept> — must be suppressed or converted")
	}
}

func TestDisplayTag_ClosingBpt(t *testing.T) {
	got := displayTag("</bpt>")
	if got == "</bpt>" {
		t.Errorf("displayTag(</bpt>) should NOT return raw </bpt> — must be suppressed or converted")
	}
}

func TestCheckTagsDetailed_NoXLIFFClosingElementsInMessages(t *testing.T) {
	// When ept is removed from target, the message should show </1>, not </ept>
	src := `Haga clic <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept> para continuar.`
	tgt := `Haga clic <bpt id="1"><b></bpt>aquí para continuar.`
	issues := CheckTagsDetailed(src, tgt, "error", allTagChecks())
	if len(issues) != 1 || issues[0].CheckType != "tag_missing" {
		t.Fatalf("want exactly one tag_missing issue, got %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "</1>") {
		t.Errorf("QA message should name the missing tag as </1>: %s", issues[0].Message)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, "</ept>") {
			t.Errorf("QA message should not contain raw </ept>: %s", issue.Message)
		}
		if strings.Contains(issue.Message, "</bpt>") {
			t.Errorf("QA message should not contain raw </bpt>: %s", issue.Message)
		}
	}
}

// --- CheckNumbers ---

// findIssue returns the first issue with the given check_type, or nil.
func findIssue(issues []Issue, checkType string) *Issue {
	for i := range issues {
		if issues[i].CheckType == checkType {
			return &issues[i]
		}
	}
	return nil
}

func TestCheckNumbers_AllPresent(t *testing.T) {
	issues := CheckNumbers("There are 42 items and 3.14 units", "Hay 42 artículos y 3.14 unidades", "error")
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %+v", issues)
	}
}

func TestCheckNumbers_MissingNumber(t *testing.T) {
	issues := CheckNumbers("There are 42 items", "Hay artículos", "error")
	miss := findIssue(issues, "number_consistency")
	if miss == nil {
		t.Fatal("expected number_consistency issue for missing number")
	}
	// Dropping a source number keeps the rule's configured (blocking) severity.
	if miss.Severity != "error" {
		t.Errorf("missing-number issue should carry the configured severity 'error', got %q", miss.Severity)
	}
}

func TestCheckNumbers_ThousandSeparator(t *testing.T) {
	issues := CheckNumbers("Total: 1,000 items", "Total: 1000 Artikel", "error")
	if len(issues) != 0 {
		t.Fatalf("expected no issues (1,000 = 1000), got: %+v", issues)
	}
}

func TestCheckNumbers_NoNumbers(t *testing.T) {
	issues := CheckNumbers("Hello world", "Hola mundo", "error")
	if len(issues) != 0 {
		t.Fatal("expected no issues when neither side has numbers")
	}
}

// A number present ONLY in the translation (not the source)
// must be flagged — but as a non-blocking WARNING (number_added), not the
// blocking error, even when the source has no numbers at all.
func TestCheckNumbers_ExtraInTargetOnly(t *testing.T) {
	issues := CheckNumbers("There are some items", "Hay 42 artículos", "error")
	added := findIssue(issues, "number_added")
	if added == nil {
		t.Fatal("expected number_added issue: 42 appears in target but not source")
	}
	if added.Severity != "warning" {
		t.Errorf("target-only number must be a non-blocking warning, got severity %q", added.Severity)
	}
	// It must NOT surface as the blocking number_consistency error.
	if findIssue(issues, "number_consistency") != nil {
		t.Error("a target-only number must not raise the blocking number_consistency error")
	}
}

func TestCheckNumbers_ExtraWhenSourceAlsoHasNumbers(t *testing.T) {
	issues := CheckNumbers("There are 42 items", "Hay 42 artículos en 7 cajas", "error")
	added := findIssue(issues, "number_added")
	if added == nil || added.Severity != "warning" {
		t.Fatalf("expected a number_added warning for the target-only 7, got: %+v", issues)
	}
}

// --- CheckTerminology (concept model) ---

// approvedConcept is a one-source-term concept whose approved target synonyms are
// the given targets.
func approvedConcept(src string, approvedTargets ...string) ConceptForQA {
	return ConceptForQA{ConceptID: 1, SourceTerms: []string{src}, ApprovedTargets: approvedTargets}
}

func TestCheckTerminology_TermUsed(t *testing.T) {
	c := []ConceptForQA{approvedConcept("password", "Passwort")}
	issue := CheckTerminology("Reset your password", "Setzen Sie Ihr Passwort zurück", c, "warning")
	if issue != nil {
		t.Fatalf("expected no issue, got: %s", issue.Message)
	}
}

func TestCheckTerminology_TermNotUsed(t *testing.T) {
	c := []ConceptForQA{approvedConcept("password", "Passwort")}
	issue := CheckTerminology("Reset your password", "Setzen Sie Ihr Kennwort zurück", c, "warning")
	if issue == nil {
		t.Fatal("expected issue for unused approved term")
	}
}

// Pins that a concept with two approved synonyms is satisfied when the target
// uses EITHER one (OR within a concept), so the unused synonym is not flagged.
func TestCheckTerminology_ApprovedSynonymAlternativeSatisfies(t *testing.T) {
	c := []ConceptForQA{approvedConcept("password", "Passwort", "Kennwort")}
	if issue := CheckTerminology("Reset your password", "Setzen Sie Ihr Passwort zurück", c, "warning"); issue != nil {
		t.Fatalf("using one approved synonym must satisfy the concept, got: %s", issue.Message)
	}
	// Neither synonym present → still flags.
	if issue := CheckTerminology("Reset your password", "Setzen Sie Ihr Schlüssel zurück", c, "warning"); issue == nil {
		t.Fatal("expected issue: no approved synonym present")
	}
}

// The approved targets sum: "password password" is satisfied by one of each
// synonym (Passwort + Kennwort = 2 ≥ 2). A max-based rule would wrongly flag it.
func TestCheckTerminology_MixedSynonymSumSatisfies(t *testing.T) {
	c := []ConceptForQA{approvedConcept("password", "Passwort", "Kennwort")}
	src := "Enter your password, then confirm the password."
	tgt := "Geben Sie Ihr Passwort ein, bestätigen Sie dann das Kennwort."
	if issue := CheckTerminology(src, tgt, c, "warning"); issue != nil {
		t.Fatalf("2 approved synonyms cover 2 source occurrences, got: %s", issue.Message)
	}
}

// srcCount is the MAX over source synonyms; srcRepr is that synonym.
func TestCheckTerminology_MultiSourceTermMaxTrigger(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"table", "grid"}, ApprovedTargets: []string{"Tabelle"}}}
	if issue := CheckTerminology("The grid layout.", "Das Layout.", c, "warning"); issue == nil {
		t.Fatal("expected issue: 'grid' triggers the concept, 'Tabelle' absent")
	} else if !strings.Contains(issue.Message, "grid") {
		t.Errorf("message should name the triggering source synonym 'grid', got: %s", issue.Message)
	}
	if issue := CheckTerminology("The grid layout.", "Das Tabelle-Layout.", c, "warning"); issue != nil {
		t.Fatalf("expected no issue: approved target present, got: %s", issue.Message)
	}
}

// Occurrence count. Source has 2× "table", target has only
// 1× "Tabelle" → must flag even though the term IS present once.
func TestCheckTerminology_UndercountFlags(t *testing.T) {
	c := []ConceptForQA{approvedConcept("table", "Tabelle")}
	src := "The provided table identifier is invalid. Please provide a valid table WID."
	tgt := "Die angegebene Tabelle ist ungültig. Bitte geben Sie eine gültige Tabell-WID an."
	issue := CheckTerminology(src, tgt, c, "warning")
	if issue == nil {
		t.Fatal("expected issue: 2 source occurrences, only 1 target occurrence")
	}
}

func TestCheckTerminology_CountSatisfied(t *testing.T) {
	c := []ConceptForQA{approvedConcept("table", "Tabelle")}
	src := "The table and the table."
	tgt := "Die Tabelle und die Tabelle."
	if issue := CheckTerminology(src, tgt, c, "warning"); issue != nil {
		t.Fatalf("expected no issue, both counts equal: %s", issue.Message)
	}
}

// Word boundaries. "table" inside German compound
// "Tabellerkennung" must NOT count as a correct translation of "Tabelle".
func TestCheckTerminology_WordBoundaryNoSubwordMatch(t *testing.T) {
	c := []ConceptForQA{approvedConcept("table", "Tabelle")}
	issue := CheckTerminology("The table.", "Die Tabellerkennung.", c, "warning")
	if issue == nil {
		t.Fatal("expected issue: 'Tabelle' is not a whole word in 'Tabellerkennung'")
	}
}

// The loose DB ILIKE pre-filter can hand us a term whose source term is only a
// substring ("cat" from "category") — skip it, don't false-positive.
func TestCheckTerminology_SourceSubwordSkipped(t *testing.T) {
	c := []ConceptForQA{approvedConcept("cat", "Katze")}
	issue := CheckTerminology("Choose a category.", "Wählen Sie eine Kategorie.", c, "warning")
	if issue != nil {
		t.Fatalf("expected no issue: 'cat' only appears inside 'category': %s", issue.Message)
	}
}

// A concept with only forbidden targets (no approved) is not enforced by the
// approved check — it must not raise a "term not used" violation.
func TestCheckTerminology_ForbiddenOnlyConceptNotEnforced(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"pin"}, ForbiddenTargets: []string{"clavija"}}}
	if issue := CheckTerminology("Insert the pin.", "Introduzca el pasador.", c, "warning"); issue != nil {
		t.Fatalf("forbidden-only concept must not raise a terminology violation, got: %s", issue.Message)
	}
}

// --- CheckForbiddenTerms (concept model) ---

func TestCheckForbiddenTerms_Found(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"email"}, ForbiddenTargets: []string{"Elektronische Post"}}}
	issue := CheckForbiddenTerms("Bitte senden Sie eine Elektronische Post", c, "error")
	if issue == nil {
		t.Fatal("expected an issue for forbidden term")
	}
}

func TestCheckForbiddenTerms_NotFound(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"email"}, ForbiddenTargets: []string{"Elektronische Post"}}}
	issue := CheckForbiddenTerms("Bitte senden Sie eine E-Mail", c, "error")
	if issue != nil {
		t.Errorf("expected no issue, got %+v", issue)
	}
}

// Every forbidden synonym independently fails if present (opposite aggregation
// from approved targets).
func TestCheckForbiddenTerms_MultipleSynonyms(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"pin"}, ForbiddenTargets: []string{"clavija", "cabezal"}}}
	issue := CheckForbiddenTerms("Use el cabezal aquí.", c, "error")
	if issue == nil {
		t.Fatal("expected an issue: 'cabezal' is a forbidden synonym present in the target")
	}
}

// A forbidden term embedded as a subword must not trip the
// check ("Post" inside "Poster" is not the forbidden standalone word).
func TestCheckForbiddenTerms_WordBoundary(t *testing.T) {
	c := []ConceptForQA{{ConceptID: 1, SourceTerms: []string{"mail"}, ForbiddenTargets: []string{"Post"}}}
	issue := CheckForbiddenTerms("Ein schönes Poster.", c, "error")
	if issue != nil {
		t.Errorf("expected no issue: 'Post' only appears inside 'Poster', got %+v", issue)
	}
}

// --- GroupConcepts ---

func TestGroupConcepts(t *testing.T) {
	// Two concepts; concept 1 has a source synonym pair and approved+forbidden
	// targets + a pending target (dropped); the cross-join repeats rows.
	rows := []TermRow{
		{ConceptID: 1, Domain: "sw", SourceTerm: "pin", TargetTerm: "pin", Status: "approved"},
		{ConceptID: 1, Domain: "sw", SourceTerm: "pin", TargetTerm: "clavija", Status: "forbidden"},
		{ConceptID: 1, Domain: "sw", SourceTerm: "contact pin", TargetTerm: "pin", Status: "approved"},
		{ConceptID: 1, Domain: "sw", SourceTerm: "pin", TargetTerm: "aguja", Status: "pending"},
		{ConceptID: 2, Domain: "", SourceTerm: "cable", TargetTerm: "cable", Status: "approved"},
	}
	got := GroupConcepts(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 concepts, got %d", len(got))
	}
	c1 := got[0]
	if len(c1.SourceTerms) != 2 || c1.SourceTerms[0] != "pin" || c1.SourceTerms[1] != "contact pin" {
		t.Errorf("concept 1 source synonyms wrong: %v", c1.SourceTerms)
	}
	if len(c1.ApprovedTargets) != 1 || c1.ApprovedTargets[0] != "pin" {
		t.Errorf("approved targets should dedupe to [pin]: %v", c1.ApprovedTargets)
	}
	if len(c1.ForbiddenTargets) != 1 || c1.ForbiddenTargets[0] != "clavija" {
		t.Errorf("forbidden targets wrong: %v", c1.ForbiddenTargets)
	}
	if c1.Domain != "sw" {
		t.Errorf("domain not carried: %q", c1.Domain)
	}
	// pending 'aguja' must be dropped from both lists.
	for _, ta := range append(c1.ApprovedTargets, c1.ForbiddenTargets...) {
		if ta == "aguja" {
			t.Error("pending target 'aguja' must be dropped")
		}
	}
}

// A term that's forbidden IN THE SOURCE LANGUAGE (source_status="forbidden") is a
// discouraged input phrasing. "Forbidden" constrains output, so it's inert when
// that language is the source: it must not become an enforceable source term, or
// the segment would be flagged for "not using" the approved target even though the
// translator can't change the source. Only approved/pending source synonyms enforce.
func TestGroupConcepts_SourceForbiddenTermNotEnforceable(t *testing.T) {
	rows := []TermRow{
		{ConceptID: 1, SourceTerm: "all rights reserved", TargetTerm: "Alle Rechte vorbehalten", Status: "approved", SourceStatus: "approved"},
		{ConceptID: 1, SourceTerm: "all rights are reserved", TargetTerm: "Alle Rechte vorbehalten", Status: "approved", SourceStatus: "forbidden"},
	}
	got := GroupConcepts(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(got))
	}
	c := got[0]
	// Only the approved source variant is enforceable; the forbidden one is dropped.
	if len(c.SourceTerms) != 1 || c.SourceTerms[0] != "all rights reserved" {
		t.Errorf("source-forbidden term must be excluded from SourceTerms, got %v", c.SourceTerms)
	}
	// The approved target still stands (the concept is still valid).
	if len(c.ApprovedTargets) != 1 || c.ApprovedTargets[0] != "Alle Rechte vorbehalten" {
		t.Errorf("approved target should remain: %v", c.ApprovedTargets)
	}
}

// End-to-end of the same case: a source segment that only matches via the
// source-forbidden variant must NOT raise a terminology violation, even when the
// approved target is only paraphrased in the translation.
func TestCheckTerminology_SourceForbiddenVariantNotFlagged(t *testing.T) {
	rows := []TermRow{
		{ConceptID: 1, SourceTerm: "all rights reserved", TargetTerm: "Alle Rechte vorbehalten", Status: "approved", SourceStatus: "approved"},
		{ConceptID: 1, SourceTerm: "all rights are reserved", TargetTerm: "Alle Rechte vorbehalten", Status: "approved", SourceStatus: "forbidden"},
	}
	concepts := GroupConcepts(rows)
	// Source uses the forbidden variant ("are"); MT paraphrases the target ("sind").
	if issue := CheckTerminology("All rights are reserved", "Alle Rechte sind vorbehalten", concepts, "error"); issue != nil {
		t.Fatalf("source-forbidden-only match must not raise a terminology violation, got: %s", issue.Message)
	}
}

// An apostrophe-glued fragment ("company's" → bare "s", "don't" → "don") is not
// a whole-word occurrence — the tokenizer's split must not resurrect single-
// letter/partial term matches. A genuine standalone symbol still counts. Mirrors
// the TS isWholeWordMatch tests in web/src/lib/term-match.test.ts.
func TestCountWordOccurrences_ApostropheGlue(t *testing.T) {
	cases := []struct {
		haystack, needle string
		want             int
	}{
		{"a company's quality", "s", 0}, // possessive fragment
		{"I don't know", "don", 0},      // contraction fragment
		{"wait 5 s now", "s", 1},        // standalone unit symbol
		{"the 's' key", "s", 1},         // quoted standalone letter
	}
	for _, c := range cases {
		if got := countWordOccurrences(c.haystack, c.needle); got != c.want {
			t.Errorf("countWordOccurrences(%q, %q) = %d, want %d", c.haystack, c.needle, got, c.want)
		}
	}
}

// --- CheckPunctuation ---

func TestCheckPunctuation_Matching(t *testing.T) {
	issue := CheckPunctuation("Hello world.", "Hola mundo.", "warning")
	if issue != nil {
		t.Fatalf("expected no issue, got: %s", issue.Message)
	}
}

func TestCheckPunctuation_MissingTrailing(t *testing.T) {
	issue := CheckPunctuation("Hello world.", "Hola mundo", "warning")
	if issue == nil {
		t.Fatal("expected issue for missing trailing period")
	}
}

// --- CheckConsistency ---

func TestCheckConsistency_Consistent(t *testing.T) {
	issue := CheckConsistency("Hola mundo", []string{"Hola mundo"}, "warning")
	if issue != nil {
		t.Fatalf("expected no issue, got: %s", issue.Message)
	}
}

func TestCheckConsistency_Inconsistent(t *testing.T) {
	issue := CheckConsistency("Hola mundo", []string{"Hallo Welt"}, "warning")
	if issue == nil {
		t.Fatal("expected issue for inconsistent translation")
	}
}

// --- CheckAll ---

func TestCheckAll_RunsEnabledChecks(t *testing.T) {
	rules := map[string]string{
		"empty_target":       "error",
		"number_consistency": "error",
		"punctuation":        "warning",
	}
	issues := CheckAll(t.Context(), "There are 5 items.", "", rules, nil)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (empty target), got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "empty_target" {
		t.Fatalf("expected empty_target, got %s", issues[0].CheckType)
	}
}

func TestCheckAll_TagChecksViaCheckAll(t *testing.T) {
	rules := map[string]string{"tag_checks": "error"}
	issues := CheckAll(t.Context(), "<b>Hello</b>", "Hola", rules, nil)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "tag_missing" {
		t.Fatalf("expected tag_missing, got %s", issues[0].CheckType)
	}
}

func TestCheckAllWithParams_TagChecksWithConfig(t *testing.T) {
	issues := CheckAllWithParams(t.Context(), CheckAllParams{
		Source: "<b>Hello</b>",
		Target: "Hola",
		Rules:  map[string]string{"tag_checks": "error"},
		RuleConfigs: map[string][]byte{
			"tag_checks": []byte(`{"missing":false,"extra":true,"unclosed":true,"unopened":true}`),
		},
	})
	for _, i := range issues {
		if i.CheckType == "tag_missing" {
			t.Error("tag_missing should be disabled by config")
		}
	}
}

func TestCheckAllWithParams_RichTextUsed(t *testing.T) {
	// Plain text has no tags, rich text has XLIFF tags
	issues := CheckAllWithParams(t.Context(), CheckAllParams{
		Source:         "Click here",
		Target:         "Haga clic aquí",
		SourceRichText: `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>`,
		TargetRichText: `Haga clic <bpt id="1"><b></bpt>aquí`,
		Rules:          map[string]string{"tag_checks": "error"},
	})
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue from rich text, got %d: %+v", len(issues), issues)
	}
	if issues[0].CheckType != "tag_missing" {
		t.Fatalf("expected tag_missing, got %s", issues[0].CheckType)
	}
}

func TestCheckAllWithParams_DraftWithRemovedTag(t *testing.T) {
	// Scenario: user edited segment and removed a closing tag.
	// Draft saved human_rich_text with the tag removed.
	// QA should detect the missing tag using human_rich_text, not fall back to active_rich_text.
	src := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue.`
	tgtRich := `Haga clic <bpt id="1"><b></bpt>aquí para continuar.` // ept removed

	issues := CheckAllWithParams(t.Context(), CheckAllParams{
		Source:         "Click here to continue.",
		Target:         "Haga clic aquí para continuar.",
		SourceRichText: src,
		TargetRichText: tgtRich, // human_rich_text with tag removed
		Rules:          map[string]string{"tag_checks": "error"},
	})
	if len(issues) == 0 {
		t.Fatal("expected tag_missing issue when human_rich_text has removed tag")
	}
	if issues[0].CheckType != "tag_missing" {
		t.Fatalf("expected tag_missing, got %s: %s", issues[0].CheckType, issues[0].Message)
	}
}

// --- CheckTargetIdenticalToSource ---
// An empty confirm copies the source into the target, so a segment can look
// translated while it is not; this check flags target == source.

func TestCheckTargetIdenticalToSource_Identical(t *testing.T) {
	issue := CheckTargetIdenticalToSource("Hello world", "Hello world", "warning")
	if issue == nil {
		t.Fatal("expected issue when target == source")
	}
	if issue.CheckType != "target_identical_to_source" {
		t.Errorf("check_type = %q, want 'target_identical_to_source'", issue.CheckType)
	}
}

func TestCheckTargetIdenticalToSource_Different(t *testing.T) {
	issue := CheckTargetIdenticalToSource("Hello world", "Hola mundo", "warning")
	if issue != nil {
		t.Errorf("expected no issue when target != source, got: %v", issue)
	}
}

func TestCheckTargetIdenticalToSource_CaseInsensitive(t *testing.T) {
	// "HELLO WORLD" vs "hello world" — should still flag (same content, just casing)
	issue := CheckTargetIdenticalToSource("Hello World", "hello world", "warning")
	if issue == nil {
		t.Fatal("expected issue when target == source (case-insensitive)")
	}
}

func TestCheckTargetIdenticalToSource_EmptySkipped(t *testing.T) {
	// Empty target is caught by empty_target, not this check
	issue := CheckTargetIdenticalToSource("Hello", "", "warning")
	if issue != nil {
		t.Errorf("expected no issue for empty target (that's empty_target's job), got: %v", issue)
	}
}

func TestCheckAllWithParams_TargetIdenticalToSourceWired(t *testing.T) {
	// Verify the check is wired into CheckAllWithParams
	issues := CheckAllWithParams(t.Context(), CheckAllParams{
		Source: "Reset your password.",
		Target: "Reset your password.", // identical — should flag
		Rules:  map[string]string{"target_identical_to_source": "warning"},
	})
	found := false
	for _, i := range issues {
		if i.CheckType == "target_identical_to_source" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CheckAllWithParams should detect target==source, got issues: %v", issues)
	}
}

// Pins that tags are identified by `(kind, id)`: `<ph id="1" x="ENTER"/>` and
// `<ph id="1"/>` are the SAME tag with different metadata, so neither
// tag_missing nor tag_extra fires.
func TestCheckTagsDetailed_SameIdDifferentAttrs(t *testing.T) {
	src := `Press <ph id="1" x="ENTER"/> to submit`
	tgt := `Stiskněte <ph id="1"/> pro odeslání` // user typed the tag back in, attrs slightly different
	issues := CheckTagsDetailed(src, tgt, "error", allTagChecks())

	// Anti-tautology: a raw-string compare reports BOTH missing and extra here.
	types := issueTypes(issues)
	if types["tag_missing"] {
		t.Errorf("tag_missing reported for ph#1 — but target HAS ph#1 (just with different attrs); raw-string compare bug")
	}
	if types["tag_extra"] {
		t.Errorf("tag_extra reported for ph#1 — same ID is the same tag, not extra; raw-string compare bug")
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for same-ID/different-attrs case, got %d: %+v", len(issues), issues)
	}
}

// Pins the same contract through the full QA entry point (CheckAllWithParams):
// the source has `<ph id="1" x="ENTER"/>`, the user types `<ph id="1"/>` into
// the target, and QA reports no tag_missing / tag_extra for ph#1 — whether the
// typed tag landed in target_rich_text or stayed in the plain target.
// The unit test above covers only CheckTagsDetailed; this
// one also covers the rich-text fallback and rule-enabling layers.
func TestCheckAll_TypedTagInTarget_NoFalsePositive(t *testing.T) {
	rules := map[string]string{"tag_checks": "error"}

	cases := []struct {
		name       string
		sourceRich string
		targetRich string
		target     string // plain target (used when targetRich is "")
	}{
		{
			name:       "typed tag round-tripped into target_rich_text",
			sourceRich: `Press <ph id="1" x="ENTER"/> to submit`,
			targetRich: `Stiskněte <ph id="1"/> pro odeslání`, // user's typed form
			target:     `Stiskněte <ph id="1"/> pro odeslání`,
		},
		{
			name:       "typed tag stayed in plain target (target_rich_text empty)",
			sourceRich: `Press <ph id="1" x="ENTER"/> to submit`,
			targetRich: "",
			target:     `Stiskněte <ph id="1"/> pro odeslání`, // plain text fallback
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := CheckAllWithParams(t.Context(), CheckAllParams{
				Source:         "Press  to submit",
				Target:         c.target,
				SourceRichText: c.sourceRich,
				TargetRichText: c.targetRich,
				Rules:          rules,
				RuleConfigs:    map[string][]byte{},
			})
			for _, iss := range issues {
				if iss.CheckType == "tag_missing" || iss.CheckType == "tag_extra" {
					t.Errorf("user flow regression: unexpected %s issue (%s); user added the tag in target with same id, no false-positive expected",
						iss.CheckType, iss.Message)
				}
			}
		})
	}
}

// --- helpers ---

func issueTypes(issues []Issue) map[string]bool {
	m := make(map[string]bool)
	for _, i := range issues {
		m[i.CheckType] = true
	}
	return m
}

// TestTagChecks_TMXPairingAttributes — TMX 1.4 pairs inline tags with `i`, not
// `id`, and an SDLX export carries no `id` at all. Pins that such markup is
// still parsed as tags, so a 100% TM match reports no phantom tag mismatch.
// The markup keeps the SDLX export's tag shape.
func TestTagChecks_TMXPairingAttributes(t *testing.T) {
	const tmxRich = `Store the samples below 5<bpt i="1" x="1">&lt;1&gt;</bpt>°C<ept i="1">&lt;/1&gt;</ept> overnight.`

	t.Run("both halves of a pair display the same number", func(t *testing.T) {
		// Membership, not position: displayTags sorts, and which of <1> / </1>
		// comes first says nothing about the pairing this test is about.
		tags := displayTags(extractTags(tmxRich))
		seen := map[string]bool{}
		for _, tg := range tags {
			seen[tg] = true
		}
		if !seen["<1>"] || !seen["</1>"] {
			t.Errorf("want both <1> and </1>, got %v", tags)
		}
	})

	t.Run("identical TMX markup on both sides reports no mismatch", func(t *testing.T) {
		issues := CheckTagsDetailed(tmxRich, tmxRich, "error", DefaultTagChecksConfig())
		if len(issues) != 0 {
			t.Errorf("identical tags must not report issues, got %+v", issues)
		}
	})

	t.Run("a genuinely missing TMX tag is still caught", func(t *testing.T) {
		// Anti-tautology: loosening the patterns must not blind the check.
		issues := CheckTagsDetailed(tmxRich, "Lagern Sie die Proben über Nacht unter 5°C.", "error", DefaultTagChecksConfig())
		if len(issues) == 0 {
			t.Error("dropping both tags should report a mismatch")
		}
	})

	t.Run("XLIFF rid pairing also resolves", func(t *testing.T) {
		const xliffRich = `Click <bpt id="1" rid="1">&lt;b&gt;</bpt>here<ept id="2" rid="1">&lt;/b&gt;</ept>.`
		issues := CheckTagsDetailed(xliffRich, xliffRich, "error", DefaultTagChecksConfig())
		if len(issues) != 0 {
			t.Errorf("identical rid-paired tags must not report issues, got %+v", issues)
		}
	})
}

// --- Message stability ---

// A finding's message is its identity: the per-finding ignore
// survives a re-run only while `qa_results.message` is unchanged. Every check
// that lists several items must therefore emit them in a fixed order, or an
// ignored finding un-ignores itself on the next run with nobody touching the
// text. The tag checks collect their items from maps, whose iteration order Go
// randomises, so the sort in displayTags is what makes them stable.
//
// The loop is what gives the test teeth: one pass would agree with an unsorted
// implementation whenever the map happened to come out in order.
func TestTagMessagesAreStableAcrossRuns(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		target    string
		cfg       TagChecksConfig
		checkType string
		want      string
	}{
		{
			name:      "missing",
			source:    `<x id="1"/> a <x id="2"/> b <x id="3"/> c <x id="4"/>`,
			target:    `a b c`,
			cfg:       TagChecksConfig{Missing: true},
			checkType: "tag_missing",
			want:      "Source tag(s) missing from target: {1}, {2}, {3}, {4}",
		},
		{
			name:      "extra",
			source:    `a b c`,
			target:    `<x id="1"/> a <x id="2"/> b <x id="3"/> c <x id="4"/>`,
			cfg:       TagChecksConfig{Extra: true},
			checkType: "tag_extra",
			want:      "Target has extra tag(s) not in source: {1}, {2}, {3}, {4}",
		},
		{
			name:      "unclosed",
			source:    `<b>a</b> <i>b</i> <u>c</u> <s>d</s>`,
			target:    `<b>a <i>b <u>c <s>d`,
			cfg:       TagChecksConfig{Unclosed: true},
			checkType: "tag_unclosed",
			want:      "Opening tag(s) without closing tag: <b>, <i>, <s>, <u>",
		},
		{
			name:      "unopened",
			source:    `<b>a</b> <i>b</i> <u>c</u> <s>d</s>`,
			target:    `a</b> b</i> c</u> d</s>`,
			cfg:       TagChecksConfig{Unopened: true},
			checkType: "tag_unopened",
			want:      "Closing tag(s) without opening tag: </b>, </i>, </s>, </u>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 50; i++ {
				var got string
				for _, issue := range CheckTagsDetailed(tc.source, tc.target, "error", tc.cfg) {
					if issue.CheckType == tc.checkType {
						got = issue.Message
					}
				}
				if got != tc.want {
					t.Fatalf("run %d: message is not stable\n got: %q\nwant: %q", i, got, tc.want)
				}
			}
		})
	}
}
