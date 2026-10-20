package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// PlatformHandler serves the operator routes. Mount it behind
// RequirePlatformAdmin: it reads every org's issues.
type PlatformHandler struct {
	svc *Service
}

func NewPlatformHandler(svc *Service) *PlatformHandler { return &PlatformHandler{svc: svc} }

func operatorFrom(r *http.Request) Operator {
	return Operator{UserID: auth.UserIDFromContext(r.Context())}
}

func issueID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "issueID")
	if err != nil {
		httputil.BadRequest(w, "invalid issue ID")
		return 0, false
	}
	return id, true
}

// HandleList is GET /api/platform/issues: every org's issues, filtered and
// ordered as listFilter reads them, plus org (ids), priority, tag,
// verification (the assistant's check), and assignee (ids, and "none" for the
// unassigned ones), each comma-separated.
func (h *PlatformHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if writeError(w, r, err, "load", "issues") {
		return
	}
	f.Priorities, f.Tags, f.Verifications = queryList(r, "priority"), queryList(r, "tag"), queryList(r, "verification")
	var orgNone bool
	if f.TenantIDs, orgNone, err = queryIDs(r, "org"); err != nil || orgNone {
		httputil.BadRequest(w, "invalid org filter")
		return
	}
	if f.AssigneeIDs, f.Unassigned, err = queryIDs(r, "assignee"); err != nil {
		httputil.BadRequest(w, "invalid assignee filter")
		return
	}
	cursor, err := ParseCursor(r.URL.Query().Get("cursor"))
	if writeError(w, r, err, "load", "issues") {
		return
	}
	p := httputil.ParseCursor(r, 100)
	items, err := h.svc.PlatformList(r.Context(), f, cursor, p.Limit+1)
	if writeError(w, r, err, "load", "issues") {
		return
	}
	writePage(w, items, p.Limit)
}

// queryIDs reads a comma-separated list of ids; the value "none" sets none.
func queryIDs(r *http.Request, key string) (ids []int64, none bool, err error) {
	for _, v := range queryList(r, key) {
		if v == "none" {
			none = true
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, false, clientError("invalid id")
		}
		ids = append(ids, id)
	}
	return ids, none, nil
}

// HandleOrgs is GET /api/platform/issues/orgs: the orgs that have issues.
func (h *PlatformHandler) HandleOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.svc.PlatformOrgs(r.Context())
	if writeError(w, r, err, "load", "organizations") {
		return
	}
	httputil.Data(w, orgs)
}

// HandleAssignees is GET /api/platform/issues/assignees: the support team.
func (h *PlatformHandler) HandleAssignees(w http.ResponseWriter, r *http.Request) {
	people, err := h.svc.PlatformAssignees(r.Context())
	if writeError(w, r, err, "load", "support team") {
		return
	}
	httputil.Data(w, people)
}

// HandleTags is GET /api/platform/issues/tags: every tag in use.
func (h *PlatformHandler) HandleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.svc.PlatformTags(r.Context())
	if writeError(w, r, err, "load", "tags") {
		return
	}
	httputil.Data(w, tags)
}

// HandleGet is GET /api/platform/issues/{issueID}.
func (h *PlatformHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.PlatformGet(r.Context(), id)
	if writeError(w, r, err, "load", "issue") {
		return
	}
	httputil.Data(w, detail)
}

// HandleDelete is DELETE /api/platform/issues/{issueID}: any org's issue.
func (h *PlatformHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	if writeError(w, r, h.svc.PlatformDelete(r.Context(), id), "delete", "issue") {
		return
	}
	httputil.NoContent(w)
}

