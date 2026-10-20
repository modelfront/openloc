package publicapi

import (
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/label"
)

// Label is a v1 label with its allowed values. The built-in Customer and
// Domain labels carry key "customer" and "domain".
type Label struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Key    *string      `json:"key"`
	Values []LabelValue `json:"values"`
}

// LabelValue is one allowed value of a label.
type LabelValue struct {
	ID    int64  `json:"id"`
	Value string `json:"value"`
}

// ProjectLabel is a value a project carries, with its label.
type ProjectLabel struct {
	LabelID   int64  `json:"label_id"`
	LabelName string `json:"label_name"`
	ValueID   int64  `json:"value_id"`
	Value     string `json:"value"`
}

func labelDTO(l label.Label) Label {
	out := Label{ID: l.ID, Name: l.Name, Values: make([]LabelValue, len(l.Values))}
	if l.SystemKey != "" {
		key := l.SystemKey
		out.Key = &key
	}
	for i, v := range l.Values {
		out.Values[i] = LabelValue{ID: v.ID, Value: v.Value}
	}
	return out
}

func projectLabelDTO(p label.Pick) ProjectLabel {
	return ProjectLabel{LabelID: p.LabelDefinitionID, LabelName: p.LabelName, ValueID: p.LabelValueID, Value: p.LabelValue}
}

func (h Handlers) listLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := label.List(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "labels")
		return
	}
	writeAll(w, labels, labelDTO)
}

func (h Handlers) createLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	l, err := label.Create(r.Context(), h.Pool, auth.TenantIDFromContext(r.Context()), req.Name, req.Values)
	if httputil.ServiceError(w, err, "create", "label") {
		return
	}
	httputil.Created(w, labelDTO(l))
}

func (h Handlers) addLabelValue(w http.ResponseWriter, r *http.Request) {
	labelID, err := httputil.ParseID(r, "labelID")
	if err != nil {
		httputil.BadRequest(w, "invalid label ID")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	v, err := label.AddValue(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), labelID, req.Value)
	if httputil.ServiceError(w, err, "add", "label") {
		return
	}
	httputil.Created(w, LabelValue{ID: v.ID, Value: v.Value})
}

func (h Handlers) listProjectLabels(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	if !h.projectExists(w, r, projectID) {
		return
	}
	picks, err := label.ProjectPicks(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		httputil.ServerError(w, "list", "project labels")
		return
	}
	writeAll(w, picks, projectLabelDTO)
}

// setProjectLabels replaces a project's label values: one value per label,
// and an empty list clears them.
func (h Handlers) setProjectLabels(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		ValueIDs []int64 `json:"value_ids"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.ValueIDs == nil {
		httputil.FieldError(w, http.StatusBadRequest, "value_ids", "value_ids is required (an empty list clears the labels)")
		return
	}
	picks, err := label.SetProjectPicks(r.Context(), h.Pool, h.Queries, auth.TenantIDFromContext(r.Context()), projectID, req.ValueIDs)
	if httputil.ServiceError(w, err, "set", "project") {
		return
	}
	writeAll(w, picks, projectLabelDTO)
}

// labelFromRoute checks the {labelID} label is the tenant's.
func (h Handlers) labelFromRoute(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "labelID")
	if err != nil {
		httputil.BadRequest(w, "invalid label ID")
		return 0, false
	}
	_, err = h.Queries.GetLabelDefinition(r.Context(), db.GetLabelDefinitionParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	return id, !httputil.DBError(w, err, "label")
}

func (h Handlers) deleteLabel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.labelFromRoute(w, r)
	if !ok {
		return
	}
	if err := label.Delete(r.Context(), h.Queries, auth.TenantIDFromContext(r.Context()), id); httputil.ServiceError(w, err, "delete", "label") {
		return
	}
	httputil.NoContent(w)
}

func (h Handlers) deleteLabelValue(w http.ResponseWriter, r *http.Request) {
	labelID, ok := h.labelFromRoute(w, r)
	if !ok {
		return
	}
	valueID, err := httputil.ParseID(r, "valueID")
	if err != nil {
		httputil.BadRequest(w, "invalid value ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	v, err := h.Queries.GetLabelValue(r.Context(), db.GetLabelValueParams{ID: valueID, TenantID: tenantID})
	if httputil.DBError(w, err, "label value") {
		return
	}
	if v.LabelDefinitionID != labelID {
		httputil.NotFound(w, "label value")
		return
	}
	if err := label.DeleteValue(r.Context(), h.Pool, tenantID, valueID); httputil.ServiceError(w, err, "delete", "label value") {
		return
	}
	httputil.NoContent(w)
}
