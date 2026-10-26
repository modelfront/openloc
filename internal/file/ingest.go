package file

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/parsers"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xlsx"
	"openloc/internal/projectevents"
	"openloc/internal/timing"
	"openloc/internal/wordcount"
)

// IngestOptions are the upload dialog's choices.
type IngestOptions struct {
	// Analyze requests the automatic analysis once the jobs exist.
	Analyze bool
	// XLSXLayout is the uploader's column layout of an Excel workbook without
	// language codes in row 1; nil reads the workbook by its row 1.
	XLSXLayout *xlsx.XLSXLayout
}

// IngestResult is the stored file and any dispatch warnings (work that could
// not be queued after the file itself committed).
type IngestResult struct {
	File     db.File
	Warnings []string
}

// Ingest adds a document to a project — the upload behind POST
// /api/projects/{id}/files and the MCP attach_uploaded_file tool. It detects
// the format, parses the segments, stores the source, and in one transaction
// writes the file, its segments and — unless the project is a draft — the
// translations, one job per target language and their step assignments. Work
// that runs after commit (pre-translation, predict-on-start, auto analysis)
// is best-effort and reported as warnings. A draft's files get their jobs at
// publish (MaterializeProjectFiles).
//
// project must come from a tenant-scoped lookup. An unsupported or unreadable
// file comes back as httputil.ClientError.
func (h *Handler) Ingest(ctx context.Context, project db.Project, filename string, content []byte, opts IngestOptions) (IngestResult, error) {
	// Ingest is file-scoped but jobs are per target language, so the recorder
	// collects the stages once and is flushed per job at the end: every job's
	// timing row then starts at the user's upload.
	ctx, rec := timing.WithRecorder(ctx)

	// Format detection needs the content: .xml is ambiguous between Android
	// string resources and generic XML until the root element is read.
	format := common.DetectFormat(filename, content)
	if format == "" {
		if msg, ok := common.UnsupportedFormatMessage(filename); ok {
			return IngestResult{}, httputil.NewClientError(msg)
		}
		return IngestResult{}, httputil.NewClientError("unsupported file format")
	}
	settingsProfile, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return IngestResult{}, fmt.Errorf("get settings profile: %w", err)
	}
	// The file keeps the mode it is parsed with, so a later change of the
	// setting never re-reads it differently. The setting applies to the
	// InlineHTMLFormat formats only; every other file's strings are HTML.
	inlineHTML := settingsProfile.InlineHtml || !parsers.InlineHTMLFormat(format)
	layoutJSON, err := validateIngestLayout(format, content, opts.XLSXLayout)
	if err != nil {
		return IngestResult{}, err
	}
	p, err := parsers.GetParserWith(format, parsers.ParseOptions{InlineHTML: inlineHTML, XLSXLayout: opts.XLSXLayout})
	if err != nil {
		logging.Warn(ctx, err, "parser lookup failed", "upload-parser-lookup-failed")
		return IngestResult{}, httputil.NewClientError(fmt.Sprintf("file format %q is not supported", format))
	}
	// One span covers all of ingest: parse, storage upload, word count, the
	// inserts and job creation (internal/timing/stages.go).
	ctx, doneIngest := timing.Start(ctx, timing.StageIngest, timing.Meta("format", string(format)))

	segments, err := p.Parse(content)
	if err != nil {
		doneIngest(err)
		logging.Warn(ctx, err, "upload parse failed", "upload-parse-failed")
		if msg, ok := common.ImportMessage(err); ok {
			return IngestResult{}, httputil.NewClientError(msg)
		}
		return IngestResult{}, httputil.NewClientError(fmt.Sprintf("could not parse %s file — check that the file is well-formed", format))
	}

	// Storage is nil in tests without an object store.
	var storagePath string
	if h.storage != nil {
		storagePath, err = h.storage.Upload(ctx, project.ID, filename, bytes.NewReader(content))
		if err != nil {
			doneIngest(err)
			return IngestResult{}, fmt.Errorf("store file: %w", err)
		}
	}

	// A failed transaction leaves the stored object orphaned — cheap and
	// cleanable, unlike a file row without its source.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		doneIngest(err)
		return IngestResult{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)

	fileRecord, err := txq.CreateFile(ctx, db.CreateFileParams{
		ProjectID:    project.ID,
		Name:         filename,
		Format:       db.FileFormat(format),
		StoragePath:  storagePath,
		SegmentCount: int32(len(segments)),
		InlineHtml:   &inlineHTML,
		XlsxLayout:   layoutJSON,
	})
	if err != nil {
		doneIngest(err)
		return IngestResult{}, fmt.Errorf("create file record: %w", err)
	}

	// Word count via wordcount.CountWords.
	wordCount := 0
	for _, seg := range segments {
		wordCount += wordcount.CountWords(seg.Text)
	}
	segmentIDs, err := h.batchInsertSegments(ctx, tx, fileRecord.ID, segments)
	if err != nil {
		doneIngest(err)
		return IngestResult{}, fmt.Errorf("create segments: %w", err)
	}

	in := ingest{
		project: project, file: fileRecord, format: string(format), content: content,
		segments: segments, segmentIDs: segmentIDs, wordCount: wordCount,
	}

	// A draft's target languages are still open, so everything from the
	// translations on waits for publish.
	var jobs []langJob
	var wfSteps []db.WorkflowStep
	var warnings []string
	draft := project.Status == db.ProjectStatusDraft
	if !draft {
		if jobs, wfSteps, warnings, err = h.materializeInTx(ctx, tx, in); err != nil {
			doneIngest(err)
			return IngestResult{}, fmt.Errorf("create jobs: %w", err)
		}
	}
	// Ingest ends here; everything after this is dispatch.
	doneIngest(nil)

	// The upload event commits atomically with the file and jobs it describes.
	if err := projectevents.EmitInTx(ctx, txq, uploadEvent(ctx, project, fileRecord, string(format), len(content), len(segments), jobs, draft, settingsProfile)); err != nil {
		return IngestResult{}, fmt.Errorf("record upload event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, fmt.Errorf("commit: %w", err)
	}

	res := IngestResult{File: fileRecord}
	if !draft {
		res.Warnings = append(warnings, h.dispatch(ctx, in, jobs, wfSteps, settingsProfile, opts.Analyze, rec)...)
	}
	return res, nil
}

