package project

// events.go — projectevents payload builders for this package's handlers.
// Kept out of the handler files for the 500-line cap.

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectevents"
)

// projectMetaMap flattens the audit-relevant metadata fields of a project
// into JSON-friendly values for projectevents.Diff. Pointer/pgtype fields
// become nil or plain scalars so from/to render cleanly.
func projectMetaMap(p db.Project) map[string]any {
	m := map[string]any{
		"name":        p.Name,
		"description": p.Description,
		"status":      string(p.Status),
	}
	if p.CustomerID != nil {
		m["customer_id"] = *p.CustomerID
	} else {
		m["customer_id"] = nil
	}
	if p.Deadline.Valid {
		m["deadline"] = p.Deadline.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	} else {
		m["deadline"] = nil
	}
	m["client"] = strOrNil(p.Client)
	m["domain"] = strOrNil(p.Domain)
	return m
}

// settingsMap flattens the handler-editable scalars of a settings profile.
func settingsMap(p db.SettingsProfile) map[string]any {
	m := map[string]any{
		"fuzzy_threshold":         p.FuzzyThreshold,
		"mt_on_upload":            p.MtOnUpload,
		"tm_on_upload":            p.TmOnUpload,
		"sublanguage_penalty_pct": p.SublanguagePenaltyPct,
		"near_exact_penalty_pct":  p.NearExactPenaltyPct,
		"auto_complete":           p.AutoComplete,
		"inline_html":             p.InlineHtml,
		"tm_lookup_order":         p.TmLookupOrder,
	}
	if p.TmProfileID != nil {
		m["tm_profile_id"] = *p.TmProfileID
	} else {
		m["tm_profile_id"] = nil
	}
	if p.MtRouterID != nil {
		m["mt_router_id"] = *p.MtRouterID
	} else {
		m["mt_router_id"] = nil
	}
	if p.QaProfileID != nil {
		m["qa_profile_id"] = *p.QaProfileID
	} else {
		m["qa_profile_id"] = nil
	}
	return m
}

func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// customerName resolves a customer id to its name for diff enrichment.
// Best-effort: returns nil on lookup failure so a missing row never blocks
// the event.
func customerName(ctx context.Context, q db.Querier, tenantID int64, id *int64) any {
	if id == nil {
		return nil
	}
	c, err := q.GetCustomer(ctx, db.GetCustomerParams{ID: *id, TenantID: tenantID})
	if err != nil {
		return nil
	}
	return c.Name
}

// qaProfileName resolves a QA profile id to its name; same best-effort
// semantics as customerName.
func qaProfileName(ctx context.Context, q db.Querier, tenantID int64, id *int64) any {
	if id == nil {
		return nil
	}
	p, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: *id, TenantID: tenantID})
	if err != nil {
		return nil
	}
	return p.Name
}

// idFromChange coerces a Change side (any) back to *int64 for name lookups.
func idFromChange(v any) *int64 {
	if n, ok := v.(int64); ok {
		return &n
	}
	return nil
}

// tmAssignmentsPayload renders a TM assignment list for before/after details,
// resolving TM names best-effort (a deleted TM renders id-only).
func tmAssignmentsPayload(ctx context.Context, q db.Querier, tenantID int64, rows []db.SettingsTmAssignment) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		// penalty_pct included so a penalty-only edit still differs before/after
		// (emitAssignmentsSet skips equal lists) and the FE can render it.
		item := map[string]any{"tm_id": a.TmID, "mode": a.Mode, "priority": a.Priority, "penalty_pct": a.PenaltyPct}
		if tm, err := q.GetTMCollection(ctx, db.GetTMCollectionParams{ID: a.TmID, TenantID: tenantID}); err == nil {
			item["name"] = tm.Name
		}
		out = append(out, item)
	}
	return out
}

// termAssignmentsPayload renders a terminology assignment list, resolving
// term-base names best-effort.
func termAssignmentsPayload(ctx context.Context, q db.Querier, tenantID int64, rows []db.SettingsTerminologyAssignment) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		item := map[string]any{
			"term_base_id":    a.TermBaseID,
			"source_language": a.SourceLanguage,
			"target_language": a.TargetLanguage,
			"mode":            a.Mode,
			"priority":        a.Priority,
		}
		if tb, err := q.GetTermBase(ctx, db.GetTermBaseParams{ID: a.TermBaseID, TenantID: tenantID}); err == nil {
			item["name"] = tb.Name
		}
		out = append(out, item)
	}
	return out
}

// workflowStepsPayload renders workflow steps for before/after details.
// Assignees are ids only — the actor join + members list cover display.
// AI engine names are resolved best-effort (the FE has no engines list on the
// history tab), same pattern as termAssignmentsPayload.
func workflowStepsPayload(ctx context.Context, q db.Querier, tenantID int64, rows []db.WorkflowStep) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		item := map[string]any{"name": s.Name, "step_type": s.StepType}
		if s.StepDefinitionID != nil {
			item["step_definition_id"] = *s.StepDefinitionID
		}
		if s.DefaultAssignee != nil {
			item["default_assignee"] = *s.DefaultAssignee
		}
		if s.Deadline.Valid {
			item["deadline"] = s.Deadline.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if s.AiRouterID != nil {
			item["ai_router_id"] = *s.AiRouterID
			if rt, err := q.GetAIRouterAny(ctx, db.GetAIRouterAnyParams{ID: *s.AiRouterID, TenantID: tenantID}); err == nil {
				item["ai_router_name"] = rt.Name
			}
		}
		out = append(out, item)
	}
	return out
}

// emitAssignmentsSet emits one of the assignments_set events when before and
// after actually differ (replace-all endpoints are frequently no-op resaves).
func emitAssignmentsSet(ctx context.Context, q db.Querier, eventType string, projectID, tenantID int64, before, after []map[string]any) {
	if assignmentListsEqual(before, after) {
		return
	}
	actorID, actorName := projectevents.ActorFromContext(ctx)
	projectevents.Emit(ctx, q, projectevents.Event{
		ProjectID: projectID,
		TenantID:  tenantID,
		Type:      eventType,
		ActorID:   actorID,
		ActorName: actorName,
		Details:   map[string]any{"before": before, "after": after},
	})
}

func assignmentListsEqual(a, b []map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if projectevents.Diff(a[i], b[i]) != nil {
			return false
		}
	}
	return true
}
