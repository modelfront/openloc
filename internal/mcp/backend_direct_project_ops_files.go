package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/label"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
	"openloc/internal/sliceutil"
)

// --- Files ------------------------------------------------------------------

// fileRow is the tenant-scoped file lookup for file-taking tools. A linguist
// additionally needs an assignment to the file, as on the /api/files routes
// (auth.RequireLinguistFileAccess).
func (b *directBackend) fileRow(ctx context.Context, fileID int64) (db.File, error) {
	tenantID, _ := scope(ctx)
	f, err := b.q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.File{}, errNotFound
		}
		return db.File{}, fmt.Errorf("get file: %w", err)
	}
	ok, err := auth.LinguistMayAccessFile(ctx, b.q, f.ID)
	if err != nil {
		return db.File{}, fmt.Errorf("check file assignment: %w", err)
	}
	if !ok {
		return db.File{}, errNotFound
	}
	return f, nil
}

func fileDetail(f db.File) FileDetail {
	return FileDetail{
		ID: f.ID, ProjectID: f.ProjectID, Name: f.Name, Format: string(f.Format), SegmentCount: f.SegmentCount,
		CreatedAt: tsString(f.CreatedAt), UpdatedAt: tsString(f.UpdatedAt), HasSource: f.StoragePath != "",
	}
}

func (b *directBackend) GetFile(ctx context.Context, in FileRef) (FileDetail, error) {
	f, err := b.fileRow(ctx, in.FileID)
	if err != nil {
		return FileDetail{}, err
	}
	return fileDetail(f), nil
}

func (b *directBackend) RenameFile(ctx context.Context, in RenameFileIn) (FileDetail, error) {
	tenantID, _ := scope(ctx)
	f, err := file.Rename(ctx, b.q, tenantID, in.FileID, in.Name)
	if err != nil {
		return FileDetail{}, serviceErr(err, "rename file")
	}
	return fileDetail(f), nil
}

func (b *directBackend) files() (*file.Handler, error) {
	if b.ops.Files == nil {
		return nil, invalidf("file handling is not available on this server")
	}
	return b.ops.Files, nil
}

// readUpload reads an assistant upload whole, refusing one past the project
// upload limit.
func (b *directBackend) readUpload(ctx context.Context, uploadID string) (db.ChatUpload, []byte, error) {
	row, body, err := b.openUpload(ctx, uploadID)
	if err != nil {
		return db.ChatUpload{}, nil, err
	}
	defer body.Close()
	content, err := io.ReadAll(io.LimitReader(body, file.MaxUploadSize+1))
	if err != nil {
		return db.ChatUpload{}, nil, fmt.Errorf("read upload: %w", err)
	}
	if len(content) > file.MaxUploadSize {
		return db.ChatUpload{}, nil, invalidf("the file is larger than the 50 MB upload limit")
	}
	return row, content, nil
}

func (b *directBackend) AttachUploadedFile(ctx context.Context, in AttachUploadedFileIn) (AttachedFileOut, error) {
	fh, err := b.files()
	if err != nil {
		return AttachedFileOut{}, err
	}
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return AttachedFileOut{}, err
	}
	return b.attachUpload(ctx, fh, p, in.UploadID, file.IngestOptions{Analyze: in.Analyze, XLSXLayout: in.XLSXLayout})
}

func (b *directBackend) attachUpload(ctx context.Context, fh *file.Handler, p db.Project, uploadID string, opts file.IngestOptions) (AttachedFileOut, error) {
	upload, content, err := b.readUpload(ctx, uploadID)
	if err != nil {
		return AttachedFileOut{}, err
	}
	return ingestIntoProject(ctx, fh, p, upload.Filename, content, opts)
}

// attachBatchBudget is how long a batch keeps starting new files. The chat
// gives a whole turn 120 s, model calls included, so a batch that stops here
// still returns its rows in time; the files it did not start say so.
var attachBatchBudget = 60 * time.Second