// uploadEvent is the project.file_uploaded audit event.
func uploadEvent(ctx context.Context, project db.Project, f db.File, format string, size, segmentCount int, jobs []langJob, draft bool, profile db.SettingsProfile) projectevents.Event {
	jobDetails := make([]map[string]any, 0, len(jobs))
	for _, lj := range jobs {
		jobDetails = append(jobDetails, map[string]any{"job_id": lj.jobID, "target_language": lj.lang})
	}
	details := map[string]any{
		"file_name":     f.Name,
		"format":        format,
		"size_bytes":    size,
		"segment_count": segmentCount,
		"jobs":          jobDetails,
	}
	if draft {
		details["draft"] = true
	}
	// What this upload queues for auto pre-translate; the per-job outcomes
	// arrive as job.pipeline_completed/_failed system events from the worker.
	if !draft && (profile.TmOnUpload || profile.MtOnUpload) {
		details["pretranslate"] = map[string]any{"tm": profile.TmOnUpload, "mt": profile.MtOnUpload}
	}
	actorID, actorName := projectevents.ActorFromContext(ctx)
	return projectevents.Event{
		ProjectID: project.ID,
		TenantID:  project.TenantID,
		Type:      projectevents.TypeFileUploaded,
		ActorID:   actorID,
		ActorName: actorName,
		FileID:    &f.ID,
		Details:   details,
	}
}

