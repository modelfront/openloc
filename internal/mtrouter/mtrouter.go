// Package mtrouter is the MT routers library. A router is what a settings
// profile (project or template) points at for machine translation: it maps
// target languages to MT engines — a classic engine (mt_engines) or an LLM
// translation engine (ai_engines with purpose 'mt') — and may name a default
// engine for every other language. The pipeline resolves router → engine by
// the job's target language at run time (ResolveEngine); source languages
// play no part.
//
// Access scope: the whole surface is manager-level (owner/admin/
// project_manager), the same posture as MT engines.
package mtrouter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/aiengine"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/mt"
	"openloc/internal/mtengine"
)

// maxNameLen caps router names defensively (UI spillover, not a DB limit).
const maxNameLen = 200

// maxRules caps how many routes one router may carry. A rule is one target
// language and duplicates are already refused, so a real router cannot exceed
// the language list; this sits well above it. The bound matters because each
// rule can trigger a catalog fetch against a cold cache, and a failed fetch is
// never cached — so a blackholing endpoint would otherwise buy one stalled
// request per rule from a single save.
const maxRules = 200

// EngineRef names exactly one engine: a classic MT engine or an LLM
// translation engine.
type EngineRef struct {
	MTEngineID *int64 `json:"mt_engine_id"`
	AIEngineID *int64 `json:"ai_engine_id"`
}

// IsSet reports whether the ref names an engine at all.
func (e EngineRef) IsSet() bool { return e.MTEngineID != nil || e.AIEngineID != nil }

// RuleInput is one target language → engine mapping as a client submits it.
type RuleInput struct {
	TargetLang string `json:"target_lang"`
	EngineRef
}

// Input is the writable shape of a router (create + update).
type Input struct {
	Name    string      `json:"name"`
	Default EngineRef   `json:"default"`
	Rules   []RuleInput `json:"rules"`
}

// checkEngine verifies a ref names exactly one engine that belongs to the
// tenant; an LLM engine must carry the 'mt' purpose. Returns a
// client-safe message ("" = valid).
//
// targetLang is the language a rule routes, or "" for the router's default —
// the default answers for every language nobody named, so there is nothing to
// check it against. A provider engine is asked whether it writes that language;
// an LLM engine publishes no catalog and takes any pair.
func checkEngine(ctx context.Context, q db.Querier, dec mtengine.Decryptor, tenantID int64, ref EngineRef, targetLang string) string {
	switch {
	case ref.MTEngineID != nil && ref.AIEngineID != nil:
		return "pick either an MT engine or an LLM translation engine, not both"
	case ref.MTEngineID != nil:
		eng, err := q.GetMTEngineCredential(ctx, db.GetMTEngineCredentialParams{ID: *ref.MTEngineID, TenantID: tenantID})
		if err != nil {
			return fmt.Sprintf("MT engine %d not found", *ref.MTEngineID)
		}
		if targetLang == "" {
			return ""
		}
		// Credential, not eng.ApiKey: a provider engine keeps its key on a
		// vendor token and leaves the column NULL, and an engine handed an
		// empty key answers from the static table, which refuses nothing.
		apiKey, apiURL, credErr := mtengine.Credential(eng, dec)
		if credErr != nil {
			// Unreadable key: no catalog to check against, so nothing is
			// refused. Saving the router is not the place to report it.
			return ""
		}
		// nil settings: the providers' language endpoints take none of the
		// request options an engine row carries.
		catalog := mt.CatalogEngine(eng.EngineType, apiKey, apiURL, nil)
		if v := mt.ValidateTarget(ctx, targetLang, catalog); v.Rejected {
			return fmt.Sprintf("%s %s", eng.Name, v.Reason)
		}
	case ref.AIEngineID != nil:
		engine, err := q.GetAIEngine(ctx, db.GetAIEngineParams{ID: *ref.AIEngineID, TenantID: tenantID})
		if err != nil {
			return fmt.Sprintf("AI engine %d not found", *ref.AIEngineID)
		}
		if engine.Purpose != aiengine.PurposeMT {
			return fmt.Sprintf("AI engine %q is a QE+APE engine — an MT router needs a translation engine", engine.Name)
		}
	default:
		return "an engine is required"
	}
	return ""
}

