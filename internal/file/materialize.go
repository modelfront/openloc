package file

// materialize.go — the half of an upload that depends on the project's target
// languages: translations, jobs, step assignments and the pipeline work behind
// them. A normal upload runs it right after storing the file; an upload into a
// draft project stops before it, and publish runs it for every file the draft
// collected. One code path, two moments.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/analysis"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/parsers"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xlsx"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
	"openloc/internal/stepdef"
	"openloc/internal/stepround"
	"openloc/internal/timing"
	"openloc/internal/wordcount"
)

// ingest is a stored, segmented file together with what the deferred half
// needs to finish it.
type ingest struct {
	project    db.Project
	file       db.File
	format     string
	content    []byte
	segments   []common.Segment
	segmentIDs []int64 // one per segment, same order
	wordCount  int
}

// langJob is one job the ingest created, by target language.
type langJob struct {
	lang  string
	jobID int64
}

// materializeInTx inserts the translations for every segment × target
// language (with the XLSX multi-target import on top), creates one job per
// target language with its workflow step assignments, and records the
// step_assigned audit rows. Runs inside the caller's transaction. warnings
// say why a bilingual file's translations were not imported.
func (h *Handler) materializeInTx(ctx context.Context, tx pgx.Tx, in ingest) (jobs []langJob, wfSteps []db.WorkflowStep, warnings []string, err error) {
	txq := db.New(tx)
	stampLang, warning := bilingualTarget(in)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	if err := h.batchInsertTranslations(ctx, tx, in.segmentIDs, in.segments, in.project.TargetLanguages, stampLang); err != nil {
		return nil, nil, nil, fmt.Errorf("create translations: %w", err)
	}
	if in.format == "xlsx" {
		if err := h.importXLSXTargets(ctx, txq, in); err != nil {
			return nil, nil, nil, err
		}
	}

	for _, lang := range in.project.TargetLanguages {
		j, err := txq.CreateJob(ctx, db.CreateJobParams{
			FileID:         in.file.ID,
			TargetLanguage: lang,
			WordCount:      int32(in.wordCount),
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create job: %w", err)
		}
		jobs = append(jobs, langJob{lang: lang, jobID: j.ID})
	}

	wfSteps, err = h.assignWorkflowSteps(ctx, txq, in, jobs)
	if err != nil {
		return nil, nil, nil, err
	}
	return jobs, wfSteps, warnings, nil
}

// importXLSXTargets copies the spreadsheet's per-language target columns onto
// the freshly inserted translations, with file_import lineage and history, in
// one statement per language inside the upload transaction. An unreadable
// workbook or stored layout imports no targets, like every other import
// warning.
func (h *Handler) importXLSXTargets(ctx context.Context, txq *db.Queries, in ingest) error {
	layout, err := parsers.FileXLSXLayout(in.file.XlsxLayout)
	if err != nil {
		logging.Warn(ctx, err, "stored xlsx layout unreadable", "upload-xlsx-layout-unreadable", logging.FileID(in.file.ID))
		return nil
	}
	xlsxTargets, err := xlsx.XLSX{PlainText: !in.file.InlineHtml, Layout: layout}.ExtractTargets(in.content, in.project.TargetLanguages)
	if err != nil {
		return nil
	}
	uploader := auth.UserIDFromContext(ctx)
	for lang, targets := range xlsxTargets {
		var segIDs []int64
		var texts, richTexts []string
		for i, seg := range in.segments {
			target, ok := targets[seg.Key]
			if !ok || target.Text == "" {
				continue
			}
			segIDs = append(segIDs, in.segmentIDs[i])
			texts = append(texts, target.Text)
			richTexts = append(richTexts, target.RichText)
		}
		if len(segIDs) == 0 {
			continue
		}
		if _, err := txq.ImportBilingualTargets(ctx, db.ImportBilingualTargetsParams{
			SegmentIds: segIDs, Texts: texts, RichTexts: richTexts,
			TargetLanguage: lang, CreatedBy: uploader,
		}); err != nil {
			return fmt.Errorf("import %s targets: %w", lang, err)
		}
	}
	return nil
}

// assignWorkflowSteps creates each job's step assignments from the project's
// workflow steps and records a workflow.step_assigned event for every
// assignment that carries a configured assignee or deadline. Returns the
// steps so the dispatcher can see whether the first one is an AI step.
func (h *Handler) assignWorkflowSteps(ctx context.Context, txq *db.Queries, in ingest, jobs []langJob) ([]db.WorkflowStep, error) {
	wfSteps, err := txq.ListSettingsWorkflowSteps(ctx, in.project.SettingsProfileID)
	if err != nil || len(wfSteps) == 0 {
		return nil, nil
	}
	blueprint, err := settings.LoadStepDeadlines(ctx, txq, in.project.SettingsProfileID)
	if err != nil {
		return nil, err
	}
	userID := auth.UserIDFromContext(ctx)
	actorID, actorName := projectevents.ActorFromContext(ctx)
	assignees := map[int64]db.User{}

	for _, lj := range jobs {
		var firstAssignmentID int64
		for i, step := range wfSteps {
			status := "pending"
			var assignedTo *int64
			if i == 0 {
				status = "in_progress"
				// First step: the blueprint's default assignee, else the
				// uploader — unless it is an automated step, which nobody works.
				switch {
				case stepdef.IsAutomated(step.StepType):
				case step.DefaultAssignee != nil:
					assignedTo = step.DefaultAssignee
				default:
					assignedTo = &userID
				}
			} else if !stepdef.IsAutomated(step.StepType) && step.DefaultAssignee != nil {
				assignedTo = step.DefaultAssignee
			}
			deadline := blueprint.For(step, lj.lang)
			assignment, err := txq.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
				JobID:          lj.jobID,
				WorkflowStepID: step.ID,
				AssignedTo:     assignedTo,
				Status:         status,
				Deadline:       deadline,
			})
			if err != nil {
				logging.Warn(ctx, err, "step assignment not created", "upload-step-assignment-failed",
					logging.JobID(lj.jobID))
				continue
			}
			if i == 0 {
				firstAssignmentID = assignment.ID
			}
			// Audit-worthy = explicitly configured: a default assignee on the
			// step, or a deadline. The first-step fallback to the uploader is
			// bookkeeping — every bare project has a default step.
			if step.DefaultAssignee == nil && !deadline.Valid {
				continue
			}
			details := map[string]any{
				"step": step.Name, "file_name": in.file.Name,
				"target_language": lj.lang, "auto": true,
			}
			if assignedTo != nil {
				details["assignee_id"] = *assignedTo
				assignee, cached := assignees[*assignedTo]
				if !cached {
					if u, uErr := txq.GetUser(ctx, *assignedTo); uErr == nil {
						assignee = u
					}
					assignees[*assignedTo] = assignee
				}
				if assignee.Email != "" {
					details["assignee_email"] = assignee.Email
					details["assignee_name"] = projectevents.PersonName(assignee)
				}
			}
			if deadline.Valid {
				details["deadline"] = deadline.Time.UTC().Format(time.RFC3339)
			}
			jobID := lj.jobID
			if err := projectevents.EmitInTx(ctx, txq, projectevents.Event{
				ProjectID: in.project.ID,
				TenantID:  in.project.TenantID,
				Type:      projectevents.TypeStepAssigned,
				ActorID:   actorID,
				ActorName: actorName,
				FileID:    &in.file.ID,
				JobID:     &jobID,
				Details:   details,
			}); err != nil {
				return nil, fmt.Errorf("record assignment event: %w", err)
			}
		}
		if firstAssignmentID > 0 {
			if err := txq.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{
				ID: lj.jobID, CurrentStepID: &firstAssignmentID,
			}); err != nil {
				logging.Warn(ctx, err, "job current step not set", "upload-current-step-failed",
					logging.JobID(lj.jobID))
			}
			if err := stepround.Open(ctx, txq, lj.jobID, firstAssignmentID, stepround.Keep); err != nil {
				return nil, err
			}
		}
	}
	return wfSteps, nil
}

