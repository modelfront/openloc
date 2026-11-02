package translationmemory_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type stampedMatch struct {
	TargetText string    `json:"target_text"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	CreatedBy  string    `json:"created_by"`
	UpdatedBy  string    `json:"updated_by"`
}

// The Resources panel's match card shows who added the entry and who last
// changed it, and when. The match carries the entry's own stamps (not the time
// of the lookup), with the creator and the last editor kept apart; "human",
// the stand-in for "no person", comes back empty; and only a manager gets the
// email part of a user label — a linguist or vendor gets the name.
func TestIntegrationTMMatch_CarriesEntryStamps(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TM-Entry-Stamps")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@stamps.test", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@stamps.test", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@stamps.test", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &linguist.ID)
	tm := testutil.SeedTM(t, q, tenant.ID, "Stamps TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read", Priority: 0,
	}); err != nil {
		t.Fatalf("assign TM: %v", err)
	}
	insert := func(src, tgt, createdBy string) int64 {
		t.Helper()
		e, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: src, TargetText: tgt, CreatedBy: createdBy,
		})
		if err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
		return e.ID
	}
	// An entry the editor wrote: the creator is the "human" stand-in, the
	// last change is a person.
	editorEntry := insert("Hello world", "Hallo Welt", "human")
	created := time.Date(2026, 9, 3, 9, 10, 0, 0, time.UTC)
	updated := time.Date(2026, 10, 12, 14, 32, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE translation_memory SET created_at = $2, updated_at = $3, updated_by = $4 WHERE id = $1`,
		editorEntry, created, updated, "Anna Miller <anna@test.com>",
	); err != nil {
		t.Fatalf("stamp entry: %v", err)
	}
	// An imported entry whose TMX creationid is a name and an email.
	insert("Hello world!", "Hallo Welt!", "Daniel Weber <daniel@test.com>")

	lookup := func(userID int64, role string) map[string]stampedMatch {
		t.Helper()
		path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Hello world"))
		rec := testutil.DoRequestWithRole(t, router, "GET", path, nil, userID, tenant.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Matches []stampedMatch `json:"matches"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		out := map[string]stampedMatch{}
		for _, m := range resp.Matches {
			out[m.TargetText] = m
		}
		if len(out) != 2 {
			t.Fatalf("%s: got %d matches, want 2: %+v", role, len(out), resp.Matches)
		}
		return out
	}
	users := func(m stampedMatch) [2]string { return [2]string{m.CreatedBy, m.UpdatedBy} }

	asOwner := lookup(owner.ID, "owner")
	ed := asOwner["Hallo Welt"]
	if !ed.CreatedAt.Equal(created) || !ed.UpdatedAt.Equal(updated) {
		t.Errorf("stamps = created %v, updated %v; want %v, %v", ed.CreatedAt, ed.UpdatedAt, created, updated)
	}
	if got, want := users(ed), [2]string{"", "Anna Miller <anna@test.com>"}; got != want {
		t.Errorf("owner, editor entry: users %q, want %q", got, want)
	}
	if got, want := users(asOwner["Hallo Welt!"]), [2]string{"Daniel Weber <daniel@test.com>", "Daniel Weber <daniel@test.com>"}; got != want {
		t.Errorf("owner, imported entry: users %q, want %q", got, want)
	}

	// A project manager is a manager: the email boundary sits below it.
	if got, want := users(lookup(pm.ID, "project_manager")["Hallo Welt"]), [2]string{"", "Anna Miller <anna@test.com>"}; got != want {
		t.Errorf("project manager, editor entry: users %q, want %q", got, want)
	}

	asLinguist := lookup(linguist.ID, "linguist")
	if got, want := users(asLinguist["Hallo Welt"]), [2]string{"", "Anna Miller"}; got != want {
		t.Errorf("linguist, editor entry: users %q, want %q", got, want)
	}
	if got, want := users(asLinguist["Hallo Welt!"]), [2]string{"Daniel Weber", "Daniel Weber"}; got != want {
		t.Errorf("linguist, imported entry: users %q, want %q", got, want)
	}
}
