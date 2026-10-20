// Package airouter is the AI routers library. A router is
// what a hybrid / ai workflow step points at: it maps target languages to
// QE+APE AI engines and may name a default engine for every other language.
// The step's job resolves router → engine by its target language at run time
// (ResolveEngineID); source languages play no part.
//
// Access scope: list is open to every manager (owner/admin/project_manager);
// create/update/delete are owner/admin — the same posture as AI engines
// (api.go wraps the group in RequireManager and gates writes with RequireRole).
package airouter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/aiengine"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
)

// maxNameLen caps router names defensively (UI spillover, not a DB limit).
const maxNameLen = 200

// RuleInput is one target language → engine mapping as a client submits it.
type RuleInput struct {
	TargetLang string `json:"target_lang"`
	EngineID   int64  `json:"engine_id"`
}

// Input is the writable shape of a router (create + update).
type Input struct {
	Name            string      `json:"name"`
	DefaultEngineID *int64      `json:"default_engine_id"`
	Rules           []RuleInput `json:"rules"`
}

// Validate normalizes in and returns a client-safe message when it is not a
// legal router: empty/long name, no route at all, a rule without a language,
// two rules for one language, or an engine that is not this tenant's QE+APE
// engine. An empty message means valid.
func Validate(ctx context.Context, q db.Querier, tenantID int64, in *Input) (msg string) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "name is required"
	}
	if len(in.Name) > maxNameLen {
		return fmt.Sprintf("name too long (max %d characters)", maxNameLen)
	}
	// A router with nothing to route to would silently skip every job.
	if in.DefaultEngineID == nil && len(in.Rules) == 0 {
		return "a router needs at least one route: a default engine or a language rule"
	}
	if in.DefaultEngineID != nil {
		if msg := aiengine.CheckQEAPEEngine(ctx, q, tenantID, *in.DefaultEngineID); msg != "" {
			return "default engine: " + msg
		}
	}
	seen := make(map[string]bool, len(in.Rules))
	for i := range in.Rules {
		r := &in.Rules[i]
		r.TargetLang = langcode.Normalize(r.TargetLang)
		if r.TargetLang == "" || r.TargetLang == langcode.Wildcard {
			return fmt.Sprintf("rule %d: a target language is required", i+1)
		}
		if seen[r.TargetLang] {
			return fmt.Sprintf("rule %d: language %q is listed twice", i+1, r.TargetLang)
		}
		seen[r.TargetLang] = true
		if msg := aiengine.CheckQEAPEEngine(ctx, q, tenantID, r.EngineID); msg != "" {
			return fmt.Sprintf("rule %d (%s): %s", i+1, r.TargetLang, msg)
		}
	}
	return ""
}

// Create writes a validated router and its rules. The two inserts must land
// together, so the caller passes a transaction's querier (the HTTP handler and
// the MCP tool both do). Validate first — Create does not re-check.
func Create(ctx context.Context, q db.Querier, tenantID int64, in Input) (db.AiRouter, []db.AiRouterRule, error) {
	rt, err := q.CreateAIRouter(ctx, db.CreateAIRouterParams{
		TenantID: tenantID, Name: in.Name, DefaultEngineID: in.DefaultEngineID,
	})
	if err != nil {
		return db.AiRouter{}, nil, err
	}
	rules, err := writeRules(ctx, q, rt.ID, in.Rules)
	if err != nil {
		return db.AiRouter{}, nil, err
	}
	return rt, rules, nil
}

// ValidateStepRouter checks the router attached to a workflow step before it
// is persisted (shared by the step-definition and profile step save paths):
// it must be this tenant's live router. Returns a client-safe message
// ("" = valid). A nil router means "plain human step" and is always valid.
func ValidateStepRouter(ctx context.Context, q db.Querier, tenantID int64, routerID *int64) string {
	if routerID == nil {
		return ""
	}
	if _, err := q.GetAIRouter(ctx, db.GetAIRouterParams{ID: *routerID, TenantID: tenantID}); err != nil {
		return fmt.Sprintf("AI router %d not found", *routerID)
	}
	return ""
}

// ResolveEngineID picks the engine a router sends a job with targetLang to:
// the matching rule's engine, else the router's default, else nil (nothing
// runs for this language). The router is resolved regardless of deleted_at —
// a profile step keeps pointing at a retired router and its jobs must still
// run. pgx.ErrNoRows means the router is not this tenant's.
func ResolveEngineID(ctx context.Context, q db.Querier, tenantID, routerID int64, targetLang string) (*int64, error) {
	router, err := q.GetAIRouterAny(ctx, db.GetAIRouterAnyParams{ID: routerID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("AI router %d: %w", routerID, err)
		}
		return nil, fmt.Errorf("get AI router %d: %w", routerID, err)
	}
	rules, err := q.ListAIRouterRules(ctx, routerID)
	if err != nil {
		return nil, fmt.Errorf("list rules for AI router %d: %w", routerID, err)
	}
	if id := MatchRule(rules, targetLang); id != nil {
		return id, nil
	}
	return router.DefaultEngineID, nil
}

// MatchRule returns the engine of the rule that covers targetLang, or nil —
// exact code first, then same base language, first listed wins
// (langcode.BestMatch; rules come pre-sorted from ListAIRouterRules).
func MatchRule(rules []db.AiRouterRule, targetLang string) *int64 {
	if r := langcode.BestMatch(rules, func(r db.AiRouterRule) string { return r.TargetLang }, targetLang); r != nil {
		return &r.EngineID
	}
	return nil
}
