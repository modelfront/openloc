package hold

import (
	"errors"
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// Handler serves the hold routes under /api/jobs/{jobID}/holds. The job route
// group gates them: any member of the job's organization, and a linguist only
// on a job assigned to them; taking a hold also needs a task the caller may
// edit (Service.Acquire).
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

type acquireRequest struct {
	TabID string `json:"tab_id"`
}

// HandleAcquire takes or renews the caller's hold on a segment.
// PUT /api/jobs/{jobID}/holds/{translationID}
func (h *Handler) HandleAcquire(w http.ResponseWriter, r *http.Request) {
	jobID, translationID, ok := routeIDs(w, r)
	if !ok {
		return
	}
	var req acquireRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	got, err := h.svc.Acquire(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), jobID, translationID, req.TabID)
	var held *HeldError
	if errors.As(err, &held) {
		WriteHeld(w, held)
		return
	}
	if httputil.ServiceError(w, err, "hold", "segment") {
		return
	}
	httputil.Data(w, got)
}

// HandleRelease ends the caller's hold from one tab. Always 204: releasing a
// hold that already ended is not an error.
// DELETE /api/jobs/{jobID}/holds/{translationID}?tab_id=
func (h *Handler) HandleRelease(w http.ResponseWriter, r *http.Request) {
	_, translationID, ok := routeIDs(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	err := h.svc.Release(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), translationID, r.URL.Query().Get("tab_id"))
	if httputil.ServiceError(w, err, "release", "segment hold") {
		return
	}
	httputil.NoContent(w)
}

// Jobs resolves the jobs a request reads holds for, already checked for the
// caller. On failure it writes the response and returns false.
type Jobs func(w http.ResponseWriter, r *http.Request) ([]int64, bool)

// JobFromURL is the route's {jobID}, which the job route group checked.
func JobFromURL(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return nil, false
	}
	return []int64{jobID}, true
}

// HandleList returns the job's live holds with the holders' names.
// GET /api/jobs/{jobID}/holds
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	h.List(JobFromURL)(w, r)
}

// List returns a handler for the live holds of the jobs `jobs` resolves,
// with the holders' names.
func (h *Handler) List(jobs Jobs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobIDs, ok := jobs(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		holds, err := h.svc.List(ctx, auth.TenantIDFromContext(ctx), jobIDs)
		if httputil.ServiceError(w, err, "list", "segment holds") {
			return
		}
		httputil.Data(w, holds)
	}
}

// WriteHeld answers a refusal because another user holds the segment: 409
// with code segment_held, and the holder, so the editor can name them.
func WriteHeld(w http.ResponseWriter, e *HeldError) {
	httputil.JSON(w, http.StatusConflict, map[string]any{
		"error":  map[string]any{"code": CodeSegmentHeld, "message": e.Error()},
		"holder": map[string]any{"id": e.HolderID, "name": e.HolderName},
	})
}

func routeIDs(w http.ResponseWriter, r *http.Request) (jobID, translationID int64, ok bool) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return 0, 0, false
	}
	translationID, err = httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return 0, 0, false
	}
	return jobID, translationID, true
}
