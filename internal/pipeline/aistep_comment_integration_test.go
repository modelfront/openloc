package pipeline_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// commentEnv is a job whose AI step runs a comment-only prompt against a stub
// provider. The stub records which segments it was sent and answers with a
// comment for the segments in flag, null for the rest.
type commentEnv struct {
	*aiUsageEnv
	jobID int64

	mu      sync.Mutex
	sent    []string
	flag    map[string]bool
	wording string
}

func newCommentEnv(t *testing.T) *commentEnv {
	t.Helper()
	c := &commentEnv{flag: map[string]bool{}, wording: "Term mismatch: use the glossary term."}
	srv := usageStub(t, func(id string) string {
		c.mu.Lock()
		defer c.mu.Unlock()
		id = strings.Trim(id, `"`)
		c.sent = append(c.sent, id)
		if c.flag[id] {
			// A volunteered verdict and postedit, which this mode must ignore.
			return `"comment":"` + c.wording + `","approved":true,"score":99,"postedit":"Rewritten","postedit_approved":true`
		}
		return `"comment":null`
	})
	c.aiUsageEnv = newAIUsageEnv(t, srv.URL)
	ctx := context.Background()

	prompt, err := c.q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
		TenantID: c.tenant.ID, Name: "Advisor", PromptText: "Flag issues.", OutComments: true,
	})
	if err != nil {
		t.Fatalf("CreateAIPrompt: %v", err)
	}
	engine, err := c.q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: c.tenant.ID, Name: "Advisor", Purpose: "qe_ape"})
	if err != nil {
		t.Fatalf("CreateAIEngine: %v", err)
	}
	if _, err := c.q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "en", TargetLangs: []string{"fr"},
		SystemPromptID: &prompt.ID, TokenID: &c.tokenID, Model: "gpt-4o-mini", Settings: []byte("{}"),
	}); err != nil {
		t.Fatalf("CreateAIEngineRow: %v", err)
	}
	job := testutil.SeedJob(t, c.q, c.file.ID, "fr")
	routerID := testutil.SeedRouterForEngine(t, c.q, c.tenant.ID, engine.ID)
	testutil.SeedAIJobStepWithRouter(t, c.q, c.project.SettingsProfileID, job.ID, routerID)
	c.jobID = job.ID
	return c
}

