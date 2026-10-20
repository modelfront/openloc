// The Changes cell of a settings_updated event: an id field that has a name
// twin is folded into the name row, and every field the settings PATCH can
// record has a human label. A raw `mt_router_id: 3 → 4` row next to
// `MT router: Default → Production` is the noise this guards against.
import { describe, it, expect } from 'vitest'
import { flatChangeGroups } from './project-event-render'

describe('flatChangeGroups', () => {
  it('shows the MT router by name only when both id and name changed', () => {
    const groups = flatChangeGroups({
      mt_router_id: { from: 3, to: 4 },
      mt_router_name: { from: 'Default MT router', to: 'Production MT' },
    })
    expect(groups).toEqual([
      { label: 'MT router', entries: [{ from: 'Default MT router', to: 'Production MT' }] },
    ])
  })

  it('keeps the id row when the event has no name twin', () => {
    expect(flatChangeGroups({ mt_router_id: { from: 3, to: null } })).toEqual([
      { label: 'MT router ID', entries: [{ from: 3, to: null }] },
    ])
  })

  it('applies the same folding to the QA profile', () => {
    const groups = flatChangeGroups({
      qa_profile_id: { from: 1, to: 2 },
      qa_profile_name: { from: 'Strict', to: 'Lenient' },
      mt_on_upload: { from: false, to: true },
    })
    expect(groups.map(g => g.label)).toEqual(['QA profile', 'MT on upload'])
  })
})
