// AI-authored segment comments: the QE+APE step writes the
// LLM's `comment` output with user_id NULL. Covers: listed with a null author,
// deletable by any user (unlike human comments), human flow unchanged.
package comments_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationAIComments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AICommentCo")
	author := testutil.SeedUserWithRole(t, q, tenant.ID, "author@ai.test", "pass", "owner")
	other := testutil.SeedUserWithRole(t, q, tenant.ID, "other@ai.test", "pass", "admin")
	project := testutil.SeedProject(t, q, tenant.ID, "AIC Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)

	// One AI comment (the pipeline path) + one human comment.
	aiComment, err := q.CreateAIComment(ctx, db.CreateAICommentParams{TranslationID: tr.ID, Content: "meaning unclear"})
	if err != nil {
		t.Fatalf("CreateAIComment: %v", err)
	}
	if aiComment.UserID != nil {
		t.Fatalf("AI comment must have NULL user_id, got %v", *aiComment.UserID)
	}
	humanComment, err := q.CreateComment(ctx, db.CreateCommentParams{TranslationID: tr.ID, UserID: &author.ID, Content: "human remark"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// Listing shows both; the AI comment has a null author (LEFT JOIN).
	rec := testutil.DoRequestWithRole(t, router, "GET",
		fmt.Sprintf("/api/translations/%d/comments", tr.ID), nil, other.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data []struct {
			ID         int64   `json:"id"`
			Content    string  `json:"content"`
			AuthorName *string `json:"author_name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(list.Data))
	}
	for _, c := range list.Data {
		switch c.ID {
		case aiComment.ID:
			if c.AuthorName != nil {
				t.Errorf("AI comment must list a null author, got %q", *c.AuthorName)
			}
		case humanComment.ID:
			if c.AuthorName == nil {
				t.Errorf("human comment must carry its author")
			}
		}
	}

	// A different user can delete the AI comment…
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/translations/%d/comments/%d", tr.ID, aiComment.ID), nil, other.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	// …but not the human one: author-matched delete, reported as not found.
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/translations/%d/comments/%d", tr.ID, humanComment.ID), nil, other.ID, tenant.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	remaining, err := q.ListCommentsByTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("ListCommentsByTranslation: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != humanComment.ID {
		t.Fatalf("AI comment must be deleted and the human one kept, got %+v", remaining)
	}
	// Soft delete: the row stays, tombstoned.
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM segment_comments WHERE id = $1`, aiComment.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted comment row: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted comment must keep its row with deleted_at set")
	}
}

// A comment id is only reachable through its own translation. Another
// tenant's user, routing through a translation they own, must not reach an
// AI comment (no author, so the author check alone would let anyone in).
func TestIntegrationCommentDeleteScopedToTranslation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "CommentScopeA")
	tenantB := testutil.SeedTenant(t, q, "CommentScopeB")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b@scope.test", "pass", "owner")
	seed := func(tenantID int64, name string) db.Translation {
		p := testutil.SeedProject(t, q, tenantID, name, "en", []string{"de"})
		f := testutil.SeedFile(t, q, p.ID, name+".txt", db.FileFormatTxt)
		seg := testutil.SeedSegment(t, q, f.ID, "Hello", 0)
		return testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	}
	trA := seed(tenantA.ID, "A")
	trB := seed(tenantB.ID, "B")
	aiA, err := q.CreateAIComment(ctx, db.CreateAICommentParams{TranslationID: trA.ID, Content: "tenant A remark"})
	if err != nil {
		t.Fatalf("CreateAIComment: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/translations/%d/comments/%d", trB.ID, aiA.ID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		fmt.Sprintf("/api/translations/%d/comments/%d", trA.ID, aiA.ID), nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	left, err := q.ListCommentsByTranslation(ctx, trA.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("tenant A's comment must survive, got %d", len(left))
	}
}

// A comment may only be added to a segment in the caller's own organization:
// another tenant's translation id answers 404 and stores nothing.
func TestIntegrationCommentCreateScopedToTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	victim := testutil.SeedTenant(t, q, "CommentVictim")
	project := testutil.SeedProject(t, q, victim.ID, "Victim project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)

	attacker := testutil.SeedTenant(t, q, "CommentAttacker")
	manager := testutil.SeedUserWithRole(t, q, attacker.ID, "pm@attacker.test", "pass", "project_manager")

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/translations/%d/comments", tr.ID), map[string]any{"content": "planted"}, manager.ID, attacker.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	rows, err := q.ListCommentsByTranslation(context.Background(), tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a comment from another organization was stored: %+v", rows)
	}
}
