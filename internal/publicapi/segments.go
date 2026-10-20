package publicapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/stepround"
	"openloc/internal/translation"
)

const segmentListMaxLimit = 200

type segmentCtxKey struct{}

// segmentRef is the job and translation a /jobs/{jobID}/segments/{translationID}
// route works on, resolved tenant-scoped by requireSegmentInJob.
type segmentRef struct {
	job         db.Job
	translation db.Translation
}

// requireSegmentInJob makes a segment route answer 404 unless the translation
// belongs to that job — same file, same target language — in the caller's
// tenant. The job's own gates run before it.
func (h Handlers) requireSegmentInJob(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenantID := auth.TenantIDFromContext(ctx)
		jobID, err := httputil.ParseID(r, "jobID")
		if err != nil {
			httputil.BadRequest(w, "invalid job ID")
			return
		}
		id, err := httputil.ParseID(r, "translationID")
		if err != nil {
			httputil.BadRequest(w, "invalid segment ID")
			return
		}
		job, err := h.Queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
		if httputil.DBError(w, err, "job") {
			return
		}
		tr, err := h.Queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: id, TenantID: tenantID})
		if httputil.DBError(w, err, "segment") {
			return
		}
		fileID, err := h.Queries.GetTranslationFileID(ctx, id)
		if httputil.DBError(w, err, "segment") {
			return
		}
		if fileID != job.FileID || tr.TargetLanguage != job.TargetLanguage {
			httputil.NotFound(w, "segment")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, segmentCtxKey{}, segmentRef{job: job, translation: tr})))
	})
}

func segmentFromContext(r *http.Request) segmentRef {
	ref, _ := r.Context().Value(segmentCtxKey{}).(segmentRef)
	return ref
}

var segmentStatuses = map[string]bool{
	string(db.TranslationStatusNeedsReview): true, string(db.TranslationStatusAutoApproved): true, string(db.TranslationStatusApproved): true,
}

func (h Handlers) listSegments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	job, err := h.Queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if httputil.DBError(w, err, "job") {
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status != "" && !segmentStatuses[status] {
		httputil.FieldError(w, http.StatusBadRequest, "status", "status must be needs_review, auto_approved or approved")
		return
	}
	page := httputil.ParseCursor(r, segmentListMaxLimit)
	rows, err := h.Queries.ListJobSegmentsPage(ctx, db.ListJobSegmentsPageParams{
		TargetLanguage: job.TargetLanguage,
		FileID:         job.FileID,
		TenantID:       tenantID,
		Status:         status,
		Query:          strings.TrimSpace(q.Get("q")),
		Cursor:         int32(page.Cursor),
		RowLimit:       page.Limit + 1, // +1 to detect has_more
	})
	if err != nil {
		httputil.ServerError(w, "list", "segments")
		return
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.TranslationID
	}
	previous, err := stepround.PreviousByTranslation(ctx, h.Queries, tenantID, ids)
	if err != nil {
		logging.Error(ctx, err, "previous step statuses read failed", "v1-segments-previous-step-failed", logging.JobID(job.ID))
		httputil.ServerError(w, "list", "segments")
		return
	}
	// The cursor is a position and the query starts AT it, so the next page
	// starts one past the last row shown.
	writePage(w, rows, page.Limit, func(s db.ListJobSegmentsPageRow) int64 { return int64(s.Position) + 1 },
		func(row db.ListJobSegmentsPageRow) Segment {
			seg := segmentDTO(row)
			if e, ok := previous[row.TranslationID]; ok {
				seg.PreviousStep = stepOutcomeDTO(e)
			}
			return seg
		})
}

// segmentWrite is the body of a draft save and of a confirm.
type segmentWrite struct {
	Text              *string    `json:"text"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

func (b segmentWrite) expected() pgtype.Timestamptz {
	if b.ExpectedUpdatedAt == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *b.ExpectedUpdatedAt, Valid: true}
}

func (h Handlers) saveSegmentDraft(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	var req segmentWrite
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Text == nil {
		httputil.FieldError(w, http.StatusBadRequest, "text", "text is required")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	id := ref.translation.ID
	res, err := h.Translations.BatchDraft(ctx, tenantID, auth.UserIDFromContext(ctx), ref.job.FileID, []translation.DraftFix{{
		TranslationID:     id,
		HumanText:         *req.Text,
		HumanRichText:     translation.KeptRichText(ref.translation, *req.Text),
		ExpectedUpdatedAt: req.expected(),
	}}, false)
	switch {
	case err != nil:
		h.writeSegmentError(ctx, w, err, id, tenantID, "unlock before editing")
	case len(res.Held) > 0:
		h.writeSegmentError(ctx, w, res.HeldError(id), id, tenantID, "")
	case len(res.Conflicts) > 0:
		h.writeSegmentError(ctx, w, translation.ErrConflict, id, tenantID, "")
	case len(res.Locked) > 0:
		h.writeSegmentError(ctx, w, translation.ErrLocked, id, tenantID, "unlock before editing")
	case len(res.Applied) == 0:
		httputil.ErrorCode(w, http.StatusConflict, "segment_not_editable", "this segment cannot be edited now")
	default:
		httputil.Data(w, segmentStateDTO(res.Applied[0]))
	}
}

func (h Handlers) confirmSegment(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	var req segmentWrite
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	// No text confirms the segment as it stands.
	text := ref.translation.ActiveText
	if req.Text != nil {
		text = req.Text
	}
	if text == nil || *text == "" {
		httputil.FieldError(w, http.StatusBadRequest, "text", "the segment has no translation to confirm; send text")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	id := ref.translation.ID
	res, err := h.Translations.Confirm(ctx, translation.ConfirmParams{
		ID:                id,
		TenantID:          tenantID,
		UserID:            auth.UserIDFromContext(ctx),
		HumanText:         *text,
		HumanRichText:     translation.KeptRichText(ref.translation, *text),
		ExpectedUpdatedAt: req.expected(),
		Via:               translation.ConfirmedViaUser,
	})
	if err != nil {
		h.writeSegmentError(ctx, w, err, id, tenantID, "unlock before confirming")
		return
	}
	httputil.Data(w, segmentStateDTO(res.Translation))
}

// writeSegmentError answers a failed segment write. A conflict is a plain
// error: the client reads the segment again for its current state. Everything
// else answers as the app does — a segment another member has open is 409
// segment_held, to retry later.
func (h Handlers) writeSegmentError(ctx context.Context, w http.ResponseWriter, err error, id, tenantID int64, lockedHint string) {
	if errors.Is(err, translation.ErrConflict) {
		httputil.ErrorCode(w, http.StatusConflict, "conflict", "the segment changed since you read it: read it again, then retry")
		return
	}
	h.Translations.WriteSegmentWriteError(ctx, w, err, id, tenantID, lockedHint)
}
