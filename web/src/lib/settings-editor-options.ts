// The SettingsEditor's pickers take only what they show: a TM or term base with
// its languages and size, a profile by name. Every page that renders the editor
// maps its queries through these.

interface LangResource {
  id: number
  name: string
  source_language?: string | null
  target_languages?: string[] | null
  entry_count?: number | null
}

/** TMs and term bases, with their languages and entry count; the source keeps the type it came with. */
export function toLangResources<T extends LangResource>(rows: T[] | undefined) {
  return rows?.map(r => ({
    id: r.id, name: r.name, source_language: r.source_language as T['source_language'], target_languages: r.target_languages ?? [],
    entry_count: r.entry_count ?? undefined,
  })) ?? []
}

/** QA and TM profiles, by name. */
export function toNamedOptions(rows: { id: number; name: string }[] | undefined) {
  return rows?.map(r => ({ id: r.id, name: r.name })) ?? []
}
