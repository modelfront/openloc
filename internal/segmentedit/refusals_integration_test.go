package segmentedit_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Every refusal answers with its status and code, and changes nothing: the
// German job keeps its two rows.
func TestIntegrationSplitJoin_Refusals(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	other := testutil.SeedUserWithRole(t, f.q, f.tenantID, "other@test.com", "pass", "project_manager")

	cases := []struct {
		name   string
		setup  func(t *testing.T) (undo func())
		body   func(t *testing.T) (string, any)
		status int
		code   string
	}{
		{"locked", func(t *testing.T) func() {
			f.exec(t, `UPDATE translations SET locked = true WHERE id = $1`, de.ID)
			return func() { f.exec(t, `UPDATE translations SET locked = false WHERE id = $1`, de.ID) }
		}, splitAt(f, de.ID, 5), http.StatusConflict, "segment_locked"},
		{"held by another user", func(t *testing.T) func() {
			testutil.SeedHold(t, f.q, f.tenantID, f.jobs["de"].ID, de.ID, other.ID)
			return func() { f.exec(t, `DELETE FROM translation_holds WHERE translation_id = $1`, de.ID) }
		}, splitAt(f, de.ID, 5), http.StatusConflict, "segment_held"},
		{"pre-translation running", func(t *testing.T) func() {
			f.exec(t, `UPDATE jobs SET status = 'in_progress', current_step = 'mt' WHERE id = $1`, f.jobs["de"].ID)
			return func() {
				f.exec(t, `UPDATE jobs SET status = 'completed', current_step = '' WHERE id = $1`, f.jobs["de"].ID)
			}
		}, splitAt(f, de.ID, 5), http.StatusConflict, "pipeline_running"},
		{"an old version", nil, func(t *testing.T) (string, any) {
			body := splitBody(f.translation(t, de.ID), 5)
			f.exec(t, `UPDATE translations SET updated_at = now() WHERE id = $1`, de.ID)
			return "split", body
		}, http.StatusConflict, "conflict"},
		{"at the edge", nil, splitAt(f, de.ID, 0), http.StatusBadRequest, "split_position"},
		{"past the end", nil, splitAt(f, de.ID, 500), http.StatusBadRequest, "split_position"},
		{"XLIFF codes", func(t *testing.T) func() {
			f.exec(t, `UPDATE segments SET source_rich_text = $2 WHERE id = $1`, f.segs[0].ID,
				`Press the <bpt id="1">&lt;b&gt;</bpt>button<ept id="1">&lt;/b&gt;</ept> and wait for the light`)
			return func() { f.exec(t, `UPDATE segments SET source_rich_text = NULL WHERE id = $1`, f.segs[0].ID) }
		}, splitAt(f, de.ID, 5), http.StatusBadRequest, "split_tags"},
		{"a PO plural form", func(t *testing.T) func() {
			f.exec(t, `UPDATE files SET format = 'po' WHERE id = $1`, f.file.ID)
			f.exec(t, `UPDATE segments SET segment_key = 'press#1' WHERE id = $1`, f.segs[0].ID)
			return func() {
				f.exec(t, `UPDATE files SET format = 'txt' WHERE id = $1`, f.file.ID)
				f.exec(t, `UPDATE segments SET segment_key = 'line:1' WHERE id = $1`, f.segs[0].ID)
			}
		}, splitAt(f, de.ID, 5), http.StatusBadRequest, "split_unsupported"},
		{"join of one row", nil, joinOf(f, de.ID), http.StatusBadRequest, "join_count"},
		{"join of two imported rows", nil, joinOf(f, de.ID, f.trs["de"][1].ID), http.StatusBadRequest, "join_other_unit"},
		{"join of another job's row", nil, joinOf(f, de.ID, f.trs["fr"][1].ID), http.StatusNotFound, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.setup != nil {
				defer c.setup(t)()
			}
			action, body := c.body(t)
			rec := f.post(t, "de", action, body)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.code) {
				t.Errorf("%d %s, want %d %s", rec.Code, rec.Body.String(), c.status, c.code)
			}
			if n := len(f.index(t, "de").SegmentIDs); n != 2 {
				t.Errorf("the German job has %d rows after a refusal, want 2", n)
			}
		})
	}
}

func splitAt(f *fixture, id int64, offset int) func(t *testing.T) (string, any) {
	return func(t *testing.T) (string, any) { return "split", splitBody(f.translation(t, id), offset) }
}

func joinOf(f *fixture, ids ...int64) func(t *testing.T) (string, any) {
	return func(t *testing.T) (string, any) {
		rows := make([]map[string]any, len(ids))
		for i, id := range ids {
			rows[i] = map[string]any{"translation_id": id, "expected_updated_at": f.translation(t, id).UpdatedAt.Time}
		}
		return "join", map[string]any{"rows": rows}
	}
}

// Another tenant cannot reach the job; a linguist can only when assigned.
func TestIntegrationSplitJoin_Access(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	path := fmt.Sprintf("/api/jobs/%d/segments/split", f.jobs["de"].ID)

	strangerID, strangerTenant := testutil.RegisterUser(t, f.q, "stranger")
	if rec := testutil.DoRequest(t, f.router, http.MethodPost, path, splitBody(de, 5), strangerID, strangerTenant); rec.Code != http.StatusNotFound {
		t.Errorf("another tenant: %d %s, want 404", rec.Code, rec.Body.String())
	}

	linguist := testutil.SeedUserWithRole(t, f.q, f.tenantID, "linguist@test.com", "pass", "linguist")
	if rec := testutil.DoRequestWithRole(t, f.router, http.MethodPost, path, splitBody(de, 5), linguist.ID, f.tenantID, "linguist"); rec.Code != http.StatusForbidden {
		t.Errorf("unassigned linguist: %d %s, want 403", rec.Code, rec.Body.String())
	}
	testutil.SeedJobStep(t, f.q, f.project.SettingsProfileID, f.jobs["de"].ID, &linguist.ID)
	if rec := testutil.DoRequestWithRole(t, f.router, http.MethodPost, path, splitBody(f.translation(t, de.ID), 5), linguist.ID, f.tenantID, "linguist"); rec.Code != http.StatusOK {
		t.Errorf("assigned linguist: %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// A step reopened after a split does not bring back the status its round
// ended with: that status was earned on the source before the split.
func TestIntegrationSplit_RollbackDoesNotRestoreApproval(t *testing.T) {
	f := newFixture(t)
	de := f.trs["de"][0]
	ctx := context.Background()
	step := testutil.SeedJobStep(t, f.q, f.project.SettingsProfileID, f.jobs["de"].ID, &f.userID)
	f.exec(t, `INSERT INTO translation_step_statuses
		(tenant_id, translation_id, job_step_assignment_id, round, ended_at, status, confirmed_via, human_edited, end_text_md5)
		SELECT $1, t.id, $3, jsa.round, now(), 'approved', 'user', true,
		       md5(coalesce(t.active_text, '') || chr(31) || coalesce(t.active_rich_text, ''))
		FROM translations t, job_step_assignments jsa WHERE t.id = $2 AND jsa.id = $3`, f.tenantID, de.ID, step)

	f.split(t, "de", de.ID, len("Press the button"))
	if err := f.q.RestoreStepStatuses(ctx, db.RestoreStepStatusesParams{AssignmentID: step, OnlyUnchanged: false}); err != nil {
		t.Fatal(err)
	}
	if got := f.translation(t, de.ID).Status; got != db.TranslationStatusNeedsReview {
		t.Errorf("status after the rollback = %s, want needs_review", got)
	}
}