// AttachUploadedFiles adds each upload on its own, as the project page does
// with several files: a file that fails gets its reason in its row and the
// others are still added. Only when every file fails is the call an error.
func (b *directBackend) AttachUploadedFiles(ctx context.Context, in AttachUploadedFilesIn) (AttachedFilesOut, error) {
	if len(in.Files) == 0 || len(in.Files) > MaxAttachFiles {
		return AttachedFilesOut{}, invalidf("files takes 1 to %d attached files", MaxAttachFiles)
	}
	seen := make(map[string]bool, len(in.Files))
	for _, f := range in.Files {
		if seen[f.UploadID] {
			return AttachedFilesOut{}, invalidf("upload_id %s is listed twice", f.UploadID)
		}
		seen[f.UploadID] = true
	}
	fh, err := b.files()
	if err != nil {
		return AttachedFilesOut{}, err
	}
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return AttachedFilesOut{}, err
	}
	out := AttachedFilesOut{Files: make([]AttachedFileResult, 0, len(in.Files)), Draft: p.Status == db.ProjectStatusDraft}
	var reasons []string
	started := time.Now()
	for i, f := range in.Files {
		row := AttachedFileResult{UploadID: f.UploadID, Warnings: []string{}}
		if i > 0 && time.Since(started) > attachBatchBudget {
			row.NotStarted = true
			row.Error = "not started: this call ran out of time; ask the user to send this file again in a new message"
			reasons = append(reasons, f.UploadID+": "+row.Error)
			out.Files = append(out.Files, row)
			continue
		}
		res, err := b.attachUpload(ctx, fh, p, f.UploadID, file.IngestOptions{Analyze: in.Analyze, XLSXLayout: f.XLSXLayout})
		if err != nil {
			row.Error = attachErrorText(ctx, err)
			reasons = append(reasons, f.UploadID+": "+row.Error)
		} else {
			row.File, row.Warnings = &res.File, res.Warnings
			out.Added++
		}
		out.Files = append(out.Files, row)
	}
	if out.Added == 0 {
		return AttachedFilesOut{}, invalidf("no file was added:\n%s", strings.Join(reasons, "\n"))
	}
	return out, nil
}

// attachErrorText is the text one row of a batch shows, reduced as a whole
// tool error is: not-found stays opaque, a caller mistake keeps its own
// message, and anything else is logged and stays generic.
func attachErrorText(ctx context.Context, err error) string {
	var inv errInvalid
	switch {
	case errors.Is(err, errNotFound):
		return notFoundText
	case errors.As(err, &inv):
		return inv.msg
	default:
		logging.Error(ctx, err, "MCP attach of one upload failed", "mcp-attach-upload-failed")
		return "the file could not be added; the error has been logged"
	}
}

// ingestIntoProject adds a document to a project as an upload on the project
// page does, and answers with the new file.
func ingestIntoProject(ctx context.Context, fh *file.Handler, p db.Project, filename string, content []byte, opts file.IngestOptions) (AttachedFileOut, error) {
	res, err := fh.Ingest(ctx, p, filename, content, opts)
	if err != nil {
		return AttachedFileOut{}, serviceErr(err, "attach file")
	}
	return AttachedFileOut{File: fileDetail(res.File), Draft: p.Status == db.ProjectStatusDraft, Warnings: sliceutil.NonNil(res.Warnings)}, nil
}

// reimport resolves the target and runs the re-import — dry-run for the
// preview, for real on confirm — so both validate identically.
func (b *directBackend) reimport(ctx context.Context, in ReimportXLIFFIn, dryRun bool) (file.ReimportTarget, *file.ReimportXLIFFResult, error) {
	fh, err := b.files()
	if err != nil {
		return file.ReimportTarget{}, nil, err
	}
	tenantID, _ := scope(ctx)
	t, err := fh.ResolveReimportTarget(ctx, tenantID, in.FileID, in.TargetLanguage)
	if err != nil {
		return file.ReimportTarget{}, nil, serviceErr(err, "resolve re-import")
	}
	upload, content, err := b.readUpload(ctx, in.UploadID)
	if err != nil {
		return file.ReimportTarget{}, nil, err
	}
	res, err := fh.ReimportXLIFFFile(ctx, t, upload.Filename, content, file.ReimportOptions{
		DryRun: dryRun, Force: in.Force,
	})
	if err != nil {
		var tooMany *file.TooManySegmentsError
		if errors.As(err, &tooMany) {
			return file.ReimportTarget{}, nil, invalidf("%s", tooMany.Error())
		}
		return file.ReimportTarget{}, nil, serviceErr(err, "re-import XLIFF")
	}
	return t, res, nil
}

