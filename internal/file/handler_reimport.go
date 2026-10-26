package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/parsers"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xliff"
	"openloc/internal/taskedit"
)

// langCodeMaxLen bounds raw `lang` input before normalization. BCP-47 codes are
// always under this size; anything bigger is malformed/abusive (length-bounding
// keeps a hostile caller from spamming the error log via the response echo).
const langCodeMaxLen = 35

// MaxReimportSegments is the hard cap on segments per re-import request. A
// re-import does one UPDATE per segment inside a single transaction; on
// commodity Postgres this completes in ~1ms per row, so the cap targets a
// worst-case ~50s sync run. Above this, requests are rejected with 413 to
// avoid HTTP timeouts and to bound transaction-lock duration. Larger jobs
// belong on the (deferred) async path
const MaxReimportSegments = 50000

// ReimportTarget is a file and target language a re-import may reconcile,
// resolved tenant-scoped.
type ReimportTarget struct {
	File           db.File
	Project        db.Project
	TargetLanguage string
}

// ReimportOptions: DryRun computes the report without applying; Force
// overrides approved segments (locked ones never).
type ReimportOptions struct {
	DryRun bool
	Force  bool
}

// TooManySegmentsError refuses an XLIFF over MaxReimportSegments.
type TooManySegmentsError struct{ Count int }

func (e *TooManySegmentsError) Error() string {
	return fmt.Sprintf("XLIFF has %d segments; re-import is capped at %d. Split the file into smaller XLIFFs and re-import each.", e.Count, MaxReimportSegments)
}

// ResolveReimportTarget checks that the file is the tenant's and that rawLang
// is one of its project's target languages. Shared by the REST re-import and
// the MCP reimport_xliff tool; caller mistakes are httputil.ClientError, an
// unknown or foreign file pgx.ErrNoRows.
func (h *Handler) ResolveReimportTarget(ctx context.Context, tenantID, fileID int64, rawLang string) (ReimportTarget, error) {
	f, err := h.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return ReimportTarget{}, err
	}
	rawLang = strings.TrimSpace(rawLang)
	if rawLang == "" {
		return ReimportTarget{}, httputil.NewClientError("lang query param is required")
	}
	// Never echo malformed input back into the response or a log line.
	if len(rawLang) > langCodeMaxLen {
		return ReimportTarget{}, httputil.NewClientError("invalid language code")
	}
	targetLang := langcode.Normalize(rawLang)
	if targetLang == "" {
		return ReimportTarget{}, httputil.NewClientError("invalid language code")
	}
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: f.ProjectID, TenantID: tenantID})
	if err != nil {
		return ReimportTarget{}, fmt.Errorf("get project: %w", err)
	}
	if !slices.Contains(project.TargetLanguages, targetLang) {
		return ReimportTarget{}, httputil.NewClientError(fmt.Sprintf("target language %q is not in the project's target_languages", targetLang))
	}
	if err := h.requireLinguistLangAccess(ctx, f.ID, targetLang); err != nil {
		return ReimportTarget{}, err
	}
	return ReimportTarget{File: f, Project: project, TargetLanguage: targetLang}, nil
}

// requireLinguistLangAccess lets a linguist import only into a task assigned
// to them. The file route's check passes on any of the file's tasks, and an
// import writes one language. Every other role passes.
func (h *Handler) requireLinguistLangAccess(ctx context.Context, fileID int64, targetLang string) error {
	if auth.RoleFromContext(ctx) != auth.RoleLinguist {
		return nil
	}
	assigned, err := auth.LinguistMayAccessFileLanguage(ctx, h.queries, fileID, targetLang)
	if err != nil {
		return fmt.Errorf("verify job assignment: %w", err)
	}
	if !assigned {
		return httputil.NewForbiddenError("this task is not assigned to you")
	}
	return nil
}

