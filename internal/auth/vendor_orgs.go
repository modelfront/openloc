package auth

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// vendorOrgsPageMax caps one page of the Vendors list.
const vendorOrgsPageMax = 100

// VendorOrgPair is a language pair someone in a vendor org works in.
type VendorOrgPair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
}

// VendorOrgAdmin is an admin of a vendor org as its client sees them.
// Disabled: the vendor disabled their membership for disuse; the client may
// enable it.
type VendorOrgAdmin struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
}

// VendorOrgItem is one row of the Vendors list: the vendor org, its admins
// and the pairs its people cover.
type VendorOrgItem struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
	Admins        []VendorOrgAdmin   `json:"admins"`
	LanguagePairs []VendorOrgPair    `json:"language_pairs"`
}

// HandleListVendorOrgs lists one page of the vendor orgs the current org
// invited, by name. Filters: q (the vendor's name or an admin's email), source
// and target (a pair someone in the vendor org has). The cursor is the row
// offset. Owner/admin; a vendor org never reaches it (the vendor-org barrier
// refuses the route).
func (h *Handler) HandleListVendorOrgs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := TenantIDFromContext(ctx)
	list, err := parseListQuery(r, vendorOrgsPageMax)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	page := list.Page
	rows, err := h.queries.ListVendorOrgsForClient(ctx, db.ListVendorOrgsForClientParams{
		ClientTenantID: &tenantID, Q: list.Q, SourceLang: list.Source, TargetLang: list.Target,
		Off: list.Off(), Lim: page.Limit + 1,
	})
	if httputil.ServiceError(w, err, "list", "vendor organizations") {
		return
	}
	hasMore := len(rows) > int(page.Limit)
	if hasMore {
		rows = rows[:page.Limit]
	}
	var firstTotal int64
	if len(rows) > 0 {
		firstTotal = rows[0].Total
	}

	items := make([]VendorOrgItem, len(rows))
	ids := make([]int64, len(rows))
	byID := make(map[int64]*VendorOrgItem, len(rows))
	for i, v := range rows {
		items[i] = VendorOrgItem{ID: v.ID, Name: v.Name, CreatedAt: v.CreatedAt, Admins: []VendorOrgAdmin{}, LanguagePairs: []VendorOrgPair{}}
		if err := json.Unmarshal(v.Admins, &items[i].Admins); httputil.ServiceError(w, err, "read", "vendor admins") {
			return
		}
		ids[i] = v.ID
		byID[v.ID] = &items[i]
	}
	if len(ids) > 0 {
		pairs, err := h.queries.ListVendorOrgLanguagePairs(ctx, db.ListVendorOrgLanguagePairsParams{ClientTenantID: &tenantID, VendorIds: ids})
		if httputil.ServiceError(w, err, "list", "vendor language pairs") {
			return
		}
		for _, p := range pairs {
			if v := byID[p.TenantID]; v != nil {
				v.LanguagePairs = append(v.LanguagePairs, VendorOrgPair{SourceLang: p.SourceLang, TargetLang: p.TargetLang})
			}
		}
	}
	httputil.Paginated(w, items, page.Cursor+int64(len(rows)), hasMore, httputil.PageTotal(page.Cursor, len(rows), firstTotal))
}