// dispatch queues the work the new jobs need — pre-translation per the
// project settings, predict-on-start for a QE+APE first step, the auto
// analysis — and copies the ingest timings onto every job. Runs after commit;
// returns the warnings for the response.
func (h *Handler) dispatch(ctx context.Context, in ingest, jobs []langJob, wfSteps []db.WorkflowStep, profile db.SettingsProfile, analyze bool, rec *timing.Recorder) []string {
	var warnings []string
	tenantID := in.project.TenantID
	uploader := auth.UserIDFromContext(ctx)

	// The two flags gate TM lookup and MT independently; either
	// alone is a valid mode, both off → nothing to queue. QP+APE runs when a
	// QE+APE workflow step starts, not here.
	phases := pipeline.TranslatePhases{UseTM: profile.TmOnUpload, UseMT: profile.MtOnUpload}
	pipelineEnqueued := h.enqueuer != nil && (phases.UseTM || phases.UseMT)
	if pipelineEnqueued {
		for _, lj := range jobs {
			if err := h.enqueuer.EnqueuePipeline(in.file.ID, in.project.SourceLanguage, lj.lang, lj.jobID, tenantID, phases, &uploader); err != nil {
				logging.Warn(ctx, err, "pre-translation not queued", "upload-pretranslate-enqueue-failed",
					logging.FileID(in.file.ID), logging.JobID(lj.jobID))
				warnings = append(warnings, fmt.Sprintf("pre-translation could not be queued for %s", lj.lang))
			}
		}
	}

	// A running pre-translation chains the AI step on
	// completion; enqueuing here too would race MT. Only when nothing else
	// will trigger it.
	if h.aiEnqueuer != nil && !pipelineEnqueued && len(wfSteps) > 0 && wfSteps[0].AiRouterID != nil {
		for _, lj := range jobs {
			if err := h.aiEnqueuer.EnqueueAIStep(lj.jobID, tenantID, &uploader); err != nil {
				logging.Warn(ctx, err, "AI step not queued", "upload-ai-step-enqueue-failed",
					logging.FileID(in.file.ID), logging.JobID(lj.jobID))
			}
		}
	}

	// One pending auto analysis per project, coalesced across the
	// files of an upload. Promotion self-gates on pretranslate.
	if h.analysisEnqueuer != nil && analyze {
		analysis.EnsurePendingAutoAnalysis(ctx, h.queries, tenantID, in.project.ID, in.project.SourceLanguage)
		analysis.PromoteProjectAutoAnalysisIfReady(ctx, h.queries, h.analysisEnqueuer, tenantID, in.project.ID)
	}

	// job_timings.job_id is an FK, so this has to follow the commit.
	for _, lj := range jobs {
		rec.Flush(ctx, h.queries, tenantID, lj.jobID)
	}
	return warnings
}