// ReimportXLIFFFile parses an externally edited XLIFF and reconciles it
// against the target's translations (no-op on empty target, status
// flip to needs_review, atomic apply in one transaction). The XLIFF's own
// target-language must match when present, so a German file cannot overwrite
// French translations.
func (h *Handler) ReimportXLIFFFile(ctx context.Context, t ReimportTarget, filename string, content []byte, opts ReimportOptions) (*ReimportXLIFFResult, error) {
	format := common.FormatFromFilename(filename)
	if format != "xliff" {
		return nil, httputil.NewClientError("re-import requires an XLIFF file (.xliff, .xlf, .sdlxliff, .mxliff, .mqxliff)")
	}
	xliffParser, err := parsers.GetParser(format)
	if err != nil {
		logging.Warn(ctx, err, "re-import parser lookup failed", "reimport-parser-lookup-failed", logging.FileID(t.File.ID))
		return nil, httputil.NewClientError("re-import requires an XLIFF file")
	}
	segments, err := xliffParser.Parse(content)
	if err != nil {
		logging.Warn(ctx, err, "re-import XLIFF parse failed", "reimport-parse-failed", logging.FileID(t.File.ID))
		return nil, httputil.NewClientError("could not parse XLIFF — check that the file is well-formed")
	}
	if len(segments) > MaxReimportSegments {
		return nil, &TooManySegmentsError{Count: len(segments)}
	}
	if xliffLang, langErr := xliff.XLIFFTargetLanguage(content); langErr == nil && xliffLang != "" {
		if normalized := langcode.Normalize(xliffLang); normalized != t.TargetLanguage {
			return nil, httputil.NewClientError(fmt.Sprintf("XLIFF target-language %q does not match requested lang %q", normalized, t.TargetLanguage))
		}
	}

	// A re-import writes text into the job's current task, so the task edit
	// guard applies. The check refuses up front; the reopen of a completed
	// task runs inside the re-import transaction, so a run that writes nothing
	// (dry run, force required) reopens nothing.
	var task taskedit.Result
	var beforeWrite func(context.Context, pgx.Tx) error
	job, err := h.queries.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: t.File.ID, TargetLanguage: t.TargetLanguage})
	switch {
	case err == nil:
		edit, err := h.guard.Check(ctx, t.Project.TenantID, taskedit.Ref{JobID: job.ID}, taskedit.Opts{})
		if err != nil {
			return nil, err
		}
		beforeWrite = func(ctx context.Context, tx pgx.Tx) (err error) {
			task, err = h.guard.ApplyInTx(ctx, tx, t.Project.TenantID, edit)
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("resolve job: %w", err)
	}

	result, err := ReimportXLIFF(ctx, h.pool, ReimportXLIFFRequest{
		FileID:           t.File.ID,
		TargetLanguage:   t.TargetLanguage,
		IncomingSegments: segments,
		DryRun:           opts.DryRun,
		Force:            opts.Force,
		EditedBy:         auth.UserIDFromContext(ctx),
		ProjectID:        t.Project.ID,
		TenantID:         t.Project.TenantID,
		FileName:         t.File.Name,
		ActorEmail:       auth.UserEmailFromContext(ctx),
		BeforeWrite:      beforeWrite,
	})
	if err != nil {
		// Duplicate trans-unit keys are the user's input problem; name them.
		var dupErr *DuplicateKeysError
		if errors.As(err, &dupErr) {
			return nil, httputil.NewClientError(fmt.Sprintf("duplicate trans-unit IDs in upload — first occurrences would be lost. Keys: %v", dupErr.Keys))
		}
		return nil, fmt.Errorf("reimport xliff: %w", err)
	}
	h.guard.AfterCommit(ctx, t.Project.TenantID, task)
	result.TaskReopened = task.Reopened
	return result, nil
}

// HandleReimportXLIFF re-imports an XLIFF file after external editing and
// returns the conflict report. Open to every role; a linguist only into a
// task assigned to them (requireLinguistLangAccess).
//
// Query params: lang (required), dry_run, force (see ReimportOptions), and
// revision — an optional optimistic lock echoed from
// the export response; a mismatch with file.updated_at is a 409 carrying
// current_revision. Body: multipart `file`.
func (h *Handler) HandleReimportXLIFF(w http.ResponseWriter, r *http.Request) {
	fileID, err := httputil.ParseID(r, "fileID")
	if err != nil {
		httputil.BadRequest(w, "invalid file ID")
		return
	}
	target, err := h.ResolveReimportTarget(r.Context(), auth.TenantIDFromContext(r.Context()), fileID, r.URL.Query().Get("lang"))
	if err != nil {
		httputil.ServiceError(w, err, "reimport", "file")
		return
	}

	if rawRev := strings.TrimSpace(r.URL.Query().Get("revision")); rawRev != "" {
		clientRev, parseErr := time.Parse(time.RFC3339Nano, rawRev)
		if parseErr != nil {
			httputil.BadRequest(w, "revision must be an RFC 3339 timestamp")
			return
		}
		updatedAt := target.File.UpdatedAt
		if !updatedAt.Valid || !clientRev.Equal(updatedAt.Time) {
			currentRev := ""
			if updatedAt.Valid {
				currentRev = updatedAt.Time.Format(time.RFC3339Nano)
			}
			httputil.JSON(w, http.StatusConflict, map[string]any{
				"error":            "file revision mismatch — refresh and retry",
				"current_revision": currentRev,
			})
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	if err := r.ParseMultipartForm(UploadMemThreshold); err != nil {
		httputil.BadRequest(w, "file too large or invalid multipart form")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		httputil.ServerError(w, "read", "file")
		return
	}

	result, err := h.ReimportXLIFFFile(r.Context(), target, header.Filename, content, ReimportOptions{
		DryRun: strings.EqualFold(r.URL.Query().Get("dry_run"), "true"),
		Force:  strings.EqualFold(r.URL.Query().Get("force"), "true"),
	})
	if err != nil {
		// 413: well-formed but past what one synchronous request may apply.
		var tooMany *TooManySegmentsError
		if errors.As(err, &tooMany) {
			w.Header().Set("X-Reimport-Max-Segments", fmt.Sprintf("%d", MaxReimportSegments))
			http.Error(w, tooMany.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		if _, ok := httputil.AsClientError(err); !ok {
			logging.Error(r.Context(), err, "reimport-xliff failed", "reimport-xliff-failed", logging.FileID(fileID))
		}
		httputil.ServiceError(w, err, "reimport", "xliff")
		return
	}
	httputil.JSON(w, http.StatusOK, taskedit.WithReopened(map[string]any{"data": result}, result.TaskReopened))
}
