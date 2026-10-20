package issues

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// A request carries at most MaxFiles files plus a few text fields.
const maxRequestBody = MaxFiles*MaxFileSize + 2<<20

// Handler serves the reporter's routes: any member of the org files and
// follows their own issues. Platform operators use PlatformHandler.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func memberFrom(r *http.Request) (Member, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		return Member{}, false
	}
	return Member{TenantID: claims.TenantID, UserID: claims.UserID, Role: claims.Role}, true
}

// HandleCreate is POST /api/issues (multipart: kind, severity, area, title,
// description, context, trail (the action trail), recording (watch mode),
// and any number of "files" up to MaxFiles). A report the assistant wrote also carries its check
// (verification, verification_note) and the verification_token that proves it.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	rep, ok := memberFrom(r)
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	files, cleanup, ok := readForm(w, r)
	if !ok {
		return
	}
	defer cleanup()
	issue, err := h.svc.Create(r.Context(), rep, NewIssue{
		Kind: r.FormValue("kind"), Severity: r.FormValue("severity"), Area: r.FormValue("area"),
		Title: r.FormValue("title"), Description: r.FormValue("description"),
		Context: r.FormValue("context"), Files: files,
		Verification:      Verification{Result: r.FormValue("verification"), Note: r.FormValue("verification_note")},
		VerificationToken: r.FormValue("verification_token"),
		Trail:             r.FormValue("trail"),
		Recording:         r.FormValue("recording"),
	})
	if writeError(w, r, err, "save", "issue") {
		return
	}
	httputil.Created(w, issue)
}

// HandleList is GET /api/issues: the org's issues the caller may see (a
// manager every member's, anyone else their own), filtered and ordered as
// listFilter reads them.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	rep, ok := memberFrom(r)
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	f, err := listFilter(r)
	if writeError(w, r, err, "load", "issues") {
		return
	}
	cursor, err := ParseCursor(r.URL.Query().Get("cursor"))
	if writeError(w, r, err, "load", "issues") {
		return
	}
	p := httputil.ParseCursor(r, 100)
	items, err := h.svc.List(r.Context(), rep, f, cursor, p.Limit+1)
	if writeError(w, r, err, "load", "issues") {
		return
	}
	writePage(w, items, p.Limit)
}

// HandleGet is GET /api/issues/{issueID}.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.Get(r.Context(), rep, id)
	if writeError(w, r, err, "load", "issue") {
		return
	}
	httputil.Data(w, detail)
}

// HandleDelete is DELETE /api/issues/{issueID}: the member's own issue, or one
// reported by a lower role (an owner: any of the org's). 403 when the member
// sees it but may not delete it.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	if writeError(w, r, h.svc.Delete(r.Context(), rep, id), "delete", "issue") {
		return
	}
	httputil.NoContent(w)
}

// HandleComment is POST /api/issues/{issueID}/comments (multipart: body, files).
func (h *Handler) HandleComment(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	files, cleanup, ok := readForm(w, r)
	if !ok {
		return
	}
	defer cleanup()
	detail, err := h.svc.MemberComment(r.Context(), rep, id, NewComment{Body: r.FormValue("body"), Files: files})
	if writeError(w, r, err, "save", "comment") {
		return
	}
	httputil.Created(w, detail)
}

// HandleConfirm is POST /api/issues/{issueID}/confirm: the org checked the
// fix of a resolved issue.
func (h *Handler) HandleConfirm(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.Confirm(r.Context(), rep, id)
	if writeError(w, r, err, "save", "confirmation") {
		return
	}
	httputil.Data(w, detail)
}

// HandleKind is PATCH /api/issues/{issueID}/kind with {kind, severity}: the
// reporter, or a manager who sees the issue, changes its kind. A bug needs a
// severity; any other kind takes none.
func (h *Handler) HandleKind(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	detail, err := h.svc.ChangeKind(r.Context(), rep, id, KindChange{Kind: req.Kind, Severity: req.Severity})
	if writeError(w, r, err, "save", "issue") {
		return
	}
	httputil.Data(w, detail)
}