// MaterializeProjectFiles finishes every file the project collected while it
// was a draft: the stored source is read back and parsed again, its segments
// matched to the stored rows, and the deferred half runs exactly as it does
// for a live upload. Files that already have jobs are left alone, so a failed
// publish can simply be retried. Implements project.FileMaterializer.
func (h *Handler) MaterializeProjectFiles(ctx context.Context, tenantID, projectID int64, analyze bool) error {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	files, err := h.queries.ListFilesWithoutJobs(ctx, db.ListFilesWithoutJobsParams{ProjectID: projectID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("list draft files: %w", err)
	}
	profile, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return fmt.Errorf("get settings profile: %w", err)
	}
	for _, f := range files {
		if err := h.materializeStoredFile(ctx, project, profile, f, analyze); err != nil {
			return fmt.Errorf("file %d (%s): %w", f.ID, f.Name, err)
		}
	}
	return nil
}

// storedMatchesParse reports whether a draft file's stored segments are the
// ones a parse of its file gives: same count, keys, positions and text.
func storedMatchesParse(stored []db.Segment, segments []common.Segment) bool {
	if len(stored) != len(segments) {
		return false
	}
	for i, s := range stored {
		seg := segments[i]
		if int(s.Position) != seg.Position || s.SourceText != seg.Text || (s.SegmentKey == nil) != (seg.Key == "") ||
			(s.SegmentKey != nil && *s.SegmentKey != seg.Key) {
			return false
		}
	}
	return true
}

// replaceDraftSegments swaps a draft file's stored segments for segments, in
// tx, and returns their ids. A draft file has no jobs, so nothing else points
// at the rows it drops.
func (h *Handler) replaceDraftSegments(ctx context.Context, tx pgx.Tx, fileID int64, segments []common.Segment) ([]int64, error) {
	txq := db.New(tx)
	if err := txq.DeleteSegmentsByFile(ctx, fileID); err != nil {
		return nil, fmt.Errorf("clear draft segments: %w", err)
	}
	ids, err := h.batchInsertSegments(ctx, tx, fileID, segments)
	if err != nil {
		return nil, err
	}
	if err := txq.UpdateFileSegmentCount(ctx, db.UpdateFileSegmentCountParams{ID: fileID, SegmentCount: int32(len(segments))}); err != nil {
		return nil, fmt.Errorf("update segment count: %w", err)
	}
	return ids, nil
}

