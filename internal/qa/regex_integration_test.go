package qa_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/testutil"
)

// nbspRule flags a number followed by a regular space and a unit, where French
// typography wants a non-breaking space.
func nbspRule(message string, langs ...string) map[string]any {
	if langs == nil {
		langs = []string{}
	}
	return map[string]any{
		"check_type": "regex", "severity": "error",
		"config": map[string]any{
			"name": "Number + unit", "mode": qa.RegexModeTargetMustNotMatch,
			"pattern": `\d (kg|cm|%)`, "message": message, "target_languages": langs,
		},
	}
}

type ruleResp struct {
	ID        int64           `json:"id"`
	CheckType string          `json:"check_type"`
	Severity  string          `json:"severity"`
	Config    json.RawMessage `json:"config"`
}

type regexFixture struct {
	router            chi.Router
	q                 db.Querier
	user, tenant      int64
	profileID         int64
	projectID, fileID int64
}

func newRegexFixture(t *testing.T, prefix, targetLang string) (regexFixture, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	f := regexFixture{router: testutil.NewTestRouter(pool), q: q}
	f.user, f.tenant = testutil.RegisterUser(t, q, prefix)

	rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles", map[string]string{"name": "Regex profile"}, f.user, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var prof struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &prof)
	f.profileID = prof.Data.ID

	rec = testutil.DoRequest(t, f.router, "POST", "/api/projects", map[string]any{
		"name": "Regex project", "source_language": "en", "target_languages": []string{targetLang},
		"workflow_id": testutil.LibraryWorkflow(t, q, f.tenant),
	}, f.user, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &prof)
	f.projectID = prof.Data.ID
	rec = testutil.DoRequest(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", f.projectID),
		map[string]any{"qa_profile_id": f.profileID}, f.user, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
	f.fileID = testutil.SeedFile(t, q, f.projectID, "regex.txt", db.FileFormatTxt).ID
	return f, cleanup
}

func (f regexFixture) postRule(t *testing.T, body map[string]any) (ruleResp, int, string) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", f.profileID), body, f.user, f.tenant)
	var resp struct {
		Data  ruleResp `json:"data"`
		Error string   `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Data, rec.Code, resp.Error + rec.Body.String()
}

func (f regexFixture) runSegment(t *testing.T, translationID int64) []finding {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/translations/%d/qa", translationID), nil, f.user, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []finding `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}

func (f regexFixture) segmentWithTarget(t *testing.T, lang, source, target string, pos int32) int64 {
	t.Helper()
	seg := testutil.SeedSegment(t, f.q, f.fileID, source, pos)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, lang, db.TranslationStatusNeedsReview)
	if _, err := f.q.UpdateTranslationHuman(context.Background(), db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &target}); err != nil {
		t.Fatalf("set target: %v", err)
	}
	return tr.ID
}

