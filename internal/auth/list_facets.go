package auth

import (
	"net/http"
	"slices"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// ListFacets are the values a list's filters offer: only those with at least
// one hit, given the other filters. A filter never narrows its own values, so
// a different one can still be picked.
type ListFacets struct {
	Roles   []string `json:"roles,omitempty"`
	Sources []string `json:"sources"`
	Targets []string `json:"targets"`
}

// pairFacets splits language pairs into the Source and Target filter values:
// sources of the pairs into the chosen target (any target when none is
// chosen), and targets of the pairs from the chosen source.
func pairFacets(pairs []db.ListMemberFacetPairsRow, source, target *string) (sources, targets []string) {
	sources, targets = []string{}, []string{}
	for _, p := range pairs {
		if target == nil || p.TargetLang == *target {
			sources = append(sources, p.SourceLang)
		}
		if source == nil || p.SourceLang == *source {
			targets = append(targets, p.TargetLang)
		}
	}
	slices.Sort(sources)
	slices.Sort(targets)
	return slices.Compact(sources), slices.Compact(targets)
}

// HandleMemberFacets serves the Members filters' values: the roles and the
// languages with at least one member, given the other filters (q, role,
// source, target as on /api/members/page). Owner/admin, in a customer or a
// vendor org.
func (h *Handler) HandleMemberFacets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := TenantIDFromContext(ctx)
	list, err := parseListQuery(r, membersPageMax)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	role, err := parseRoleFilter(r)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	roles, err := h.queries.ListMemberFacetRoles(ctx, db.ListMemberFacetRolesParams{
		TenantID: tenantID, Q: list.Q, SourceLang: list.Source, TargetLang: list.Target,
	})
	if httputil.ServiceError(w, err, "list", "member filters") {
		return
	}
	pairs, err := h.queries.ListMemberFacetPairs(ctx, db.ListMemberFacetPairsParams{TenantID: tenantID, Q: list.Q, Role: role})
	if httputil.ServiceError(w, err, "list", "member filters") {
		return
	}
	// The most powerful role first, as the list itself.
	slices.SortFunc(roles, func(a, b string) int { return roleRank[b] - roleRank[a] })
	sources, targets := pairFacets(pairs, list.Source, list.Target)
	httputil.Data(w, ListFacets{Roles: roles, Sources: sources, Targets: targets})
}

// HandleVendorOrgFacets serves the Vendors filters' values: the languages
// some vendor's people cover, among the vendors that match q, given the other
// side. Owner/admin, customer orgs (the vendor-org barrier refuses the route).
func (h *Handler) HandleVendorOrgFacets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := TenantIDFromContext(ctx)
	list, err := parseListQuery(r, vendorOrgsPageMax)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	rows, err := h.queries.ListVendorOrgFacetPairs(ctx, db.ListVendorOrgFacetPairsParams{ClientTenantID: &tenantID, Q: list.Q})
	if httputil.ServiceError(w, err, "list", "vendor filters") {
		return
	}
	pairs := make([]db.ListMemberFacetPairsRow, len(rows))
	for i, p := range rows {
		pairs[i] = db.ListMemberFacetPairsRow{SourceLang: p.SourceLang, TargetLang: p.TargetLang}
	}
	sources, targets := pairFacets(pairs, list.Source, list.Target)
	httputil.Data(w, ListFacets{Sources: sources, Targets: targets})
}
