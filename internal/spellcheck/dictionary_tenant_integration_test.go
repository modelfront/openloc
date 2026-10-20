package spellcheck_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Another org's project and dictionary entry. Both writes check the project
// belongs to the caller; the delete then matches by entry id, tenant AND
// project, so naming the entry under the caller's OWN project matches nothing
// and answers 404. The rows are the evidence.
func TestIntegrationSpellcheckDictionary_OtherTenantCannotWrite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	victimUser, victim := testutil.RegisterUser(t, q, "dict-victim")
	attackerUser, attacker := testutil.RegisterUser(t, q, "dict-attacker")
	victimProject := testutil.SeedProject(t, q, victim, "Victim Project", "en", []string{"de"})
	attackerProject := testutil.SeedProject(t, q, attacker, "Attacker Project", "en", []string{"de"})
	entry, err := q.AddSpellcheckDictionaryWord(ctx, db.AddSpellcheckDictionaryWordParams{
		TenantID: victim, ProjectID: victimProject.ID, Language: "de", Word: "Acmecorp", CreatedBy: &victimUser,
	})
	if err != nil {
		t.Fatalf("AddSpellcheckDictionaryWord: %v", err)
	}

	dictPath := func(projectID int64) string {
		return fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", projectID)
	}
	victimWords := func(t *testing.T) []string {
		t.Helper()
		rows, err := pool.Query(ctx,
			`SELECT word FROM spellcheck_dictionary WHERE project_id = $1 AND deleted_at IS NULL ORDER BY word`, victimProject.ID)
		if err != nil {
			t.Fatalf("query dictionary: %v", err)
		}
		words, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("collect dictionary: %v", err)
		}
		return words
	}
	entryPath := func(projectID int64) string {
		return fmt.Sprintf("%s/%d", dictPath(projectID), entry.ID)
	}
	entryExists := func(t *testing.T) bool {
		t.Helper()
		return testutil.CountRows(t, pool, `SELECT count(*) FROM spellcheck_dictionary WHERE id = $1 AND deleted_at IS NULL`, entry.ID) == 1
	}

	t.Run("POST to another org's project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPost, dictPath(victimProject.ID),
			map[string]any{"language": "de", "word": "Planted"}, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if got := victimWords(t); !slices.Equal(got, []string{"Acmecorp"}) {
			t.Errorf("another org's POST changed the dictionary: %v, want [Acmecorp]", got)
		}
	})

	t.Run("DELETE through another org's project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodDelete, entryPath(victimProject.ID), nil, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if !entryExists(t) {
			t.Error("another org's DELETE removed the entry")
		}
	})

	t.Run("DELETE another org's entry through own project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodDelete, entryPath(attackerProject.ID), nil, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		if !entryExists(t) {
			t.Error("another org's DELETE removed the entry")
		}
	})

	t.Run("own project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPost, dictPath(victimProject.ID),
			map[string]any{"language": "de", "word": "Nordwind"}, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		if got := victimWords(t); !slices.Contains(got, "Nordwind") {
			t.Errorf("owner's POST: words = %v, want Nordwind among them", got)
		}
		rec = testutil.DoRequest(t, router, http.MethodDelete, entryPath(victimProject.ID), nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusNoContent)
		if entryExists(t) {
			t.Error("owner's DELETE returned 204 but the entry is still there")
		}
	})
}

// Same org, two projects. The delete URL names a project, so a word is
// removed only through the project it belongs to; naming another project's
// word matches nothing and answers 404.
func TestIntegrationSpellcheckDictionary_DeleteMatchesTheProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	user, tenant := testutil.RegisterUser(t, q, "dict-two-projects")
	projectA := testutil.SeedProject(t, q, tenant, "Project A", "en", []string{"de"})
	projectB := testutil.SeedProject(t, q, tenant, "Project B", "en", []string{"de"})
	wordB, err := q.AddSpellcheckDictionaryWord(ctx, db.AddSpellcheckDictionaryWordParams{
		TenantID: tenant, ProjectID: projectB.ID, Language: "de", Word: "Nordwind", CreatedBy: &user,
	})
	if err != nil {
		t.Fatalf("AddSpellcheckDictionaryWord: %v", err)
	}
	wordExists := func(t *testing.T) bool {
		t.Helper()
		return testutil.CountRows(t, pool, `SELECT count(*) FROM spellcheck_dictionary WHERE id = $1 AND deleted_at IS NULL`, wordB.ID) == 1
	}
	deletePath := func(projectID int64) string {
		return fmt.Sprintf("/api/projects/%d/spellcheck-dictionary/%d", projectID, wordB.ID)
	}

	rec := testutil.DoRequest(t, router, http.MethodDelete, deletePath(projectA.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if !wordExists(t) {
		t.Fatal("a DELETE through project A removed project B's word")
	}

	rec = testutil.DoRequest(t, router, http.MethodDelete, deletePath(projectB.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if wordExists(t) {
		t.Error("the DELETE through project B returned 204 but the word is still there")
	}
}

// Removing a word sets deleted_at and keeps the row; the list and the check
// path stop seeing it, and adding the word again brings the same row back.
func TestIntegrationSpellcheckDictionary_DeleteIsSoft(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	user, tenant := testutil.RegisterUser(t, q, "dict-soft")
	project := testutil.SeedProject(t, q, tenant, "Soft", "en", []string{"de"})
	dictPath := fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", project.ID)
	listed := func(t *testing.T) []string {
		t.Helper()
		rec := testutil.DoRequest(t, router, http.MethodGet, dictPath+"?language=de", nil, user, tenant)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var body struct {
			Data []struct {
				Word string `json:"word"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &body)
		var words []string
		for _, w := range body.Data {
			words = append(words, w.Word)
		}
		return words
	}

	rec := testutil.DoRequest(t, router, http.MethodPost, dictPath, map[string]any{"language": "de", "word": "Nordwind"}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var added struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &added)

	rec = testutil.DoRequest(t, router, http.MethodDelete, fmt.Sprintf("%s/%d", dictPath, added.Data.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM spellcheck_dictionary WHERE id = $1 AND deleted_at IS NOT NULL`, added.Data.ID); n != 1 {
		t.Fatalf("removed word: %d soft-deleted rows, want 1", n)
	}
	if got := listed(t); len(got) != 0 {
		t.Errorf("list after remove = %v, want empty", got)
	}
	words, err := q.ListSpellcheckDictionaryWords(testutil.Ctx(), db.ListSpellcheckDictionaryWordsParams{
		ProjectID: project.ID, TenantID: tenant, Language: "de",
	})
	if err != nil || len(words) != 0 {
		t.Errorf("check-path words after remove = %v (err %v), want none", words, err)
	}
	rec = testutil.DoRequest(t, router, http.MethodDelete, fmt.Sprintf("%s/%d", dictPath, added.Data.ID), nil, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	rec = testutil.DoRequest(t, router, http.MethodPost, dictPath, map[string]any{"language": "de", "word": "Nordwind"}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	if got := listed(t); !slices.Equal(got, []string{"Nordwind"}) {
		t.Errorf("list after re-add = %v, want [Nordwind]", got)
	}
}