func (b *directBackend) PreviewReimportXLIFF(ctx context.Context, in ReimportXLIFFIn) (Preview, error) {
	t, res, err := b.reimport(ctx, in, true)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Summary: fmt.Sprintf("Update the %s translations of “%s” from the attached XLIFF: %s.",
		t.TargetLanguage, t.File.Name, plural(res.Updated, "segment changes", "segments change"))}
	if res.ForceRequired && !in.Force {
		p.Summary = fmt.Sprintf("Nothing would change in “%s”: the XLIFF edits %s. Call again with force=true to overwrite them.",
			t.File.Name, plural(res.SkippedApproved, "approved segment", "approved segments"))
	}
	if res.SkippedLocked > 0 {
		p.Effects = append(p.Effects, plural(res.SkippedLocked, "locked segment is skipped", "locked segments are skipped")+".")
	}
	if res.SkippedHeld > 0 {
		p.Effects = append(p.Effects, plural(res.SkippedHeld, "segment another user has open is skipped", "segments another user has open are skipped")+".")
	}
	if res.SkippedApproved > 0 && in.Force {
		p.Effects = append(p.Effects, plural(res.SkippedApproved, "approved segment is overwritten", "approved segments are overwritten")+".")
	}
	if res.MissingFromUpload > 0 {
		p.Effects = append(p.Effects, plural(res.MissingFromUpload, "segment is missing from the XLIFF and stays as it is", "segments are missing from the XLIFF and stay as they are")+".")
	}
	if res.ExtraInUpload > 0 {
		p.Effects = append(p.Effects, plural(res.ExtraInUpload, "unit in the XLIFF matches no segment and is ignored", "units in the XLIFF match no segment and are ignored")+".")
	}
	if res.Updated > 0 {
		p.Effects = append(p.Effects, "Changed segments go back to needs review.")
	}
	return p, nil
}

func (b *directBackend) ReimportXLIFF(ctx context.Context, in ReimportXLIFFIn) (ReimportOut, error) {
	_, res, err := b.reimport(ctx, in, false)
	if err != nil {
		return ReimportOut{}, err
	}
	return ReimportOut{
		Applied: res.Applied, ForceRequired: res.ForceRequired, Updated: res.Updated,
		SkippedLocked: res.SkippedLocked, SkippedHeld: res.SkippedHeld, SkippedApproved: res.SkippedApproved,
		UntouchedNoChange: res.UntouchedNoChange, ExtraInUpload: res.ExtraInUpload,
		MissingFromUpload: res.MissingFromUpload,
		SkippedLockedKeys: sliceutil.NonNil(res.SkippedLockedKeys), SkippedHeldKeys: sliceutil.NonNil(res.SkippedHeldKeys),
		SkippedApprovedKeys: sliceutil.NonNil(res.SkippedApprovedKeys),
		ExtraInUploadKeys:   sliceutil.NonNil(res.ExtraInUploadKeys), MissingFromUploadKeys: sliceutil.NonNil(res.MissingFromUploadKeys),
	}, nil
}

func (b *directBackend) ExportFile(ctx context.Context, in ExportFileIn) (ExportFileOut, error) {
	f, err := b.fileRow(ctx, in.FileID)
	if err != nil {
		return ExportFileOut{}, err
	}
	tenantID, _ := scope(ctx)
	p, err := b.q.GetProject(ctx, db.GetProjectParams{ID: f.ProjectID, TenantID: tenantID})
	if err != nil {
		return ExportFileOut{}, fmt.Errorf("get project: %w", err)
	}
	lang := langcode.Normalize(in.TargetLanguage)
	if !slices.Contains(p.TargetLanguages, lang) {
		return ExportFileOut{}, invalidf("target_language must be one of the project's target languages: %v", p.TargetLanguages)
	}
	// A linguist reads only a language assigned to them, as on the export route.
	ok, err := auth.LinguistMayAccessFileLanguage(ctx, b.q, f.ID, lang)
	if err != nil {
		return ExportFileOut{}, fmt.Errorf("verify language assignment: %w", err)
	}
	if !ok {
		return ExportFileOut{}, invalidf("this language is not assigned to you")
	}
	q := url.Values{"lang": {lang}}
	var filename string
	xliff := false
	switch in.Format {
	case "", "source":
		filename = pipeline.SourceExportFilename(f.Name, lang)
	case "xliff":
		xliff = true
		q.Set("format", "xliff")
		filename = pipeline.BilingualXLIFFFilename(f.Name, lang)
	default:
		return ExportFileOut{}, invalidf("format must be source or xliff")
	}
	out := ExportFileOut{DownloadOut: DownloadOut{DownloadPath: fmt.Sprintf("/api/files/%d/export?%s", f.ID, q.Encode()), Filename: filename}}
	if in.IncludeContent {
		if out.Content, out.ContentError, err = b.exportContent(ctx, f, p.SourceLanguage, lang, xliff); err != nil {
			return ExportFileOut{}, err
		}
	}
	return out, nil
}

