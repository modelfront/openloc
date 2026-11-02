package issues

import (
	"sort"
	"strconv"
	"strings"
)

// SortLevel is one level of a list's order: a sort key, and whether it runs
// in reverse.
type SortLevel struct {
	Key  string
	Desc bool
}

// maxSortLevels is how many levels a client may ask for; the server adds the
// tie-breakers after them.
const maxSortLevels = 3

// The order of each ranked value, first to last in the ascending direction.
// A value missing from its list sorts after every listed one.
var (
	statusOrder   = []string{StatusNeedsInfo, StatusOpen, StatusInProgress, StatusResolved, StatusWontFix, StatusDuplicate}
	priorityOrder = []string{"high", "medium", "low"}
	kindOrder     = []string{KindBug, "enhancement", "feature_request"}
	severityOrder = []string{"blocker", "critical", "major", "minor"}
	// areaOrder is by the area's label, A to Z.
	areaOrder = keysByLabel(areaLabels)
)

func keysByLabel(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return labels[keys[i]] < labels[keys[j]] })
	return keys
}

// sortPart is one column of a sort key. expr is SQL with one %s, applied both
// to the row's column and to the cursor's value, so both are compared by the
// same expression; no part of it comes from the client. value reads the
// part's raw value from a row, for the next page's cursor.
type sortPart struct {
	expr, column, cast string
	value              func(issueRow) any
}

// sortKey is what a client may sort by: one or more parts, compared in order.
type sortKey struct {
	member, platform []sortPart // nil: not sortable in that scope
}

func rank(order []string, missing int) string {
	quoted := make([]string, len(order))
	for i, v := range order {
		quoted[i] = "'" + v + "'"
	}
	return "COALESCE(array_position(ARRAY[" + strings.Join(quoted, ",") + "]::text[], %s), " + strconv.Itoa(missing) + ")"
}

var (
	updatedPart = sortPart{"%s", "i.updated_at", "timestamptz", func(r issueRow) any { return r.UpdatedAt.Time.UnixMicro() }}
	createdPart = sortPart{"%s", "i.created_at", "timestamptz", func(r issueRow) any { return r.CreatedAt.Time.UnixMicro() }}
	idPart      = sortPart{"%s", "i.id", "bigint", func(r issueRow) any { return r.ID }}
	statusPart  = sortPart{rank(statusOrder, len(statusOrder)+1), "i.status", "text", func(r issueRow) any { return r.Status }}
	areaPart    = sortPart{rank(areaOrder, len(areaOrder)+1), "i.area", "text", func(r issueRow) any { return r.Area }}
	kindParts   = []sortPart{
		{rank(kindOrder, len(kindOrder)+1), "i.kind", "text", func(r issueRow) any { return r.Kind }},
		{rank(severityOrder, len(severityOrder)+1), "i.severity", "text", func(r issueRow) any { return r.Severity }},
	}
	priorityParts = []sortPart{{rank(priorityOrder, len(priorityOrder)+1), "i.priority", "text", func(r issueRow) any { return r.Priority }}}
	// Unassigned issues last, then by name.
	assigneeParts = []sortPart{
		{"(%s IS NULL)", "a.name", "text", func(r issueRow) any { return r.AssigneeName }},
		{"COALESCE(lower(%s), '')", "a.name", "text", func(r issueRow) any { return r.AssigneeName }},
	}
	emailPart = sortPart{"lower(%s)", "u.email", "text", func(r issueRow) any { return r.ReporterEmail }}
)

// sortKeys: the support team sees an org and an email in the "Reported by"
// column, an org's managers a name and an email, and each sorts by what it
// sees. Priority and assignee are the support team's alone.
var sortKeys = map[string]sortKey{
	"kind":    {member: kindParts, platform: kindParts},
	"area":    {member: []sortPart{areaPart}, platform: []sortPart{areaPart}},
	"status":  {member: []sortPart{statusPart}, platform: []sortPart{statusPart}},
	"updated": {member: []sortPart{updatedPart}, platform: []sortPart{updatedPart}},
	"reporter": {
		member:   []sortPart{{"lower(%s)", "u.name", "text", func(r issueRow) any { return r.ReporterName }}, emailPart},
		platform: []sortPart{{"lower(%s)", "t.name", "text", func(r issueRow) any { return r.OrgName }}, emailPart},
	},
	"priority": {platform: priorityParts},
	"assignee": {platform: assigneeParts},
}

func (k sortKey) parts(platform bool) []sortPart {
	if platform {
		return k.platform
	}
	return k.member
}

// ParseSort reads a list's sort parameter: up to three keys, comma-separated,
// each ascending or, with a leading "-", descending. Empty is no level: the
// tie-breakers alone, newest report first. platform is the support team's
// list, which has more keys.
func ParseSort(raw string, platform bool) ([]SortLevel, error) {
	if raw == "" {
		return nil, nil
	}
	fields := strings.Split(raw, ",")
	if len(fields) > maxSortLevels {
		return nil, clientError("Sort by at most 3 keys.")
	}
	levels := make([]SortLevel, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		l := SortLevel{Key: strings.TrimPrefix(f, "-"), Desc: strings.HasPrefix(f, "-")}
		k, ok := sortKeys[l.Key]
		if !ok || k.parts(platform) == nil {
			return nil, clientError("Unknown sort key: " + f)
		}
		if seen[l.Key] {
			return nil, clientError("Sort by each key once: " + l.Key)
		}
		seen[l.Key] = true
		levels = append(levels, l)
	}
	return levels, nil
}

// orderedPart is one compared column of a list's full order.
type orderedPart struct {
	sortPart
	desc bool
}

// fullOrder is levels with the tie-breakers after them: newest report first,
// then the id, so each row has one place. With no level the tie-breakers are
// the whole order, the lists' default.
func fullOrder(levels []SortLevel, platform bool) []orderedPart {
	var out []orderedPart
	for _, l := range levels {
		for _, p := range sortKeys[l.Key].parts(platform) {
			out = append(out, orderedPart{p, l.Desc})
		}
	}
	return append(out, orderedPart{createdPart, true}, orderedPart{idPart, true})
}

// sortSignature names an order, so a cursor made for one is refused by
// another.
func sortSignature(levels []SortLevel, platform bool) string {
	parts := make([]string, len(levels))
	for i, l := range levels {
		parts[i] = l.Key
		if l.Desc {
			parts[i] = "-" + l.Key
		}
	}
	scope := "m:"
	if platform {
		scope = "p:"
	}
	return scope + strings.Join(parts, ",")
}
