package flowtest

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationCrossTenantIsolation verifies that tenant A's resources
// are invisible and inaccessible to tenant B across all critical endpoints.
func TestIntegrationCrossTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Seed two tenants with users
	tenantA := testutil.SeedTenant(t, q, "Tenant A")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")
	tenantB := testutil.SeedTenant(t, q, "Tenant B")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	ctx := testutil.Ctx()

	// Tenant A creates a project with a file, segments, translations, and a job
	projA := testutil.SeedProject(t, q, tenantA.ID, "Project A", "en", []string{"de"})
	fileA := testutil.SeedFile(t, q, projA.ID, "test.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, fileA.ID, "Hello world", 1)
	trA := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")

	// Tenant A creates a TM with an entry
	tmA := testutil.SeedTM(t, q, tenantA.ID, "TM-A")
	tmEntry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmA.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello world", TargetText: "Hallo Welt", CreatedBy: "seed",
	})
	if err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	// Tenant A creates a comment on the translation
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{
		TranslationID: trA.ID, UserID: &userA.ID, Content: "Needs review",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	_ = comment // used to prove comment exists

	// --- Project access ---
	t.Run("project list isolation", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET", "/api/projects", nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		// Tenant B must NOT see tenant A's project. Decode and assert empty.
		var resp struct {
			Data []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if len(resp.Data) != 0 {
			t.Fatalf("tenant B leaked %d projects from tenant A: %+v", len(resp.Data), resp.Data)
		}
	})

	t.Run("project detail cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d", projA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// --- Job access ---
	t.Run("job detail cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("job translations cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/translations", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("job export cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/export", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("job complete cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/complete", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("job find-replace cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/find-replace", jobA.ID),
			map[string]any{"find": "Hello", "replace": "Hacked"},
			userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("job delete cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "DELETE",
			fmt.Sprintf("/api/jobs/%d", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// --- Per-job pipeline access (phase-split endpoints) ---
	t.Run("tm-apply cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/tm-apply", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("mt-apply cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/mt-apply", jobA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// mt-preview is the only GET among the phase-split routes. It goes
	// through the same RequireJobOwnership middleware and inline GetJobTenant
	// guard as tm-apply and mt-apply; this pins the contract for the read path.
	t.Run("mt-preview cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello", jobA.ID),
			nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// --- File-level pipeline access (export, the surviving file-level route) ---
	t.Run("pipeline export cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/files/%d/export?lang=de", fileA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// --- NEW: Audit findings 7.1-7.8 (cross-tenant gaps) ---

	// 7.1: Job list via project — no tenant filter on ListJobsByProject
	t.Run("7.1 job list by project cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d/jobs", projA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.2: File list via project — no tenant filter on ListFilesPaginated
	t.Run("7.2 file list by project cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d/files", projA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.3: TM entry delete — completely unscoped DeleteTMEntry
	t.Run("7.3 TM entry delete cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "DELETE",
			fmt.Sprintf("/api/tms/%d/entries/%d", tmA.ID, tmEntry.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.4: Comment count by file — no tenant filter
	t.Run("7.4 comment counts cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/files/%d/comment-counts?lang=de", fileA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.5: Comment list by translation — no tenant filter
	t.Run("7.5 comment list cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/translations/%d/comments", trA.ID), nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("7.5b comment create cross-tenant", func(t *testing.T) {
		path := fmt.Sprintf("/api/translations/%d/comments", trA.ID)
		rec := testutil.DoRequest(t, router, "POST", path,
			map[string]string{"content": "from tenant B"}, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		// The same route works for the translation's own tenant, so the 404
		// above comes from the tenant check and not from the route.
		rec = testutil.DoRequest(t, router, "POST", path,
			map[string]string{"content": "from tenant A"}, userA.ID, tenantA.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)

		rows, err := q.ListCommentsByTranslation(ctx, trA.ID)
		if err != nil {
			t.Fatalf("ListCommentsByTranslation: %v", err)
		}
		for _, c := range rows {
			if c.Content == "from tenant B" {
				t.Fatalf("tenant B's comment was stored on tenant A's translation")
			}
		}
	})

	// 7.6: Terminology lookup via project_id — uses unscoped GetProjectByID
	t.Run("7.6 terminology lookup cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/terminology/lookup?project_id=%d&source_language=en&target_language=de&source_text=Hello", projA.ID),
			nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.7: Concordance search via project_id — uses unscoped GetProjectByID
	t.Run("7.7 concordance search cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/tms/concordance?project_id=%d&source_language=en&target_language=de&q=Hello", projA.ID),
			nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// 7.8: TM matches via job — chains unscoped GetJob → GetFile → GetProjectByID
	t.Run("7.8 TM matches cross-tenant", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=Hello", jobA.ID),
			nil, userB.ID, tenantB.ID)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// --- Verify tenant A can still access their own resources ---
	t.Run("owner can access own project", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/projects/%d", projA.ID), nil, userA.ID, tenantA.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	t.Run("owner can access own job", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d", jobA.ID), nil, userA.ID, tenantA.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	t.Run("owner can access own translations", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/translations", jobA.ID), nil, userA.ID, tenantA.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	})
}