// HandleAttachment is GET /api/issues/{issueID}/attachments/{attachmentID}.
func (h *Handler) HandleAttachment(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := memberAndID(w, r)
	if !ok {
		return
	}
	attachmentID, ok := attachmentIDParam(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Attachment(r.Context(), rep, id, attachmentID)
	if writeError(w, r, err, "load", "attachment") {
		return
	}
	writeAttachment(w, a)
}

func memberAndID(w http.ResponseWriter, r *http.Request) (Member, int64, bool) {
	rep, ok := memberFrom(r)
	if !ok {
		httputil.Error(w, http.StatusUnauthorized, "unauthorized")
		return Member{}, 0, false
	}
	id, err := httputil.ParseID(r, "issueID")
	if err != nil {
		httputil.BadRequest(w, "invalid issue ID")
		return Member{}, 0, false
	}
	return rep, id, true
}

// listFilter reads the list filters, each a comma-separated query parameter,
// the search text (q), and the order: sort=-updated (the default, newest
// first) or sort=updated. The operators' own filters are read by their
// handler only.
func listFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	f := Filter{Statuses: queryList(r, "status"), Kinds: queryList(r, "kind"), Areas: queryList(r, "area"),
		Severities: queryList(r, "severity"), Search: q.Get("q")}
	switch q.Get("sort") {
	case "", "-updated":
	case "updated":
		f.OldestFirst = true
	default:
		return Filter{}, clientError("Sort by updated or -updated.")
	}
	return f, nil
}

// queryList is a comma-separated query parameter; nil when absent.
func queryList(r *http.Request, key string) []string {
	if raw := r.URL.Query().Get(key); raw != "" {
		return strings.Split(raw, ",")
	}
	return nil
}

func attachmentIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "attachmentID")
	if err != nil {
		httputil.BadRequest(w, "invalid attachment ID")
		return 0, false
	}
	return id, true
}

// readForm parses the multipart body and returns its files. Parts beyond the
// first megabyte go to temporary files; cleanup removes them.
func readForm(w http.ResponseWriter, r *http.Request) ([]Upload, func(), bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		httputil.BadRequest(w, "The upload is too large (max 10 files of 25 MB) or malformed.")
		return nil, nil, false
	}
	cleanup := func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			logging.Error(r.Context(), err, "removing temporary upload files failed", "issue-upload-cleanup-failed")
		}
	}
	var files []Upload
	for _, fh := range r.MultipartForm.File["files"] {
		files = append(files, Upload{Name: fh.Filename, Size: fh.Size, Open: func() (io.ReadSeekCloser, error) { return fh.Open() }})
	}
	return files, cleanup, true
}

// writeError writes err (if any) and reports whether it did. Unexpected errors
// are logged; the client gets a generic message.
func writeError(w http.ResponseWriter, r *http.Request, err error, verb, resource string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errNoStorage) {
		httputil.Error(w, http.StatusServiceUnavailable, "Files cannot be stored right now. Send it without attachments.")
		return true
	}
	if isUnexpected(err) {
		logging.Error(r.Context(), err, verb+" "+resource+" failed", "issue-request-failed")
	}
	return httputil.ServiceError(w, err, verb, resource)
}

func isUnexpected(err error) bool {
	var coded httputil.CodedError
	if errors.As(err, &coded) {
		return false
	}
	if _, ok := httputil.AsClientError(err); ok {
		return false
	}
	if _, ok := httputil.AsForbiddenError(err); ok {
		return false
	}
	return !errors.Is(err, pgx.ErrNoRows)
}

// pageItem is an issue of a list page, for either side.
type pageItem interface{ position() Cursor }

func (v IssueView) position() Cursor { return Cursor{At: v.UpdatedAt, ID: v.ID} }

// writePage writes one page of a list: the items, and the cursor of the next
// page when there is one.
func writePage[T pageItem](w http.ResponseWriter, items []T, limit int32) {
	hasMore := int32(len(items)) > limit
	if hasMore {
		items = items[:limit]
	}
	var next Cursor
	if len(items) > 0 {
		next = items[len(items)-1].position()
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"data": items, "cursor": next.String(), "has_more": hasMore})
}

// writeAttachment sends a stored file. An image (by its sniffed bytes) is
// shown inline; anything else is a download of opaque bytes, so an uploaded
// page or script never runs in the app's origin.
func writeAttachment(w http.ResponseWriter, a Attachment) {
	defer a.Body.Close()
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("Content-Length", strconv.FormatInt(a.Size, 10))
	if a.IsImage {
		h.Set("Content-Type", a.ContentType)
		h.Set("Content-Disposition", "inline")
	} else {
		h.Set("Content-Type", "application/octet-stream")
		httputil.SetAttachmentFilename(w, a.Filename)
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, a.Body) //nolint:errcheck // the client went away; nothing left to do
}
