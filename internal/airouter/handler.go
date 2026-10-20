package airouter

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/httputil"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// Rule is one target language → engine mapping as the API returns it.
type Rule struct {
	ID         int64  `json:"id"`
	TargetLang string `json:"target_lang"`
	EngineID   int64  `json:"engine_id"`
}

// Response is a router with its rules.
type Response struct {
	db.AiRouter
	Rules []Rule `json:"rules"`
	// Why this router cannot fully run, or "" when every engine it routes to
	// can. Computed here so a workflow step's router picker
	// shows the warning without fetching the engine list to work it out.
	Unusable string `json:"unusable"`
}

func response(r db.AiRouter, rules []db.AiRouterRule, health enginehealth.Lookup) Response {
	out := Response{AiRouter: r, Rules: make([]Rule, 0, len(rules))}
	reasons := make([]enginehealth.Reason, 0, len(rules)+1)
	// The DEFAULT engine counts: it is what every unlisted language uses.
	if r.DefaultEngineID != nil {
		reasons = append(reasons, health.AI(*r.DefaultEngineID))
	}
	for _, rr := range rules {
		out.Rules = append(out.Rules, Rule{ID: rr.ID, TargetLang: rr.TargetLang, EngineID: rr.EngineID})
		reasons = append(reasons, health.AI(rr.EngineID))
	}
	out.Unusable = string(enginehealth.Worst(reasons...))
	return out
}

// engineHealth loads the tenant's AI engines once, for stamping routers. An AI
// router routes only to those, so the MT half of the lookup stays empty.
func (h *Handler) engineHealth(ctx context.Context, tenantID int64) (enginehealth.Lookup, error) {
	engines, err := h.queries.ListAIEngines(ctx, tenantID)
	if err != nil {
		return enginehealth.Lookup{}, err
	}
	rows, err := h.queries.ListAIEngineRowsForTenant(ctx, tenantID)
	if err != nil {
		return enginehealth.Lookup{}, err
	}
	return enginehealth.NewLookup(nil, engines, rows, nil).Shown(auth.IsDemo(ctx)), nil
}

// HandleList returns the tenant's live routers with their rules.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	routers, err := h.queries.ListAIRouters(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI routers")
		return
	}
	rules, err := h.queries.ListAIRouterRulesForTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI router rules")
		return
	}
	byRouter := make(map[int64][]db.AiRouterRule, len(routers))
	for _, rr := range rules {
		byRouter[rr.RouterID] = append(byRouter[rr.RouterID], rr)
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	out := make([]Response, 0, len(routers))
	for _, rt := range routers {
		out = append(out, response(rt, byRouter[rt.ID], health))
	}
	httputil.Data(w, out)
}

// HandleGet returns one live router with its rules.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "routerID")
	if err != nil {
		httputil.BadRequest(w, "invalid AI router ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	rt, err := h.queries.GetAIRouter(r.Context(), db.GetAIRouterParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "AI router") {
		return
	}
	rules, err := h.queries.ListAIRouterRules(r.Context(), rt.ID)
	if err != nil {
		httputil.ServerError(w, "list", "AI router rules")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	httputil.Data(w, response(rt, rules, health))
}

// HandleCreate inserts a router and its rules in one transaction. 409 on a
// case-insensitive duplicate name.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if msg := Validate(r.Context(), h.queries, tenantID, &in); msg != "" {
		httputil.BadRequest(w, msg)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	txq := db.New(tx)

	rt, rules, err := Create(r.Context(), txq, tenantID, in)
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			httputil.Error(w, http.StatusConflict, "an AI router with this name already exists")
			return
		}
		httputil.ServerError(w, "create", "AI router")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "transaction")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	httputil.Created(w, response(rt, rules, health))
}

// HandleUpdate replaces a router's name, default and rules (see Update).
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "routerID")
	if err != nil {
		httputil.BadRequest(w, "invalid AI router ID")
		return
	}
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	rt, rules, err := Update(r.Context(), h.queries, h.pool, tenantID, id, in)
	if err != nil {
		httputil.ServiceError(w, err, "update", "AI router")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	httputil.Data(w, response(rt, rules, health))
}

// HandleDelete soft-deletes a router; 409 while any step still points at it
// (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "routerID")
	if err != nil {
		httputil.BadRequest(w, "invalid AI router ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "AI router")
		return
	}
	httputil.NoContent(w)
}

func writeRules(ctx context.Context, q db.Querier, routerID int64, in []RuleInput) ([]db.AiRouterRule, error) {
	out := make([]db.AiRouterRule, 0, len(in))
	for i, r := range in {
		row, err := q.CreateAIRouterRule(ctx, db.CreateAIRouterRuleParams{
			RouterID: routerID, TargetLang: r.TargetLang, EngineID: r.EngineID, SortOrder: int32(i),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
