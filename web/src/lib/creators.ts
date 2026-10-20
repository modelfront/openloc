// Distinct creators present in a whole-fetch asset list (TMs, term bases).
//
// Why derive from the rows instead of listing org members: an option that can
// only ever produce "no results" is noise. A member who never created a TM has
// no business in the TM list's creator filter — same reasoning as the Tasks
// toolbar deriving its Target/Assignee options from the tasks actually present. It also
// surfaces creators the member list omits, since /api/members drops deactivated
// users while their assets (and their names, via created_by_name) remain.
//
// Only for lists fetched whole. The projects list is cursor-paginated, so its
// rows can't know about creators on later pages — that chip keeps using
// /api/members.

export interface CreatorRef {
  id: number
  name: string
}

interface AttributedRow {
  created_by?: number | null
  created_by_name?: string
}

/**
 * Unique creators across `rows`, sorted by display name.
 *
 * Rows with no creator (`created_by` null — created before attribution, or by a
 * system path) contribute nothing: there's no id to filter by. A creator whose
 * name didn't resolve falls back to `#<id>` so they stay selectable rather than
 * collapsing into a blank option.
 */
export function distinctCreators(rows: AttributedRow[] | undefined): CreatorRef[] {
  // Ids and names are tracked separately so the `#id` fallback is only applied
  // once every row has been seen. Resolving inline instead would let a row with
  // no name claim the id first and block the real name behind it (`#7` being a
  // perfectly truthy string).
  const seen = new Set<number>()
  const names = new Map<number, string>()
  for (const row of rows ?? []) {
    const id = row.created_by
    if (id == null) continue
    seen.add(id)
    const name = row.created_by_name?.trim()
    // First real name wins; rows for one creator agree in practice.
    if (name && !names.has(id)) names.set(id, name)
  }
  return [...seen]
    .map(id => ({ id, name: names.get(id) ?? `#${id}` }))
    .sort((a, b) => a.name.localeCompare(b.name))
}
