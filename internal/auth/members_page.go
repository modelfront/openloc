package auth

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// membersPageMax caps one page of the Members tab.
const membersPageMax = 100

// MemberPair is one of a member's language pairs, with its editor row.
type MemberPair struct {
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
	RowGroup   int32  `json:"row_group"`
}

// MemberPageItem is one row of the Members tab: the member and their pairs.
type MemberPageItem struct {
	ID        int64              `json:"id"`
	Email     string             `json:"email"`
	Name      string             `json:"name"`
	Role      string             `json:"role"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	// DisabledAt is set when a vendor org disabled the membership for disuse.
	DisabledAt    pgtype.Timestamptz `json:"disabled_at"`
	LanguagePairs []MemberPair       `json:"language_pairs"`
}

// HandleListMembersPage serves the Members tab: one page of the org's active
// members, the most powerful role first, then name. Filters: q (name or email),
// role, source and target (a language pair). The cursor is the row offset in
// that order. Owner/admin, in a customer or a vendor org.
func (h *Handler) HandleListMembersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := TenantIDFromContext(ctx)
	list, err := parseListQuery(r, membersPageMax)
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	page := list.Page
	params := db.ListMembersPageParams{
		TenantID: tenantID, Off: list.Off(), Lim: page.Limit + 1,
		Q: list.Q, SourceLang: list.Source, TargetLang: list.Target,
	}
	if params.Role, err = parseRoleFilter(r); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}

	rows, err := h.queries.ListMembersPage(ctx, params)
	if httputil.ServiceError(w, err, "list", "members") {
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
	total := httputil.PageTotal(page.Cursor, len(rows), firstTotal)

	items := make([]MemberPageItem, len(rows))
	ids := make([]int64, len(rows))
	byID := make(map[int64]*MemberPageItem, len(rows))
	for i, m := range rows {
		items[i] = MemberPageItem{ID: m.ID, Email: m.Email, Name: m.Name, Role: m.Role, CreatedAt: m.CreatedAt, DisabledAt: m.DisabledAt, LanguagePairs: []MemberPair{}}
		ids[i] = m.ID
		byID[m.ID] = &items[i]
	}
	if len(ids) > 0 {
		pairs, err := h.queries.ListLanguagePairsForMembers(ctx, db.ListLanguagePairsForMembersParams{TenantID: tenantID, UserIds: ids})
		if httputil.ServiceError(w, err, "list", "language pairs") {
			return
		}
		for _, p := range pairs {
			if m := byID[p.UserID]; m != nil {
				m.LanguagePairs = append(m.LanguagePairs, MemberPair{SourceLang: p.SourceLang, TargetLang: p.TargetLang, RowGroup: p.RowGroup})
			}
		}
	}
	httputil.Paginated(w, items, page.Cursor+int64(len(rows)), hasMore, total)
}
