// Name search for the lists fetched whole and filtered in the browser (TMs, TM
// profiles, term bases).

// Lower-case with accents dropped, so "medical" finds "Médical". Not
// toLocaleLowerCase: in a Turkish browser "I" folds to a dotless "ı".
function fold(s: string): string {
  return s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()
}

/** Whether `name` contains `query`, ignoring case, accents and the query's outer spaces. An empty query matches every name. */
export function matchesName(name: string, query: string): boolean {
  const q = query.trim()
  return q === '' || fold(name).includes(fold(q))
}

interface NamedRow {
  name: string
  created_by?: number | null
}

/** The rows whose name matches `query` and, when `createdBy` is set, that this user created. */
export function filterByNameAndCreator<T extends NamedRow>(rows: T[] | undefined, query: string, createdBy: number | null): T[] | undefined {
  return rows?.filter(r => matchesName(r.name, query) && (createdBy == null || r.created_by === createdBy))
}