// Validate normalizes in and returns a client-safe message when it is not a
// legal router: empty/long name, no route at all, a rule without a language,
// two rules for one language, or an engine that is not this tenant's (or an
// AI engine that does not translate). An empty message means valid.
func Validate(ctx context.Context, q db.Querier, dec mtengine.Decryptor, tenantID int64, in *Input) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "name is required"
	}
	if len(in.Name) > maxNameLen {
		return fmt.Sprintf("name too long (max %d characters)", maxNameLen)
	}
	// A router with nothing to route to would silently skip every job.
	if !in.Default.IsSet() && len(in.Rules) == 0 {
		return "a router needs at least one route: a default engine or a language rule"
	}
	if in.Default.IsSet() {
		if msg := checkEngine(ctx, q, dec, tenantID, in.Default, ""); msg != "" {
			return "default engine: " + msg
		}
	}
	if len(in.Rules) > maxRules {
		return fmt.Sprintf("a router carries at most %d routes", maxRules)
	}
	seen := make(map[string]bool, len(in.Rules))
	for i := range in.Rules {
		r := &in.Rules[i]
		r.TargetLang = langcode.Normalize(r.TargetLang)
		if r.TargetLang == "" || r.TargetLang == langcode.Wildcard {
			return "a target language is required on every row"
		}
		if seen[r.TargetLang] {
			return fmt.Sprintf("language %q is listed twice", r.TargetLang)
		}
		seen[r.TargetLang] = true
		// No rule index in any of these: the client flattens rows × languages
		// into this array and drops the "all languages" entry, so a position
		// here names nothing the user can see. Each message identifies itself
		// by language or by engine instead.
		if msg := checkEngine(ctx, q, dec, tenantID, r.EngineRef, r.TargetLang); msg != "" {
			return msg
		}
	}
	return ""
}

// Create writes a validated router and its rules. The two inserts must land
// together, so the caller passes a transaction's querier (the HTTP handler and
// the MCP tool both do). Validate first — Create does not re-check.
func Create(ctx context.Context, q db.Querier, tenantID int64, in Input) (db.MtRouter, []db.MtRouterRule, error) {
	rt, err := q.CreateMTRouter(ctx, db.CreateMTRouterParams{
		TenantID: tenantID, Name: in.Name,
		DefaultMtEngineID: in.Default.MTEngineID, DefaultAiEngineID: in.Default.AIEngineID,
	})
	if err != nil {
		return db.MtRouter{}, nil, err
	}
	rules, err := writeRules(ctx, q, rt.ID, in.Rules)
	if err != nil {
		return db.MtRouter{}, nil, err
	}
	return rt, rules, nil
}

// ValidateProfileRouter checks the router a settings profile is being pointed
// at: it must be this tenant's live router. nil (no machine translation) is
// always valid. Returns a client-safe message ("" = valid).
func ValidateProfileRouter(ctx context.Context, q db.Querier, tenantID int64, routerID *int64) string {
	if routerID == nil {
		return ""
	}
	if _, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: *routerID, TenantID: tenantID}); err != nil {
		return fmt.Sprintf("MT router %d not found", *routerID)
	}
	return ""
}

// ResolveEngine picks the engine a router sends a job with targetLang to: the
// matching rule's engine, else the router's default, else an unset ref
// (nothing translates this language). The router is resolved regardless of
// deleted_at — a profile keeps pointing at a retired router and its jobs must
// still run. pgx.ErrNoRows means the router is not this tenant's.
func ResolveEngine(ctx context.Context, q db.Querier, tenantID, routerID int64, targetLang string) (EngineRef, error) {
	router, err := q.GetMTRouterAny(ctx, db.GetMTRouterAnyParams{ID: routerID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EngineRef{}, fmt.Errorf("MT router %d: %w", routerID, err)
		}
		return EngineRef{}, fmt.Errorf("get MT router %d: %w", routerID, err)
	}
	rules, err := q.ListMTRouterRules(ctx, routerID)
	if err != nil {
		return EngineRef{}, fmt.Errorf("list rules for MT router %d: %w", routerID, err)
	}
	return Pick(rules, func(r db.MtRouterRule) string { return r.TargetLang },
		func(r db.MtRouterRule) EngineRef {
			return EngineRef{MTEngineID: r.MtEngineID, AIEngineID: r.AiEngineID}
		},
		EngineRef{MTEngineID: router.DefaultMtEngineID, AIEngineID: router.DefaultAiEngineID},
		targetLang), nil
}

// Pick is a router's choice for targetLang: the engine of the rule that best
// matches it (langcode.BestMatch, so a rule for "fr" covers "fr-ca"), else
// def, the router's default. rules come in the router's order.
func Pick[R, E any](rules []R, ruleLang func(R) string, ruleEngine func(R) E, def E, targetLang string) E {
	if r := langcode.BestMatch(rules, ruleLang, targetLang); r != nil {
		return ruleEngine(*r)
	}
	return def
}
