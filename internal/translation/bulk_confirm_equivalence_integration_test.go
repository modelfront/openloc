package translation_test

import (
	"context"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/testutil"
	"openloc/internal/translation"
)

// A bulk confirm writes what one confirm per row writes: the same translation
// state, the same history row (kind, text, rich text, edit distance, editing
// time, step) and the same QA ignores. Two identical files, one confirmed row
// by row, the other in bulk.
func TestIntegrationConfirmSelection_WritesWhatASingleConfirmWrites(t *testing.T) {
	f := newConfirmFixture(t)
	ctx := auth.WithClaims(context.Background(), &auth.Claims{TenantID: f.tenantID, UserID: f.userID, Role: auth.RoleOwner})
	qaSvc := qa.NewServiceWithPool(f.q, f.pool)
	svc := translation.NewService(f.q, f.pool, nil, qaSvc)

	type row struct {
		source, history, text string
		rich, sourceRich      *string
		qeNote                bool
		ignoreQA              bool
	}
	rich, sourceRich := "<g id=\"1\">Rich</g> uno", "<g id=\"1\">Rich</g> one"
	rows := []row{
		{source: "Edited", history: "Machine one", text: "Human one", qeNote: true},
		{source: "Unchanged", history: "Same text", text: "Same text", qeNote: true},
		{source: "Rich one", history: "Machine rich", text: "Rich uno", rich: &rich, sourceRich: &sourceRich},
		{source: "No history", text: "Only text"},
		{source: "Hello {name}", history: "Hola {name}", text: "Hola", ignoreQA: true},
	}
	seedFile := func(name string) (db.Job, []int64) {
		t.Helper()
		file := testutil.SeedFile(t, f.q, f.projectID, name, db.FileFormatTxt)
		job := testutil.SeedJob(t, f.q, file.ID, "es")
		var ids []int64
		for i, r := range rows {
			seg := testutil.SeedSegment(t, f.q, file.ID, r.source, int32(i+1))
			f.exec(t, `UPDATE segments SET source_rich_text = $2 WHERE id = $1`, seg.ID, r.sourceRich)
			id := testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview).ID
			if r.history != "" {
				text := r.history
				if _, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
					TranslationID: id, Kind: db.TranslationHistoryKindMt, Text: &text,
				}); err != nil {
					t.Fatal(err)
				}
			}
			f.exec(t, `UPDATE translations SET active_text = $2, active_rich_text = $3,
				qe_note = CASE WHEN $4 THEN 'note' END WHERE id = $1`, id, r.text, r.rich, r.qeNote)
			if r.ignoreQA {
				if _, err := qaSvc.RunAndPersistTranslation(ctx, id, f.tenantID); err != nil {
					t.Fatal(err)
				}
				f.exec(t, `UPDATE qa_results SET ignored = true WHERE translation_id = $1`, id)
			}
			ids = append(ids, id)
		}
		return job, ids
	}
	_, single := seedFile("single.txt")
	bulkJob, bulk := seedFile("bulk.txt")

	for i, id := range single {
		if _, err := svc.Confirm(ctx, translation.ConfirmParams{
			ID: id, TenantID: f.tenantID, UserID: f.userID, HumanText: rows[i].text, HumanRichText: rows[i].rich,
		}); err != nil {
			t.Fatalf("single confirm of %q: %v", rows[i].source, err)
		}
	}
	res, err := svc.ConfirmSelection(ctx, bulkJob, f.tenantID, f.userID, bulk)
	if err != nil {
		t.Fatalf("bulk confirm: %v", err)
	}
	if len(res.Applied) != len(rows) {
		t.Fatalf("bulk applied %v, skipped %v; want every row", res.Applied, res.Skipped)
	}

	state := func(id int64) string {
		t.Helper()
		var s string
		if err := f.pool.QueryRow(ctx, `
			SELECT concat_ws('|', t.status, t.step_edited, t.qe_note IS NULL, t.edit_distance, t.confirmed_via,
			                 t.edited_by, t.active_text, coalesce(t.active_rich_text, '<nil>'),
			                 h.kind, coalesce(h.text, '<nil>'), coalesce(h.rich_text, '<nil>'),
			                 coalesce(h.edit_distance::text, '<nil>'), coalesce(h.editing_time_ms::text, '<nil>'),
			                 h.created_by, h.job_step_assignment_id IS NULL,
			                 (SELECT string_agg(qr.check_type || ':' || qr.ignored, ',' ORDER BY qr.check_type)
			                  FROM qa_results qr WHERE qr.translation_id = t.id))
			FROM translations t
			JOIN LATERAL (SELECT * FROM translation_history th WHERE th.translation_id = t.id
			              ORDER BY th.created_at DESC, th.id DESC LIMIT 1) h ON true
			WHERE t.id = $1`, id).Scan(&s); err != nil {
			t.Fatalf("read state: %v", err)
		}
		return s
	}
	for i := range rows {
		if got, want := state(bulk[i]), state(single[i]); got != want {
			t.Errorf("%s:\nbulk   %s\nsingle %s", rows[i].source, got, want)
		}
	}
}
