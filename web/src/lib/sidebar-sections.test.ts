import { describe, it, expect, beforeEach } from 'vitest'
import {
  DEFAULT_LAYOUT,
  SIDEBAR_SECTION_IDS,
  STORAGE_KEY,
  loadSidebarLayout,
  moveSection,
  normalizeLayout,
  saveSidebarLayout,
  toggleSectionVisible,
  visibleSections,
  type SidebarLayout,
} from './sidebar-sections'

describe('normalizeLayout', () => {
  it('returns the default order for garbage input', () => {
    expect(normalizeLayout(null)).toEqual(DEFAULT_LAYOUT)
    expect(normalizeLayout('nope')).toEqual(DEFAULT_LAYOUT)
    expect(normalizeLayout({ order: 'tm', hidden: 42 })).toEqual(DEFAULT_LAYOUT)
  })

  it('drops unknown ids and duplicates but keeps the stored order', () => {
    const out = normalizeLayout({ order: ['comments', 'bogus', 'tm', 'tm'], hidden: ['qa', 'qa', 'nope'] })
    expect(out.order).toEqual(['comments', 'tm', 'mt', 'terminology', 'references', 'history', 'qa'])
    expect(out.hidden).toEqual(['qa'])
  })

  it('appends sections the stored order does not know about, visible', () => {
    // A layout saved before "qa" and "references" existed.
    const out = normalizeLayout({ order: ['comments', 'tm', 'mt', 'terminology'], hidden: [] })
    expect(out.order).toEqual(['comments', 'tm', 'mt', 'terminology', 'references', 'history', 'qa'])
    expect(visibleSections(out)).toHaveLength(SIDEBAR_SECTION_IDS.length)
  })
})

describe('moveSection', () => {
  it('moves a section down and up', () => {
    const down = moveSection(DEFAULT_LAYOUT, 0, 2)
    expect(down.order).toEqual(['mt', 'terminology', 'tm', 'references', 'history', 'qa', 'comments'])
    const up = moveSection(down, 2, 0)
    expect(up.order).toEqual(DEFAULT_LAYOUT.order)
  })

  it('ignores out-of-range and no-op moves', () => {
    expect(moveSection(DEFAULT_LAYOUT, 0, 0)).toBe(DEFAULT_LAYOUT)
    expect(moveSection(DEFAULT_LAYOUT, -1, 2)).toBe(DEFAULT_LAYOUT)
    expect(moveSection(DEFAULT_LAYOUT, 1, 99)).toBe(DEFAULT_LAYOUT)
  })

  it('does not mutate the input', () => {
    const before = [...DEFAULT_LAYOUT.order]
    moveSection(DEFAULT_LAYOUT, 0, 5)
    expect(DEFAULT_LAYOUT.order).toEqual(before)
  })
})

describe('toggleSectionVisible', () => {
  it('hides and re-shows a section', () => {
    const hidden = toggleSectionVisible(DEFAULT_LAYOUT, 'qa')
    expect(hidden.hidden).toEqual(['qa'])
    expect(visibleSections(hidden)).not.toContain('qa')
    const shown = toggleSectionVisible(hidden, 'qa')
    expect(shown.hidden).toEqual([])
  })

  it('refuses to hide the last visible section', () => {
    let layout: SidebarLayout = DEFAULT_LAYOUT
    for (const id of SIDEBAR_SECTION_IDS) layout = toggleSectionVisible(layout, id)
    expect(visibleSections(layout)).toHaveLength(1)
    expect(layout.hidden).toHaveLength(SIDEBAR_SECTION_IDS.length - 1)
  })
})

describe('localStorage round-trip', () => {
  beforeEach(() => localStorage.clear())

  it('returns the default when nothing is stored', () => {
    expect(loadSidebarLayout()).toEqual(DEFAULT_LAYOUT)
  })

  it('saves and loads a layout under the sidebar key', () => {
    const layout = toggleSectionVisible(moveSection(DEFAULT_LAYOUT, 5, 0), 'references')
    saveSidebarLayout(layout)
    expect(localStorage.getItem(STORAGE_KEY)).not.toBeNull()
    expect(loadSidebarLayout()).toEqual(layout)
  })

  it('falls back to the default on corrupt JSON', () => {
    localStorage.setItem(STORAGE_KEY, '{not json')
    expect(loadSidebarLayout()).toEqual(DEFAULT_LAYOUT)
  })
})