// TestIntegration_RegexRules pins regex rules end to end over HTTP: several
// per profile, each finding under its own check type with the rule's message,
// filtered by target language, blocking confirm at error severity, and
// ignorable through the route the editor calls.
func TestIntegration_RegexRules(t *testing.T) {
	f, cleanup := newRegexFixture(t, "qarx", "fr-FR")
	defer cleanup()

	nbsp, code, body := f.postRule(t, nbspRule("Use a non-breaking space before the unit", "fr"))
	if code != http.StatusOK {
		t.Fatalf("create rule: %d %s", code, body)
	}
	other, code, body := f.postRule(t, map[string]any{
		"check_type": "regex", "severity": "warning",
		"config": map[string]any{
			"name": "No double space", "mode": qa.RegexModeTargetMustNotMatch,
			"pattern": "  ", "message": "Double space", "target_languages": []string{},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create second rule: %d %s", code, body)
	}
	esOnly, code, body := f.postRule(t, nbspRule("Spanish only", "es"))
	if code != http.StatusOK {
		t.Fatalf("create es rule: %d %s", code, body)
	}

	trID := f.segmentWithTarget(t, "fr-FR", "It weighs 5 kg  now", "Il pèse 5 kg  maintenant", 1)

	t.Run("EachRuleFindsUnderItsOwnCheckType", func(t *testing.T) {
		rows := f.runSegment(t, trID)
		a := findCheck(rows, qa.RegexFindingCheckType(nbsp.ID))
		b := findCheck(rows, qa.RegexFindingCheckType(other.ID))
		if a == nil || a.Message != "Use a non-breaking space before the unit" || a.Severity != "error" {
			t.Fatalf("nbsp finding wrong: %+v", rows)
		}
		if b == nil || b.Message != "Double space" || b.Severity != "warning" {
			t.Fatalf("double-space finding wrong: %+v", rows)
		}
		if findCheck(rows, qa.RegexFindingCheckType(esOnly.ID)) != nil {
			t.Errorf("an es-only rule fired on fr-FR: %+v", rows)
		}
	})

	t.Run("ErrorBlocksConfirmUntilIgnored", func(t *testing.T) {
		confirm := func() int {
			return testutil.DoRequest(t, f.router, "PUT",
				fmt.Sprintf("/api/files/%d/translations/%d", f.fileID, trID),
				map[string]any{"human_text": "Il pèse 5 kg  maintenant", "editing_time_ms": 0}, f.user, f.tenant).Code
		}
		if c := confirm(); c != http.StatusUnprocessableEntity {
			t.Fatalf("confirm with a regex error: want 422, got %d", c)
		}
		// The editor sends the check type through encodeURIComponent.
		path := fmt.Sprintf("/api/translations/%d/qa/%s/ignore", trID, strings.ReplaceAll(qa.RegexFindingCheckType(nbsp.ID), ":", "%3A"))
		rec := testutil.DoRequest(t, f.router, "POST", path, nil, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if c := confirm(); c != http.StatusOK {
			t.Fatalf("confirm after ignoring: want 200, got %d", c)
		}
		rows := f.runSegment(t, trID)
		if r := findCheck(rows, qa.RegexFindingCheckType(nbsp.ID)); r == nil || !r.Ignored {
			t.Errorf("ignore did not survive a re-run: %+v", rows)
		}
	})

	t.Run("NonBreakingSpacePasses", func(t *testing.T) {
		ok1 := f.segmentWithTarget(t, "fr-FR", "5 kg", "5\u00a0kg", 2)
		ok2 := f.segmentWithTarget(t, "fr-FR", "5 %", "5\u202f%", 3)
		for _, id := range []int64{ok1, ok2} {
			if r := findCheck(f.runSegment(t, id), qa.RegexFindingCheckType(nbsp.ID)); r != nil {
				t.Errorf("translation %d: special space flagged: %+v", id, r)
			}
		}
	})

	t.Run("UpdateByRuleIDKeepsIdentity", func(t *testing.T) {
		body := nbspRule("Edited message", "fr")
		body["rule_id"] = nbsp.ID
		got, code, msg := f.postRule(t, body)
		if code != http.StatusOK || got.ID != nbsp.ID {
			t.Fatalf("update: %d %s (id %d, want %d)", code, msg, got.ID, nbsp.ID)
		}
		var cfg qa.RegexRuleConfig
		if err := json.Unmarshal(got.Config, &cfg); err != nil || cfg.Message != "Edited message" {
			t.Fatalf("config not updated: %s", got.Config)
		}
		rows, err := f.q.ListQARules(context.Background(), f.profileID)
		if err != nil || len(rows) != 3 {
			t.Fatalf("want 3 rules after an update, got %d (%v)", len(rows), err)
		}
	})

	t.Run("DisabledRuleDoesNotFire", func(t *testing.T) {
		if findCheck(f.runSegment(t, trID), qa.RegexFindingCheckType(other.ID)) == nil {
			t.Fatal("precondition: the double-space rule fires while enabled")
		}
		body := map[string]any{
			"rule_id": other.ID, "check_type": "regex", "enabled": false, "severity": "warning",
			"config": map[string]any{
				"name": "No double space", "mode": qa.RegexModeTargetMustNotMatch,
				"pattern": "  ", "message": "Double space", "target_languages": []string{},
			},
		}
		if _, code, msg := f.postRule(t, body); code != http.StatusOK {
			t.Fatalf("disable: %d %s", code, msg)
		}
		if r := findCheck(f.runSegment(t, trID), qa.RegexFindingCheckType(other.ID)); r != nil {
			t.Errorf("a disabled regex rule fired: %+v", r)
		}
	})

	t.Run("DeleteRegexRuleOnly", func(t *testing.T) {
		del := func(id int64) int {
			return testutil.DoRequest(t, f.router, "DELETE", fmt.Sprintf("/api/qa-profiles/%d/rules/%d", f.profileID, id), nil, f.user, f.tenant).Code
		}
		if c := del(esOnly.ID); c != http.StatusNoContent {
			t.Fatalf("delete regex rule: %d", c)
		}
		if c := del(esOnly.ID); c != http.StatusNotFound {
			t.Errorf("second delete: want 404, got %d", c)
		}
		builtin, code, msg := f.postRule(t, map[string]any{"check_type": "punctuation"})
		if code != http.StatusOK {
			t.Fatalf("create built-in: %d %s", code, msg)
		}
		if c := del(builtin.ID); c != http.StatusNotFound {
			t.Errorf("deleting a built-in rule: want 404, got %d", c)
		}
	})
}

// TestIntegration_RegexRuleValidation pins the save-time checks: bad syntax,
// empty match, missing fields and the per-profile cap are 400s with a message
// naming the problem, and nothing is written.
func TestIntegration_RegexRuleValidation(t *testing.T) {
	f, cleanup := newRegexFixture(t, "qarxv", "fr")
	defer cleanup()

	bad := func(mutate func(cfg map[string]any), want string) {
		t.Helper()
		body := nbspRule("msg")
		mutate(body["config"].(map[string]any))
		_, code, msg := f.postRule(t, body)
		if code != http.StatusBadRequest || !strings.Contains(msg, want) {
			t.Errorf("want 400 containing %q, got %d %s", want, code, msg)
		}
	}
	bad(func(c map[string]any) { c["pattern"] = `\d (kg` }, "missing closing )")
	bad(func(c map[string]any) { c["pattern"] = `a*` }, "matches empty text")
	bad(func(c map[string]any) { c["pattern"] = strings.Repeat("a", 1001) }, "at most 1000")
	bad(func(c map[string]any) { c["message"] = "" }, "message is required")
	bad(func(c map[string]any) { c["mode"] = "sometimes" }, "mode must be one of")
	bad(func(c map[string]any) { c["mode"] = qa.RegexModeSourceMatchTargetNot }, "source_pattern is required")
	bad(func(c map[string]any) { c["surprise"] = true }, "unknown field")

	_, code, msg := f.postRule(t, map[string]any{"check_type": "punctuation", "rule_id": 1})
	if code != http.StatusBadRequest || !strings.Contains(msg, "only for regex rules") {
		t.Errorf("rule_id on a built-in: %d %s", code, msg)
	}

	for i := 0; i < qa.MaxRegexRulesPerProfile; i++ {
		if _, code, msg := f.postRule(t, nbspRule(fmt.Sprintf("rule %d", i))); code != http.StatusOK {
			t.Fatalf("rule %d: %d %s", i, code, msg)
		}
	}
	if _, code, msg := f.postRule(t, nbspRule("one too many")); code != http.StatusBadRequest || !strings.Contains(msg, "at most 50") {
		t.Errorf("rule 51: want 400, got %d %s", code, msg)
	}
	n, err := f.q.CountQARegexRules(context.Background(), f.profileID)
	if err != nil || n != qa.MaxRegexRulesPerProfile {
		t.Errorf("want %d regex rules stored, got %d (%v)", qa.MaxRegexRulesPerProfile, n, err)
	}
}

// TestIntegration_ProfileExportImport pins the JSON round trip: special spaces
// come back byte for byte through export → import (new profile and replace),
// a name clash gets a suffix, and a bad rule leaves the target profile as it
// was.
func TestIntegration_ProfileExportImport(t *testing.T) {
	f, cleanup := newRegexFixture(t, "qarxio", "fr")
	defer cleanup()

	special := "Mettre une espace insécable\u00a0ou fine\u202fici"
	rule := nbspRule(special, "fr")
	rule["config"].(map[string]any)["pattern"] = "\\d[ \u00a0](kg|cm) <b>"
	if _, code, msg := f.postRule(t, rule); code != http.StatusOK {
		t.Fatalf("create rule: %d %s", code, msg)
	}
	if _, code, msg := f.postRule(t, map[string]any{"check_type": "tag_checks", "severity": "warning", "config": map[string]any{"missing": true, "extra": false, "unclosed": true, "unopened": true}}); code != http.StatusOK {
		t.Fatalf("create tag rule: %d %s", code, msg)
	}

	exportFile := func(profileID int64) []byte {
		t.Helper()
		rec := testutil.DoRequest(t, f.router, "GET", fmt.Sprintf("/api/qa-profiles/%d/export", profileID), nil, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("export is not an attachment: %q", cd)
		}
		return rec.Body.Bytes()
	}
	original := exportFile(f.profileID)
	for _, want := range []string{"\u00a0ou fine\u202fici", "[ \u00a0](kg|cm) <b>"} {
		if !bytes.Contains(original, []byte(want)) {
			t.Fatalf("export lost %q:\n%s", want, original)
		}
	}
	var file map[string]any
	if err := json.Unmarshal(original, &file); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}

	t.Run("ImportAsNewProfile", func(t *testing.T) {
		rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles/import", file, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Name != "Regex profile (2)" {
			t.Errorf("name clash: want %q, got %q", "Regex profile (2)", resp.Data.Name)
		}
		again := exportFile(resp.Data.ID)
		if !bytes.Equal(bytes.Replace(again, []byte("Regex profile (2)"), []byte("Regex profile"), 1), original) {
			t.Errorf("round trip changed the file:\n--- before\n%s\n--- after\n%s", original, again)
		}
	})

	t.Run("ReplaceRules", func(t *testing.T) {
		rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles", map[string]string{"name": "Target"}, f.user, f.tenant)
		var resp struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		target := resp.Data.ID
		rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", target), map[string]any{"check_type": "punctuation"}, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)

		rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/import", target), file, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)
		after := exportFile(target)
		if !bytes.Equal(bytes.Replace(after, []byte(`"Target"`), []byte(`"Regex profile"`), 1), original) {
			t.Errorf("replace did not copy the rules exactly:\n%s", after)
		}
	})

	t.Run("BadRuleRollsBack", func(t *testing.T) {
		broken := map[string]any{"format": "openloc-qa-profile", "format_version": 1, "name": "x", "rules": []any{
			map[string]any{"check_type": "empty_target", "enabled": true, "severity": "error", "config": map[string]any{}},
			map[string]any{"check_type": "regex", "enabled": true, "severity": "error", "config": map[string]any{"name": "n", "mode": "target_must_match", "pattern": "(", "message": "m"}},
		}}
		rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/import", f.profileID), broken, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if !bytes.Equal(exportFile(f.profileID), original) {
			t.Error("a rejected import changed the profile")
		}

		// 51 regex rules are one more than a profile holds: the whole file is
		// refused.
		many := make([]any, 0, qa.MaxRegexRulesPerProfile+1)
		for i := 0; i <= qa.MaxRegexRulesPerProfile; i++ {
			r := nbspRule(fmt.Sprintf("rule %d", i))
			r["enabled"] = true
			many = append(many, r)
		}
		tooMany := map[string]any{"format": "openloc-qa-profile", "format_version": 1, "name": "x", "rules": many}
		rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/import", f.profileID), tooMany, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
		if !bytes.Equal(exportFile(f.profileID), original) {
			t.Error("an import over the regex rule limit changed the profile")
		}
	})

	t.Run("WrongFormatRejected", func(t *testing.T) {
		rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles/import", map[string]any{"format": "openloc-qa-profile", "format_version": 2, "name": "x"}, f.user, f.tenant)
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	})
}