// HandleSetStatus is POST /api/platform/issues/{issueID}/status
// ({"status": "...", "note": "...", "duplicate_of": 12}); duplicate_of only
// with the duplicate status.
func (h *PlatformHandler) HandleSetStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status      string `json:"status"`
		Note        string `json:"note"`
		DuplicateOf *int64 `json:"duplicate_of"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	detail, err := h.svc.PlatformSetStatus(r.Context(), operatorFrom(r), id, StatusChange{
		Status: req.Status, Note: strings.TrimSpace(req.Note), DuplicateOf: req.DuplicateOf,
	})
	if writeError(w, r, err, "save", "status") {
		return
	}
	httputil.Data(w, detail)
}

// HandleComment is POST /api/platform/issues/{issueID}/comments (multipart:
// body, files): a reply the reporter sees.
func (h *PlatformHandler) HandleComment(w http.ResponseWriter, r *http.Request) {
	h.handleComment(w, r, h.svc.PlatformComment)
}

// HandleNote is POST /api/platform/issues/{issueID}/notes (multipart: body,
// files): an internal note the reporter's org never sees.
func (h *PlatformHandler) HandleNote(w http.ResponseWriter, r *http.Request) {
	h.handleComment(w, r, h.svc.PlatformNote)
}

func (h *PlatformHandler) handleComment(w http.ResponseWriter, r *http.Request,
	add func(ctx context.Context, op Operator, id int64, in NewComment) (PlatformIssueDetail, error)) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	files, cleanup, ok := readForm(w, r)
	if !ok {
		return
	}
	defer cleanup()
	detail, err := add(r.Context(), operatorFrom(r), id, NewComment{Body: r.FormValue("body"), Files: files})
	if writeError(w, r, err, "save", "comment") {
		return
	}
	httputil.Created(w, detail)
}

// HandleUpdate is PATCH /api/platform/issues/{issueID} with any of kind,
// severity, area, priority (null clears it), tags (a list) and assignee_id
// (null unassigns). Fields left out stay as they are.
func (h *PlatformHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	u, err := decodeUpdate(r)
	if writeError(w, r, err, "save", "issue") {
		return
	}
	detail, err := h.svc.PlatformUpdate(r.Context(), operatorFrom(r), id, u)
	if writeError(w, r, err, "save", "issue") {
		return
	}
	httputil.Data(w, detail)
}

// decodeUpdate reads a PATCH body, telling a field left out from a null one.
func decodeUpdate(r *http.Request) (IssueUpdate, error) {
	var raw map[string]json.RawMessage
	if err := httputil.Decode(r, &raw); err != nil {
		return IssueUpdate{}, clientError("invalid request body")
	}
	var u IssueUpdate
	for key, v := range raw {
		null := string(v) == "null"
		var err error
		switch key {
		case "kind":
			u.Kind, err = stringOrEmpty(v, null)
		case "severity":
			u.Severity, err = stringOrEmpty(v, null)
		case "area":
			u.Area, err = stringOrEmpty(v, null)
		case "priority":
			u.Priority, err = stringOrEmpty(v, null)
		case "tags":
			tags := []string{}
			if !null {
				err = json.Unmarshal(v, &tags)
			}
			u.Tags = &tags
		case "assignee_id":
			u.AssigneeSet = true
			if !null {
				var id int64
				err = json.Unmarshal(v, &id)
				u.AssigneeID = &id
			}
		default:
			return IssueUpdate{}, clientError("Unknown field: " + key)
		}
		if err != nil {
			return IssueUpdate{}, clientError("invalid " + key)
		}
	}
	return u, nil
}

// stringOrEmpty decodes a JSON string; null is "".
func stringOrEmpty(v json.RawMessage, null bool) (*string, error) {
	s := ""
	if null {
		return &s, nil
	}
	err := json.Unmarshal(v, &s)
	return &s, err
}

// HandleAttachment is GET /api/platform/issues/{issueID}/attachments/{attachmentID}.
func (h *PlatformHandler) HandleAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	attachmentID, ok := attachmentIDParam(w, r)
	if !ok {
		return
	}
	a, err := h.svc.PlatformAttachment(r.Context(), id, attachmentID)
	if writeError(w, r, err, "load", "attachment") {
		return
	}
	writeAttachment(w, a)
}