func (c *commentEnv) run(t *testing.T) (total, commented int) {
	t.Helper()
	c.mu.Lock()
	c.sent = nil
	c.mu.Unlock()
	res, err := c.svc.RunAIStep(context.Background(), c.tenant.ID, c.jobID, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.AutoApproved != 0 {
		t.Errorf("a comment-only run approved %d segments", res.AutoApproved)
	}
	// The worker names the run in the project history from this.
	if res.Mode != ai.ModeComment {
		t.Errorf("result mode = %q, want %q", res.Mode, ai.ModeComment)
	}
	return res.Total, res.Commented
}

// rowStates maps each translation id to every field a comment-only run must
// leave alone, as one comparable string.
func (c *commentEnv) rowStates(t *testing.T) map[int64]string {
	t.Helper()
	rows, err := c.pool.Query(context.Background(), `
		SELECT t.id, concat_ws('|', t.active_text, t.active_rich_text, t.status::text, t.predict_outcome::text,
		       t.qe_note, t.locked::text, t.origin::text, t.edited_by::text, t.updated_at::text,
		       (SELECT count(*) FROM translation_history h WHERE h.translation_id = t.id)::text)
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1`, c.file.ID)
	if err != nil {
		t.Fatalf("read translations: %v", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			t.Fatal(err)
		}
		out[id] = state
	}
	return out
}

func (c *commentEnv) aiComments(t *testing.T, translationID int64) []string {
	t.Helper()
	rows, err := c.pool.Query(context.Background(), `
		SELECT content FROM segment_comments
		WHERE translation_id = $1 AND user_id IS NULL AND deleted_at IS NULL ORDER BY id`, translationID)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// A comment-only step reviews every segment with text — machine, human,
// confirmed and locked — and its only effect is AI comments: no text, status,
// verdict, score or history changes on any segment.
func TestIntegrationRunAIStep_CommentOnlyChangesNothingButComments(t *testing.T) {
	c := newCommentEnv(t)
	ctx := context.Background()

	// Segment 1 (seeded by the env): MT-filled, never reviewed.
	if _, err := c.pool.Exec(ctx, `UPDATE translations SET active_text = 'Bonjour le monde', origin = 'mt' WHERE id = $1`, c.tr.ID); err != nil {
		t.Fatal(err)
	}
	// Segment 2: a person's confirmed translation.
	human := testutil.SeedTranslation(t, c.q, testutil.SeedSegment(t, c.q, c.file.ID, "Save the file", 1).ID, "fr", db.TranslationStatusApproved)
	// Segment 3: locked.
	locked := testutil.SeedTranslation(t, c.q, testutil.SeedSegment(t, c.q, c.file.ID, "Open the file", 2).ID, "fr", db.TranslationStatusApproved)
	// Segment 4: no text yet — nothing to review.
	empty := testutil.SeedTranslation(t, c.q, testutil.SeedSegment(t, c.q, c.file.ID, "Close the file", 3).ID, "fr", db.TranslationStatusNeedsReview)
	if _, err := c.pool.Exec(ctx, `UPDATE translations SET active_text = 'Enregistrez le fichier', origin = NULL WHERE id = $1`, human.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(ctx, `UPDATE translations SET active_text = 'Ouvrez le fichier', locked = true WHERE id = $1`, locked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(ctx, `UPDATE translations SET active_text = NULL WHERE id = $1`, empty.ID); err != nil {
		t.Fatal(err)
	}
	c.flag["2"], c.flag["3"] = true, true

	before := c.rowStates(t)
	total, commented := c.run(t)
	if total != 3 || commented != 2 {
		t.Errorf("total=%d commented=%d, want 3 reviewed and 2 commented", total, commented)
	}
	if got := strings.Join(c.sent, ","); got != "1,2,3" {
		t.Errorf("segments sent to the model: %s, want 1,2,3 (every one with text)", got)
	}
	after := c.rowStates(t)
	for id, was := range before {
		if after[id] != was {
			t.Errorf("translation %d changed:\n before %s\n after  %s", id, was, after[id])
		}
	}
	if got := c.aiComments(t, human.ID); len(got) != 1 || got[0] != c.wording {
		t.Errorf("human segment AI comments = %v, want the one remark", got)
	}
	if got := c.aiComments(t, locked.ID); len(got) != 1 {
		t.Errorf("locked segment AI comments = %v, want one", got)
	}
	if got := c.aiComments(t, c.tr.ID); len(got) != 0 {
		t.Errorf("unflagged segment got AI comments: %v", got)
	}
}

// Re-running over unchanged segments repeats no comment the AI already left
// with the same wording; a new wording, or a remark a person deleted, is added.
func TestIntegrationRunAIStep_CommentOnlyRerunAddsNoDuplicates(t *testing.T) {
	c := newCommentEnv(t)
	ctx := context.Background()
	if _, err := c.pool.Exec(ctx, `UPDATE translations SET active_text = 'Bonjour le monde', origin = 'mt' WHERE id = $1`, c.tr.ID); err != nil {
		t.Fatal(err)
	}
	c.flag["1"] = true

	if _, commented := c.run(t); commented != 1 {
		t.Fatalf("first run commented %d, want 1", commented)
	}
	if _, commented := c.run(t); commented != 0 {
		t.Errorf("re-run with the same wording commented %d, want 0", commented)
	}
	if got := c.aiComments(t, c.tr.ID); len(got) != 1 {
		t.Fatalf("after a re-run: %v, want one comment", got)
	}

	c.wording = "Tone is too informal."
	if _, commented := c.run(t); commented != 1 {
		t.Errorf("re-run with a new wording commented %d, want 1", commented)
	}

	if _, err := c.pool.Exec(ctx, `UPDATE segment_comments SET deleted_at = now() WHERE translation_id = $1 AND content = $2`, c.tr.ID, c.wording); err != nil {
		t.Fatal(err)
	}
	if _, commented := c.run(t); commented != 1 {
		t.Errorf("a deleted comment blocked the same remark: commented %d, want 1", commented)
	}
	if got := c.aiComments(t, c.tr.ID); len(got) != 2 {
		t.Errorf("live AI comments %v, want the first remark and the re-added one", got)
	}
}