// TestIntegration_RegexRulesTenantIsolation pins that every new route answers
// 404 for another tenant's profile and changes nothing.
func TestIntegration_RegexRulesTenantIsolation(t *testing.T) {
	f, cleanup := newRegexFixture(t, "qarxa", "fr")
	defer cleanup()
	rule, code, msg := f.postRule(t, nbspRule("A's rule"))
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, msg)
	}
	userB, tenantB := testutil.RegisterUser(t, f.q, "qarxb")

	update := nbspRule("hijacked")
	update["rule_id"] = rule.ID
	file := map[string]any{"format": "openloc-qa-profile", "format_version": 1, "name": "x", "rules": []any{}}
	cases := []struct {
		method, path string
		body         any
	}{
		{"POST", fmt.Sprintf("/api/qa-profiles/%d/rules", f.profileID), update},
		{"DELETE", fmt.Sprintf("/api/qa-profiles/%d/rules/%d", f.profileID, rule.ID), nil},
		{"GET", fmt.Sprintf("/api/qa-profiles/%d/export", f.profileID), nil},
		{"POST", fmt.Sprintf("/api/qa-profiles/%d/import", f.profileID), file},
	}
	for _, c := range cases {
		rec := testutil.DoRequest(t, f.router, c.method, c.path, c.body, userB, tenantB)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s as another tenant: want 404, got %d", c.method, c.path, rec.Code)
		}
	}

	// A rule id of tenant A under tenant B's own profile is not found either.
	rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles", map[string]string{"name": "B"}, userB, tenantB)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", resp.Data.ID), update, userB, tenantB)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequest(t, f.router, "DELETE", fmt.Sprintf("/api/qa-profiles/%d/rules/%d", resp.Data.ID, rule.ID), nil, userB, tenantB)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	rules, err := f.q.ListQARules(context.Background(), f.profileID)
	if err != nil || len(rules) != 1 || !bytes.Contains(rules[0].Config, []byte("A's rule")) {
		t.Errorf("tenant A's rule changed: %+v (%v)", rules, err)
	}
}
