/**
 * Layout of the CAT editor's Resources sidebar: which sections are shown and in
 * what order. Persisted in localStorage alongside the panel's own open/closed
 * flag (`cat-sidebar-open`), so a translator's arrangement survives a reload.
 *
 * Kept as pure functions so the reordering and the storage round-trip are
 * testable without a DOM.
 */

export const SIDEBAR_SECTION_IDS = ['tm', 'mt', 'terminology', 'references', 'history', 'qa', 'comments'] as const

export type SidebarSectionId = (typeof SIDEBAR_SECTION_IDS)[number]

/** Shown in the customize popover; matches each section's own header text. */
export const SIDEBAR_SECTION_LABELS: Record<SidebarSectionId, string> = {
  tm: 'TM matches',
  mt: 'MT suggestion',
  terminology: 'Terminology',
  references: 'References',
  history: 'History',
  qa: 'QA findings',
  comments: 'Comments',
}

export const STORAGE_KEY = 'cat-sidebar-sections'

export interface SidebarLayout {
  order: SidebarSectionId[]
  /** Sections the user turned off. Absence means visible, so a section added
   *  later starts visible for everyone. */
  hidden: SidebarSectionId[]
}

export const DEFAULT_LAYOUT: SidebarLayout = { order: [...SIDEBAR_SECTION_IDS], hidden: [] }

function isSectionId(v: unknown): v is SidebarSectionId {
  return typeof v === 'string' && (SIDEBAR_SECTION_IDS as readonly string[]).includes(v)
}

function uniqueIds(v: unknown): SidebarSectionId[] {
  if (!Array.isArray(v)) return []
  const seen = new Set<SidebarSectionId>()
  for (const item of v) if (isSectionId(item) && !seen.has(item)) seen.add(item)
  return [...seen]
}

/**
 * Turns whatever is in storage into a usable layout: unknown ids are dropped,
 * duplicates collapse, and a section the stored order never heard of is
 * appended at the end (visible). The user then moves it where they want it.
 */
export function normalizeLayout(raw: unknown): SidebarLayout {
  const parsed = (raw ?? {}) as Partial<SidebarLayout>
  const order = uniqueIds(parsed.order)
  const missing = SIDEBAR_SECTION_IDS.filter(id => !order.includes(id))
  return { order: [...order, ...missing], hidden: uniqueIds(parsed.hidden) }
}

export function loadSidebarLayout(): SidebarLayout {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_LAYOUT
    return normalizeLayout(JSON.parse(raw))
  } catch {
    return DEFAULT_LAYOUT
  }
}

export function saveSidebarLayout(layout: SidebarLayout): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(layout))
  } catch { /* storage blocked */ }
}

/** Moves the section at `from` to index `to`; out-of-range moves change nothing. */
export function moveSection(layout: SidebarLayout, from: number, to: number): SidebarLayout {
  const { order } = layout
  if (from < 0 || to < 0 || from >= order.length || to >= order.length || from === to) return layout
  const next = [...order]
  next.splice(to, 0, ...next.splice(from, 1))
  return { ...layout, order: next }
}

export function isSectionVisible(layout: SidebarLayout, id: SidebarSectionId): boolean {
  return !layout.hidden.includes(id)
}

/**
 * Flips one section's visibility. The last visible section cannot be turned off
 * — an empty panel has no way back other than this popover, which would then be
 * the only thing in it.
 */
export function toggleSectionVisible(layout: SidebarLayout, id: SidebarSectionId): SidebarLayout {
  if (!isSectionVisible(layout, id)) return { ...layout, hidden: layout.hidden.filter(h => h !== id) }
  if (layout.hidden.length >= SIDEBAR_SECTION_IDS.length - 1) return layout
  return { ...layout, hidden: [...layout.hidden, id] }
}

/** The sections to render, in order, skipping the hidden ones. */
export function visibleSections(layout: SidebarLayout): SidebarSectionId[] {
  return layout.order.filter(id => isSectionVisible(layout, id))
}