func (b *directBackend) DownloadSource(ctx context.Context, in FileRef) (DownloadOut, error) {
	f, err := b.fileRow(ctx, in.FileID)
	if err != nil {
		return DownloadOut{}, err
	}
	if f.StoragePath == "" {
		return DownloadOut{}, invalidf("the original upload was not stored for this file")
	}
	return DownloadOut{DownloadPath: fmt.Sprintf("/api/files/%d/download", f.ID), Filename: f.Name}, nil
}

// --- Labels -----------------------------------------------------------------

func labelOut(l label.Label) LabelOut {
	out := LabelOut{ID: l.ID, Name: l.Name, System: l.System, InUse: l.InUse, Values: make([]LabelValueOut, 0, len(l.Values))}
	for _, v := range l.Values {
		out.Values = append(out.Values, LabelValueOut{ID: v.ID, Value: v.Value, InUse: v.InUse})
	}
	return out
}

func (b *directBackend) ListLabels(ctx context.Context, _ ListLabelsIn) (LabelsOut, error) {
	tenantID, _ := scope(ctx)
	labels, err := label.List(ctx, b.q, tenantID)
	if err != nil {
		return LabelsOut{}, err
	}
	out := LabelsOut{Labels: make([]LabelOut, 0, len(labels))}
	for _, l := range labels {
		out.Labels = append(out.Labels, labelOut(l))
	}
	return out, nil
}

func (b *directBackend) CreateLabel(ctx context.Context, in CreateLabelIn) (LabelOut, error) {
	if err := b.needPool("creating a label"); err != nil {
		return LabelOut{}, err
	}
	tenantID, _ := scope(ctx)
	l, err := label.Create(ctx, b.pool, tenantID, in.Name, in.Values)
	if err != nil {
		return LabelOut{}, serviceErr(err, "create label")
	}
	return labelOut(l), nil
}

func (b *directBackend) AddLabelValue(ctx context.Context, in AddLabelValueIn) (LabelValueOut, error) {
	tenantID, _ := scope(ctx)
	v, err := label.AddValue(ctx, b.q, tenantID, in.LabelID, in.Value)
	if err != nil {
		return LabelValueOut{}, serviceErr(err, "add label value")
	}
	return LabelValueOut{ID: v.ID, Value: v.Value}, nil
}

func (b *directBackend) PreviewDeleteLabel(ctx context.Context, in DeleteLabelIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	if err := label.CheckDeletable(ctx, b.q, tenantID, in.LabelID); err != nil {
		return Preview{}, serviceErr(err, "check label")
	}
	def, err := b.q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: in.LabelID, TenantID: tenantID})
	if err != nil {
		return Preview{}, serviceErr(err, "get label")
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the label “%s” and its values.", def.Name),
		Effects: []string{"No project uses it; it disappears from the project and template forms."},
	}, nil
}

func (b *directBackend) DeleteLabel(ctx context.Context, in DeleteLabelIn) (DeletedOut, error) {
	tenantID, _ := scope(ctx)
	if err := label.Delete(ctx, b.q, tenantID, in.LabelID); err != nil {
		return DeletedOut{}, serviceErr(err, "delete label")
	}
	return DeletedOut{Deleted: true}, nil
}

func (b *directBackend) PreviewDeleteLabelValue(ctx context.Context, in DeleteLabelValueIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	v, count, err := label.ValueForDelete(ctx, b.q, tenantID, in.ValueID)
	if err != nil {
		return Preview{}, serviceErr(err, "check label value")
	}
	def, err := b.q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: v.LabelDefinitionID, TenantID: tenantID})
	if err != nil {
		return Preview{}, serviceErr(err, "get label")
	}
	effect := "No project uses it; it is no longer offered on the project and template forms."
	if count > 0 {
		effect = fmt.Sprintf("Set on %d project(s); it is removed from them, and from any template that presets it.", count)
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the value “%s” from the label “%s”.", v.Value, def.Name),
		Effects: []string{effect},
	}, nil
}

func (b *directBackend) DeleteLabelValue(ctx context.Context, in DeleteLabelValueIn) (DeletedOut, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("deleting a label value"); err != nil {
		return DeletedOut{}, err
	}
	if err := label.DeleteValue(ctx, b.pool, tenantID, in.ValueID); err != nil {
		return DeletedOut{}, serviceErr(err, "delete label value")
	}
	return DeletedOut{Deleted: true}, nil
}