// batchInsertSegments inserts segments in batches using multi-row INSERT ... RETURNING id.
// Returns the IDs in the same order as the input segments.
func (h *Handler) batchInsertSegments(ctx context.Context, dbtx db.DBTX, fileID int64, segments []common.Segment) ([]int64, error) {
	allIDs := make([]int64, 0, len(segments))

	for batch := range slices.Chunk(segments, segmentBatchSize) {

		var b strings.Builder
		b.WriteString("INSERT INTO segments (file_id, source_text, segment_key, position, context, word_count, source_rich_text) VALUES ")
		args := make([]any, 0, len(batch)*7)
		for i, seg := range batch {
			if i > 0 {
				b.WriteString(", ")
			}
			base := i * 7
			fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			var richText *string
			if seg.RichText != "" {
				richText = &seg.RichText
			}
			args = append(args, fileID, seg.Text, seg.Key, int32(seg.Position), seg.Context, int32(wordcount.CountWords(seg.Text)), richText)
		}
		b.WriteString(" RETURNING id")

		rows, err := dbtx.Query(ctx, b.String(), args...)
		if err != nil {
			return nil, fmt.Errorf("batch insert segments: %w", err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			allIDs = append(allIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return allIDs, nil
}

// batchInsertTranslations inserts translations for all segments × target languages
// in batches using multi-row INSERT. For segments with pre-existing targets (e.g. XLIFF),
// it also sets active_text and origin='file_import' on the stampLang translation
// (see bilingualTarget); "" stamps none.
func (h *Handler) batchInsertTranslations(ctx context.Context, dbtx db.DBTX, segmentIDs []int64, segments []common.Segment, targetLangs []string, stampLang string) error {
	// Build all (segmentID, lang, status) tuples. Every new row
	// inserts at status='needs_review'. Rows with a
	// pre-existing target get active_text + origin='file_import' stamped by the
	// follow-up loop below, which makes them *derive* as pre_translated; rows
	// with no target keep active_text NULL → derive as untranslated.
	type translationRow struct {
		segmentID int64
		lang      string
		status    string
	}
	allRows := make([]translationRow, 0, len(segmentIDs)*len(targetLangs))
	for _, sid := range segmentIDs {
		for _, lang := range targetLangs {
			allRows = append(allRows, translationRow{sid, lang, string(db.TranslationStatusNeedsReview)})
		}
	}

	// Batch-insert translations
	const translationBatchSize = 500 // 500 rows * 3 cols = 1500 params
	for batch := range slices.Chunk(allRows, translationBatchSize) {

		var b strings.Builder
		b.WriteString("INSERT INTO translations (segment_id, target_language, status) VALUES ")
		args := make([]any, 0, len(batch)*3)
		for i, r := range batch {
			if i > 0 {
				b.WriteString(", ")
			}
			base := i * 3
			fmt.Fprintf(&b, "($%d, $%d, $%d)", base+1, base+2, base+3)
			args = append(args, r.segmentID, r.lang, r.status)
		}

		if _, err := dbtx.Exec(ctx, b.String(), args...); err != nil {
			return fmt.Errorf("batch insert translations: %w", err)
		}
	}

	// For segments with pre-existing targets (XLIFF imports), update the
	// stampLang translation with origin='file_import' + active_text/active_rich_text.
	for i, seg := range segments {
		if seg.Target == "" || stampLang == "" {
			continue
		}
		var richTextPtr *string
		if seg.TargetRichText != "" {
			richTextPtr = &seg.TargetRichText
		}
		_, err := dbtx.Exec(ctx,
			`UPDATE translations SET origin = 'file_import',
			 active_text = $1, active_rich_text = $2
			 WHERE segment_id = $3 AND target_language = $4`,
			seg.Target, richTextPtr, segmentIDs[i], stampLang)
		if err != nil {
			return fmt.Errorf("update pre-existing translation: %w", err)
		}
	}

	return nil
}

// validateIngestLayout checks an upload's Excel column layout against the
// workbook and returns it as stored; nil without one.
func validateIngestLayout(format string, content []byte, layout *xlsx.XLSXLayout) ([]byte, error) {
	if layout == nil {
		return nil, nil
	}
	if format != "xlsx" {
		return nil, httputil.NewClientError("an Excel column layout applies to .xlsx files only")
	}
	return checkXLSXLayout(content, layout)
}

// checkXLSXLayout validates a layout against the workbook and encodes it for
// the file row. A layout the uploader can fix is a httputil.ClientError.
func checkXLSXLayout(content []byte, layout *xlsx.XLSXLayout) ([]byte, error) {
	layout.Normalize()
	if err := layout.Validate(content); err != nil {
		var le *common.XLSXLayoutError
		if errors.As(err, &le) {
			return nil, httputil.NewClientError(le.Msg)
		}
		return nil, httputil.NewClientError("could not read the xlsx file — check that the file is well-formed")
	}
	raw, err := json.Marshal(layout)
	if err != nil {
		return nil, fmt.Errorf("encode xlsx layout: %w", err)
	}
	return raw, nil
}
