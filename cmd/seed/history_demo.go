package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/strdist"
	"openloc/internal/wordcount"
)

// The History demo: an en→es project filled from a translation memory by
// several Apply TM runs and then partly edited, so the editor's History panel
// has every shape of row to show — a fill repeated by re-runs, fuzzy matches a
// person edited, two saves by one person in one step, and a segment no memory
// matched.
//
// It lives in the second org. Steps of this seed take the main org's newest TM
// and its history as the Demo Project's own (healTMEntries,
// healProjectAssignments, healDashboardHistory), so a second TM or project
// with history there would change what they pick.
const (
	historyDemoProjectName = "History demo — TM applied"
	historyDemoTMName      = "History demo TM"
	// Apply TM runs on the job. A fuzzy fill is written by every run; an exact
	// match approves the segment on the first, so later runs leave it be.
	historyDemoTMRuns = 3
)

var historyDemoTM = []struct{ source, target string }{
	{"Your report has been sent.", "Su informe ha sido enviado."},
	{"Your report has been sent to the manager.", "Su informe ha sido enviado al gerente."},
	{"Click Save to keep the changes.", "Haga clic en Guardar para conservar los cambios."},
	{"Thank you for your patience.", "Gracias por su paciencia."},
	{"We will contact you within three business days.", "Nos pondremos en contacto con usted en un plazo de tres días hábiles."},
	{"Please check your inbox.", "Revise su bandeja de entrada."},
	{"Report sent", "Denuncia enviada"},
}

// Match percents are the scores the TM pipeline gives these pairs.
var historyDemoSegments = []struct {
	source   string
	tmSource string   // the historyDemoTM entry that matched; "" for none
	percent  int16    // match score of that entry
	edits    []string // the person's confirms, oldest first
}{
	{"Your report has been sent.", "Your report has been sent.", 100, nil},
	{"Your report has been sent to the team.", "Your report has been sent to the manager.", 85,
		[]string{"Su informe ha sido enviado al equipo."}},
	{"Click Save to keep your changes.", "Click Save to keep the changes.", 88,
		[]string{"Haga clic en Guardar para conservar sus cambio.", "Haga clic en Guardar para conservar sus cambios."}},
	{"Thank you for your patience.", "Thank you for your patience.", 100, nil},
	{"We will contact you within two business days.", "We will contact you within three business days.", 91,
		[]string{"Nos pondremos en contacto con usted en un plazo de dos días hábiles."}},
	{"Please check your email inbox.", "Please check your inbox.", 80, nil},
	{"Report Sent", "Report sent", 99, nil},
	{"The weather is nice today.", "", 0, nil},
}

// healHistoryDemo creates the History demo in the "acme" org unless a project
// by its name is already there. Everything is written in one transaction, so a
// failed run leaves nothing half-made for the next one to skip over.
func healHistoryDemo(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, userID int64) {
	tenant, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		log.Printf("⚠ History demo skipped, no second org: %v", err)
		return
	}
	projects, err := q.ListProjects(ctx, tenant.ID)
	if err != nil {
		log.Printf("⚠ History demo: list projects: %v", err)
		return
	}
	for _, p := range projects {
		if p.Name == historyDemoProjectName {
			log.Printf("✓ History demo already present (%s, project %d)", tenant.Name, p.ID)
			return
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("⚠ History demo: begin: %v", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	projectID, err := createHistoryDemo(ctx, q.WithTx(tx), tx, tenant.ID, userID)
	if err != nil {
		log.Printf("⚠ History demo not created: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("⚠ History demo: commit: %v", err)
		return
	}
	log.Printf("✓ History demo created in %s (project %d, Apply TM ×%d)", tenant.Name, projectID, historyDemoTMRuns)
}

func createHistoryDemo(ctx context.Context, q *db.Queries, tx pgx.Tx, tenantID, userID int64) (int64, error) {
	source, target := "en", "es"

	tm, err := q.CreateTMCollection(ctx, db.CreateTMCollectionParams{
		Name: historyDemoTMName, TenantID: tenantID, SourceLanguage: &source,
		TargetLanguages: []string{target}, CreatedBy: auth.UserIDPtr(userID),
	})
	if err != nil {
		return 0, fmt.Errorf("create TM: %w", err)
	}
	entries := make(map[string]db.TranslationMemory, len(historyDemoTM))
	for _, e := range historyDemoTM {
		row, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: source, TargetLanguage: target,
			SourceText: e.source, TargetText: e.target, CreatedBy: "seed",
		})
		if err != nil {
			return 0, fmt.Errorf("insert TM entry %q: %w", e.source, err)
		}
		entries[e.source] = row
	}

	// Read-only, so the confirms below never write back into the memory.
	profile, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{TenantID: tenantID, FuzzyThreshold: 0.75})
	if err != nil {
		return 0, fmt.Errorf("create settings profile: %w", err)
	}
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: profile.ID, TmID: tm.ID, Mode: "read",
	}); err != nil {
		return 0, fmt.Errorf("assign TM: %w", err)
	}
	if err := settings.CreateDefaultWorkflowSteps(ctx, q, tenantID, profile.ID); err != nil {
		return 0, fmt.Errorf("create workflow steps: %w", err)
	}
	steps, err := q.ListSettingsWorkflowSteps(ctx, profile.ID)
	if err != nil || len(steps) == 0 {
		return 0, fmt.Errorf("list workflow steps (%d): %v", len(steps), err)
	}

	project, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name: historyDemoProjectName, SourceLanguage: source, TargetLanguages: []string{target},
		TenantID: tenantID, SettingsProfileID: profile.ID,
		Status: db.ProjectStatusInProgress, CreatedBy: auth.UserIDPtr(userID),
	})
	if err != nil {
		return 0, fmt.Errorf("create project: %w", err)
	}
	// storage_path is a placeholder — the file is not S3-exportable.
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID: project.ID, Name: "history-demo.txt", Format: "txt",
		StoragePath: fmt.Sprintf("seed/history-demo-%d.txt", project.ID), SegmentCount: int32(len(historyDemoSegments)),
	})
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	words := 0
	for _, s := range historyDemoSegments {
		words += wordcount.CountWords(s.source)
	}
	job, err := q.CreateJob(ctx, db.CreateJobParams{FileID: file.ID, TargetLanguage: target, WordCount: int32(words)})
	if err != nil {
		return 0, fmt.Errorf("create job: %w", err)
	}

	// The edits below are attributed to the job's first step.
	activeStep, err := startDemoJob(ctx, q, job.ID, steps, userID)
	if err != nil {
		return 0, err
	}

	// The runs a day apart, the edits in the last hours.
	now := time.Now()
	runAt := func(run int) time.Time { return now.Add(-time.Duration(historyDemoTMRuns-run) * 24 * time.Hour) }
	editAt := func(i int) time.Time { return now.Add(-3*time.Hour + time.Duration(i)*10*time.Minute) }

	for pos, s := range historyDemoSegments {
		hist, err := historyDemoSegmentRows(ctx, q, tx, file.ID, pos, s.source, wordcount.CountWords(s.source),
			s.tmSource, s.percent, s.edits, entries, tm.ID, userID, activeStep)
		if err != nil {
			return 0, err
		}
		for i, h := range hist {
			at := editAt(i - (len(hist) - len(s.edits)))
			if h.Kind == db.TranslationHistoryKindTm {
				at = runAt(i)
			}
			if _, err := tx.Exec(ctx, `UPDATE translation_history SET created_at = $1 WHERE id = $2`, at, h.ID); err != nil {
				return 0, fmt.Errorf("backdate history %d: %w", h.ID, err)
			}
		}
	}
	return project.ID, nil
}

