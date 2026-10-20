// Router form rows ↔ router shape, shared by AI routers and MT routers. The
// dialog edits a list of rows "languages → engine"; the row whose languages
// include ALL_LANGUAGES names the router's default engine, and every other
// language, in that row and the others, is one rule. Engines travel as
// opaque string keys so both router kinds (one engine table vs two) fit.

export const ALL_LANGUAGES = '*'

export interface RouterRow {
  langs: string[]
  /** Engine key; '' = none picked yet. */
  engine: string
}

export const EMPTY_ROW: RouterRow = { langs: [], engine: '' }

/** What a router is, kind-agnostic: a default engine key (or null) and one
 *  rule per target language. */
export interface RouterShape {
  defaultEngine: string | null
  rules: { target_lang: string; engine: string }[]
}

/** "All languages" and specific languages are exclusive within a row: picking
 *  "All" clears the rest, picking a language drops "All". */
export function exclusiveAll(prev: string[], next: string[]): string[] {
  const hasAll = next.includes(ALL_LANGUAGES)
  if (!hasAll || next.length === 1) return next
  return prev.includes(ALL_LANGUAGES) ? next.filter(l => l !== ALL_LANGUAGES) : [ALL_LANGUAGES]
}

/** Router shape → form rows: one row per engine, its rule languages together,
 *  with ALL_LANGUAGES added to the default engine's row (or a row of its own,
 *  last). */
export function shapeToRows(shape: RouterShape): RouterRow[] {
  const rows: RouterRow[] = []
  const byEngine = new Map<string, RouterRow>()
  for (const r of shape.rules) {
    let row = byEngine.get(r.engine)
    if (!row) {
      row = { langs: [], engine: r.engine }
      byEngine.set(r.engine, row)
      rows.push(row)
    }
    row.langs.push(r.target_lang)
  }
  if (shape.defaultEngine != null) {
    const row = byEngine.get(shape.defaultEngine)
    if (row) row.langs.push(ALL_LANGUAGES)
    else rows.push({ langs: [ALL_LANGUAGES], engine: shape.defaultEngine })
  }
  return rows
}

/** Form rows → router shape. Validation has already refused incomplete rows
 *  and duplicates, so every row maps. */
export function rowsToShape(rows: RouterRow[]): RouterShape {
  let defaultEngine: string | null = null
  const rules: RouterShape['rules'] = []
  for (const r of rows) {
    for (const lang of r.langs) {
      if (lang === ALL_LANGUAGES) defaultEngine = r.engine
      else rules.push({ target_lang: lang, engine: r.engine })
    }
  }
  return { defaultEngine, rules }
}
