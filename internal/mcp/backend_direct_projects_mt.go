package mcp

// The machine translation a project's rows carry: its MT router and which
// engine that router sends each target language to.

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtrouter"
)

// projectMT is one project's machine translation as list_projects and
// get_project show it.
type projectMT struct {
	router   string
	engines  []MTEngineUse
	noEngine []string
}

// projectsMT resolves, for each project, the engine its MT router picks for
// every target language: the matching rule's, else the router's default — the
// choice the pipeline makes (mtrouter.Pick). targets maps project id → target
// languages; every id gets an entry.
func (b *directBackend) projectsMT(ctx context.Context, tenantID int64, targets map[int64][]string) (map[int64]projectMT, error) {
	out := make(map[int64]projectMT, len(targets))
	if len(targets) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	routers, err := b.q.ListMCPProjectMTRouters(ctx, db.ListMCPProjectMTRoutersParams{TenantID: tenantID, ProjectIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list project MT routers: %w", err)
	}
	byProject := make(map[int64]db.ListMCPProjectMTRoutersRow, len(routers))
	routerIDs := make([]int64, 0, len(routers))
	for _, r := range routers {
		byProject[r.ProjectID] = r
		routerIDs = append(routerIDs, r.RouterID)
	}
	rules := map[int64][]db.ListMCPMTRouterRulesRow{}
	if len(routerIDs) > 0 {
		rows, err := b.q.ListMCPMTRouterRules(ctx, db.ListMCPMTRouterRulesParams{TenantID: tenantID, RouterIds: routerIDs})
		if err != nil {
			return nil, fmt.Errorf("list MT router rules: %w", err)
		}
		for _, r := range rows {
			rules[r.RouterID] = append(rules[r.RouterID], r)
		}
	}
	for id, langs := range targets {
		router, ok := byProject[id]
		mt := projectMT{router: router.RouterName, engines: []MTEngineUse{}}
		group := map[string]int{} // engine name → index in mt.engines
		for _, lang := range langs {
			engine := ""
			if ok {
				engine = mtrouter.Pick(rules[router.RouterID],
					func(r db.ListMCPMTRouterRulesRow) string { return r.TargetLang },
					func(r db.ListMCPMTRouterRulesRow) string { return r.Engine },
					router.DefaultEngine, lang)
			}
			if engine == "" {
				mt.noEngine = append(mt.noEngine, lang)
				continue
			}
			i, seen := group[engine]
			if !seen {
				i = len(mt.engines)
				group[engine] = i
				mt.engines = append(mt.engines, MTEngineUse{Engine: engine, Languages: []string{}})
			}
			mt.engines[i].Languages = append(mt.engines[i].Languages, lang)
		}
		out[id] = mt
	}
	return out, nil
}

func (mt projectMT) apply(s *ProjectSummary) {
	s.MTRouterName, s.MTEngines, s.MTNoEngine = mt.router, mt.engines, mt.noEngine
}
