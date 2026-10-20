package mtrouter

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/httputil"
	"openloc/internal/mtengine"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
	// dec resolves a vendor token's key, which Validate needs to read a
	// provider engine's catalog.
	dec mtengine.Decryptor
}

func NewHandler(q db.Querier, pool *pgxpool.Pool, dec mtengine.Decryptor) *Handler {
	return &Handler{queries: q, pool: pool, dec: dec}
}

// Rule is one target language → engine mapping as the API returns it.
type Rule struct {
	ID         int64  `json:"id"`
	TargetLang string `json:"target_lang"`
	EngineRef
}

// Response is a router with its rules.
type Response struct {
	db.MtRouter
	Rules []Rule `json:"rules"`
	// Why this router cannot fully translate, or "" when every engine it routes
	// to can. Computed here rather than client-side so every
	// picker that offers a router — a project's settings, a template's — shows
	// the warning without fetching the engine lists to work it out.
	Unusable string `json:"unusable"`
}

func response(r db.MtRouter, rules []db.MtRouterRule, health enginehealth.Lookup) Response {
	out := Response{MtRouter: r, Rules: make([]Rule, 0, len(rules))}
	reasons := make([]enginehealth.Reason, 0, len(rules)+1)
	// The DEFAULT engine counts: it is what every unlisted language uses, so a
	// broken default is the widest breakage a router can have.
	if r.DefaultMtEngineID != nil {
		reasons = append(reasons, health.MT(*r.DefaultMtEngineID))
	}
	if r.DefaultAiEngineID != nil {
		reasons = append(reasons, health.AI(*r.DefaultAiEngineID))
	}
	for _, rr := range rules {
		out.Rules = append(out.Rules, Rule{ID: rr.ID, TargetLang: rr.TargetLang, EngineRef: EngineRef{MTEngineID: rr.MtEngineID, AIEngineID: rr.AiEngineID}})
		if rr.MtEngineID != nil {
			reasons = append(reasons, health.MT(*rr.MtEngineID))
		}
		if rr.AiEngineID != nil {
			reasons = append(reasons, health.AI(*rr.AiEngineID))
		}
	}
	out.Unusable = string(enginehealth.Worst(reasons...))
	return out
}

// engineHealth loads the tenant's engines once, for stamping routers.
func (h *Handler) engineHealth(ctx context.Context, tenantID int64) (enginehealth.Lookup, error) {
	mtEngines, err := h.queries.ListMTEngines(ctx, tenantID)
	if err != nil {
		return enginehealth.Lookup{}, err
	}
	aiEngines, err := h.queries.ListAIEngines(ctx, tenantID)
	if err != nil {
		return enginehealth.Lookup{}, err
	}
	aiRows, err := h.queries.ListAIEngineRowsForTenant(ctx, tenantID)
	if err != nil {
		return enginehealth.Lookup{}, err
	}
	return enginehealth.NewLookup(mtEngines, aiEngines, aiRows, mtengine.NeedsToken).Shown(auth.IsDemo(ctx)), nil
}

// HandleList returns the tenant's live routers with their rules.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	routers, err := h.queries.ListMTRouters(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT routers")
		return
	}
	rules, err := h.queries.ListMTRouterRulesForTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT router rules")
		return
	}
	byRouter := make(map[int64][]db.MtRouterRule, len(routers))
	for _, rr := range rules {
		byRouter[rr.RouterID] = append(byRouter[rr.RouterID], rr)
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT engines")
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
		httputil.BadRequest(w, "invalid MT router ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	rt, err := h.queries.GetMTRouter(r.Context(), db.GetMTRouterParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "MT router") {
		return
	}
	rules, err := h.queries.ListMTRouterRules(r.Context(), rt.ID)
	if err != nil {
		httputil.ServerError(w, "list", "MT router rules")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT engines")
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
	if msg := Validate(r.Context(), h.queries, h.dec, tenantID, &in); msg != "" {
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
			httputil.Error(w, http.StatusConflict, "an MT router with this name already exists")
			return
		}
		httputil.ServerError(w, "create", "MT router")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "transaction")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT engines")
		return
	}
	httputil.Created(w, response(rt, rules, health))
}

// HandleUpdate replaces a router's name, default and rules (see Update).
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "routerID")
	if err != nil {
		httputil.BadRequest(w, "invalid MT router ID")
		return
	}
	var in Input
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	rt, rules, err := Update(r.Context(), h.queries, h.pool, h.dec, tenantID, id, in)
	if err != nil {
		httputil.ServiceError(w, err, "update", "MT router")
		return
	}
	health, err := h.engineHealth(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "MT engines")
		return
	}
	httputil.Data(w, response(rt, rules, health))
}

// HandleDelete soft-deletes a router; 409 while any project or template still
// points at it (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "routerID")
	if err != nil {
		httputil.BadRequest(w, "invalid MT router ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "MT router")
		return
	}
	httputil.NoContent(w)
}

func writeRules(ctx context.Context, q db.Querier, routerID int64, in []RuleInput) ([]db.MtRouterRule, error) {
	out := make([]db.MtRouterRule, 0, len(in))
	for i, r := range in {
		row, err := q.CreateMTRouterRule(ctx, db.CreateMTRouterRuleParams{
			RouterID: routerID, TargetLang: r.TargetLang, MtEngineID: r.MTEngineID, AiEngineID: r.AIEngineID, SortOrder: int32(i),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