// ErrFileChanged refuses a file whose row changed after publish read it: its
// parse is stale. Publishing again reads it afresh.
var ErrFileChanged = errors.New("file changed while publishing")

// parseInputsChanged reports a change to the fields a stored file's parse
// reads: the stored bytes and the options it was uploaded with.
func parseInputsChanged(read, locked db.File) bool {
	return read.Format != locked.Format || read.StoragePath != locked.StoragePath ||
		read.InlineHtml != locked.InlineHtml || !bytes.Equal(read.XlsxLayout, locked.XlsxLayout)
}

func (h *Handler) materializeStoredFile(ctx context.Context, project db.Project, profile db.SettingsProfile, f db.File, analyze bool) error {
	timedCtx, rec := timing.WithRecorder(ctx)
	ingestCtx, doneIngest := timing.Start(timedCtx, timing.StageIngest, timing.Meta("format", string(f.Format)))

	content, err := h.storedContent(ingestCtx, f)
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("read stored file: %w", err)
	}
	// Parsed in the mode the file was uploaded with, not today's setting.
	layout, err := parsers.FileXLSXLayout(f.XlsxLayout)
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("read stored xlsx layout: %w", err)
	}
	p, err := parsers.GetParserWith(string(f.Format), parsers.ParseOptions{InlineHTML: f.InlineHtml, XLSXLayout: layout})
	if err != nil {
		doneIngest(err)
		return err
	}
	segments, err := p.Parse(content)
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("parse stored file: %w", err)
	}
	// The rows were inserted from a parse of the same file, in position order.
	// When they no longer match a parse today — stored by an older parser —
	// the draft's segments are replaced with the fresh parse below.
	stored, err := h.queries.ListSegmentsByFile(ingestCtx, f.ID)
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("list segments: %w", err)
	}
	in := ingest{project: project, file: f, format: string(f.Format), content: content, segments: segments}
	for _, seg := range segments {
		in.wordCount += wordcount.CountWords(seg.Text)
	}
	reparse := !storedMatchesParse(stored, segments)
	if !reparse {
		in.segmentIDs = make([]int64, len(stored))
		for i, s := range stored {
			in.segmentIDs[i] = s.ID
		}
	}

	tx, err := h.pool.Begin(ingestCtx)
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ingestCtx)
	// The file was read before the transaction; the lock makes this publish
	// wait for any other one, or for a layout change, holding the row.
	locked, err := db.New(tx).LockFileTenant(ingestCtx, db.LockFileTenantParams{ID: f.ID, TenantID: project.TenantID})
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("lock file: %w", err)
	}
	// A publish that ran meanwhile (a second click, the assistant and the page
	// at once) has published it already, whatever it wrote to the row.
	hasJobs, err := db.New(tx).FileHasJobs(ingestCtx, db.FileHasJobsParams{ID: f.ID, TenantID: project.TenantID})
	if err != nil {
		doneIngest(err)
		return fmt.Errorf("check jobs: %w", err)
	}
	if hasJobs {
		doneIngest(nil)
		return nil
	}
	// A change to what the parse above read (its layout, say) makes that parse
	// stale; the publish fails and a retry reads it again. Anything else, a
	// rename, publishes with the row as it is now.
	if parseInputsChanged(f, locked) {
		doneIngest(ErrFileChanged)
		return ErrFileChanged
	}
	in.file = locked
	if reparse {
		if in.segmentIDs, err = h.replaceDraftSegments(ingestCtx, tx, f.ID, segments); err != nil {
			doneIngest(err)
			return err
		}
	}
	jobs, wfSteps, warnings, err := h.materializeInTx(ingestCtx, tx, in)
	if err != nil {
		doneIngest(err)
		return err
	}
	// Publishing has no upload response to carry the warnings, so they are logged.
	for _, w := range warnings {
		logging.Warn(ingestCtx, nil, w, "file-bilingual-target-skipped", logging.FileID(f.ID))
	}
	if err := tx.Commit(ingestCtx); err != nil {
		doneIngest(err)
		return fmt.Errorf("commit: %w", err)
	}
	doneIngest(nil)
	h.dispatch(timedCtx, in, jobs, wfSteps, profile, analyze, rec)
	return nil
}
