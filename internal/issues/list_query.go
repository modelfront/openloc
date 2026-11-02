package issues

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// The two list queries, without their order: every column of
// GetIssueForPlatform in its order, so a row scans into its type by position.
// The order, the keyset condition after the cursor and the limit are added
// from the sort registry, which holds constant SQL only.

// memberListBase is the org's issues the viewer may see: every member's for a
// manager, else their own. An empty filter list matches any value; a NULL
// search matches any text, else a part of the title or description
// (wildcards escaped by the caller). The original of a duplicate keeps its
// title only when the viewer may see it.
const memberListBase = `
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(CASE WHEN d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id) THEN d.title END, '')::text AS duplicate_title,
       d.status AS duplicate_status, COALESCE(d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id), false)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.tenant_id = @tenant_id AND (@all_members::boolean OR i.reporter_id = @viewer_id)
  AND i.deleted_at IS NULL
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND (@search::text IS NULL
       OR i.title ILIKE '%' || @search::text || '%'
       OR i.description ILIKE '%' || @search::text || '%')`

// platformListBase is every org's issues, for platform operators only
// (RequirePlatformAdmin), except an org being deleted. Filtered like
// memberListBase; the reporter filter matches an issue of a listed org or by a
// listed reporter, a tag filter an issue with any of the tags, the
// assignee filter the listed assignees plus the unassigned issues when
// @unassigned, and a verification filter the reports the assistant checked
// with one of those results.
const platformListBase = `
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(d.title, '')::text AS duplicate_title,
       d.status AS duplicate_status, (d.id IS NOT NULL)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.deleted_at IS NULL AND t.status <> 'deleting'
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND ((cardinality(@tenant_ids::bigint[]) = 0 AND cardinality(@reporter_ids::bigint[]) = 0)
       OR i.tenant_id = ANY(@tenant_ids::bigint[])
       OR i.reporter_id = ANY(@reporter_ids::bigint[]))
  AND (cardinality(@priorities::text[]) = 0 OR i.priority = ANY(@priorities::text[]))
  AND (cardinality(@tags::text[]) = 0 OR i.tags && @tags::text[])
  AND ((cardinality(@assignee_ids::bigint[]) = 0 AND NOT @unassigned::boolean)
       OR i.assignee_id = ANY(@assignee_ids::bigint[])
       OR (@unassigned::boolean AND i.assignee_id IS NULL))
  AND (cardinality(@verifications::text[]) = 0 OR i.chat_verification = ANY(@verifications::text[]))
  AND (@search::text IS NULL
       OR i.title ILIKE '%' || @search::text || '%'
       OR i.description ILIKE '%' || @search::text || '%')`

// listSQL is base with the rows after the cursor (when withCursor) and the
// order. The cursor's values are the parameters @c0, @c1, … in order's order.
// When every column runs in one direction the condition is a row comparison,
// which an index on those columns can serve; else it is the expanded form.
func listSQL(base string, order []orderedPart, withCursor bool) string {
	cols := make([]string, len(order))
	curs := make([]string, len(order))
	by := make([]string, len(order))
	uniform := true
	for i, p := range order {
		cols[i] = fmt.Sprintf(p.expr, p.column)
		curs[i] = fmt.Sprintf(p.expr, "@c"+strconv.Itoa(i)+"::"+p.cast)
		by[i] = cols[i] + direction(p.desc, " DESC", " ASC")
		uniform = uniform && p.desc == order[0].desc
	}
	var b strings.Builder
	b.WriteString(base)
	if withCursor {
		b.WriteString("\n  AND (")
		if uniform {
			b.WriteString("(" + strings.Join(cols, ", ") + ") " + direction(order[0].desc, "<", ">") + " (" + strings.Join(curs, ", ") + ")")
		} else {
			alts := make([]string, len(order))
			for i, p := range order {
				terms := make([]string, 0, i+1)
				for j := 0; j < i; j++ {
					terms = append(terms, cols[j]+" = "+curs[j])
				}
				terms = append(terms, cols[i]+direction(p.desc, " < ", " > ")+curs[i])
				alts[i] = "(" + strings.Join(terms, " AND ") + ")"
			}
			b.WriteString(strings.Join(alts, "\n       OR "))
		}
		b.WriteString(")")
	}
	b.WriteString("\nORDER BY " + strings.Join(by, ", ") + "\nLIMIT @row_limit")
	return b.String()
}

func direction(desc bool, ifDesc, ifAsc string) string {
	if desc {
		return ifDesc
	}
	return ifAsc
}

// countRows is how many rows base selects with args: the whole list, every
// page, counted by the same query the list reads.
func (s *Service) countRows(ctx context.Context, base string, args pgx.NamedArgs) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM ("+base+"\n) listed", args).Scan(&n); err != nil {
		return 0, fmt.Errorf("count issues: %w", err)
	}
	return n, nil
}

// listPage runs one page of a list: up to limit rows after cursor in f's
// order, and the cursor of the next page ("" when there is none). args holds
// the base query's parameters.
func (s *Service) listPage(ctx context.Context, base string, args pgx.NamedArgs, f Filter, platform bool, cursor string, limit int32) ([]issueRow, string, error) {
	order, sig := fullOrder(f.Sort, platform), sortSignature(f.Sort, platform)
	after, err := decodeCursor(cursor, sig, order)
	if err != nil {
		return nil, "", err
	}
	for i, v := range after {
		args["c"+strconv.Itoa(i)] = v
	}
	args["row_limit"] = limit + 1
	rows, err := s.pool.Query(ctx, listSQL(base, order, after != nil), args)
	if err != nil {
		return nil, "", fmt.Errorf("list issues: %w", err)
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[db.GetIssueForPlatformRow])
	if err != nil {
		return nil, "", fmt.Errorf("list issues: %w", err)
	}
	out := make([]issueRow, 0, len(list))
	for _, r := range list {
		out = append(out, issueRow(r))
	}
	if int32(len(out)) <= limit {
		return out, "", nil
	}
	out = out[:limit]
	next, err := encodeCursor(sig, order, out[len(out)-1])
	if err != nil {
		return nil, "", err
	}
	return out, next, nil
}