// historyDemoSegmentRows writes one segment, its es translation as its last
// event left it, and its history oldest first: a tm row per Apply TM run that
// filled it, then a human row per confirm, each measured against the text
// before it.
func historyDemoSegmentRows(ctx context.Context, q *db.Queries, tx pgx.Tx, fileID int64, pos int, source string, words int,
	tmSource string, percent int16, edits []string, entries map[string]db.TranslationMemory, tmID, userID, activeStep int64,
) ([]db.TranslationHistory, error) {
	var segID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO segments (file_id, source_text, segment_key, position, word_count) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		fileID, source, fmt.Sprintf("%d", pos+1), pos, words,
	).Scan(&segID); err != nil {
		return nil, fmt.Errorf("insert segment %d: %w", pos, err)
	}

	entry, matched := entries[tmSource]
	var (
		text     *string
		origin   *string
		pct      *int16
		editedBy *int64
		distance int32
		status   = "needs_review"
	)
	if matched {
		tmOrigin := "tm"
		text, origin, pct = &entry.TargetText, &tmOrigin, &percent
		if percent >= 100 {
			status = "approved"
		}
	}
	for _, e := range edits {
		distance = int32(strdist.Levenshtein(*text, e))
		text, editedBy, status = &e, &userID, "approved"
	}

	var trID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO translations (segment_id, target_language, status, active_text, origin, tm_match_percent, edited_by, edit_distance)
		 VALUES ($1, 'es', $2, $3, $4, $5, $6, $7) RETURNING id`,
		segID, status, text, origin, pct, editedBy, distance,
	).Scan(&trID); err != nil {
		return nil, fmt.Errorf("insert translation %d: %w", pos, err)
	}
	if !matched {
		return nil, nil
	}

	runs := historyDemoTMRuns
	if percent >= 100 {
		runs = 1
	}
	var rows []db.TranslationHistory
	for range runs {
		row, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: trID, Kind: db.TranslationHistoryKindTm, Text: &entry.TargetText,
			TmID: &tmID, TmEntryID: &entry.ID, TmMatchPercent: &percent, Details: []byte(`{"source":"pipeline"}`),
		})
		if err != nil {
			return nil, fmt.Errorf("tm history %d: %w", pos, err)
		}
		rows = append(rows, row)
	}
	previous := entry.TargetText
	for _, e := range edits {
		d := int32(strdist.Levenshtein(previous, e))
		row, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: trID, Kind: db.TranslationHistoryKindHuman, Text: &e,
			CreatedBy: &userID, EditDistance: &d, JobStepAssignmentID: &activeStep, Details: []byte(`{}`),
		})
		if err != nil {
			return nil, fmt.Errorf("edit history %d: %w", pos, err)
		}
		rows = append(rows, row)
		previous = e
	}
	return rows, nil
}

// startDemoJob gives a job one assignment per workflow step, the first in
// progress and the person's own as on a live job, and returns that first
// assignment's id.
func startDemoJob(ctx context.Context, q *db.Queries, jobID int64, steps []db.WorkflowStep, userID int64) (int64, error) {
	var activeStep int64
	for i, step := range steps {
		status, assignee := "pending", (*int64)(nil)
		if i == 0 {
			status, assignee = "in_progress", &userID
		}
		a, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID: jobID, WorkflowStepID: step.ID, AssignedTo: assignee, Status: status,
		})
		if err != nil {
			return 0, fmt.Errorf("assign step %q: %w", step.Name, err)
		}
		if i == 0 {
			activeStep = a.ID
		}
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobID, CurrentStepID: &activeStep}); err != nil {
		return 0, fmt.Errorf("set current step: %w", err)
	}
	return activeStep, nil
}
