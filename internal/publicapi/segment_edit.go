package publicapi

import (
	"net/http"
	"time"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/segmentedit"
)

// splitSegment answers POST /api/v1/jobs/{jobID}/segments/{translationID}/split:
// the app's split, for the segment in the path.
func (h Handlers) splitSegment(w http.ResponseWriter, r *http.Request) {
	ref := segmentFromContext(r)
	var req struct {
		Offset            *int       `json:"offset"`
		ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	if req.Offset == nil {
		httputil.FieldError(w, http.StatusBadRequest, "offset", "offset is required")
		return
	}
	ctx := r.Context()
	res, err := h.SegmentEdit.Split(ctx, ref.job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), segmentedit.SplitRequest{
		TranslationID: ref.translation.ID, Offset: *req.Offset, ExpectedUpdatedAt: timeOrZero(req.ExpectedUpdatedAt),
	})
	if httputil.ServiceError(w, err, "split", "segment") {
		return
	}
	httputil.Data(w, SegmentEditResult{SegmentIDs: res.TranslationIDs})
}

// joinSegments answers POST /api/v1/jobs/{jobID}/segments/join: the app's
// join, for segments that follow each other in the job.
func (h Handlers) joinSegments(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Segments []struct {
			ID                int64      `json:"id"`
			ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
		} `json:"segments"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	rows := make([]segmentedit.JoinRow, len(req.Segments))
	for i, s := range req.Segments {
		if s.ID == 0 {
			httputil.FieldError(w, http.StatusBadRequest, "segments", "every segment needs an id")
			return
		}
		rows[i] = segmentedit.JoinRow{TranslationID: s.ID, ExpectedUpdatedAt: timeOrZero(s.ExpectedUpdatedAt)}
	}
	ctx := r.Context()
	job, ok := h.jobFromRoute(w, r)
	if !ok {
		return
	}
	res, err := h.SegmentEdit.Join(ctx, job, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), segmentedit.JoinRequest{Rows: rows})
	if httputil.ServiceError(w, err, "join", "segments") {
		return
	}
	httputil.Data(w, SegmentEditResult{SegmentIDs: res.TranslationIDs})
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
