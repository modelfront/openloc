import { describe, it, expect } from 'vitest'
import { distinctCreators } from './creators'

describe('distinctCreators', () => {
  it('dedupes by id and sorts by display name', () => {
    expect(distinctCreators([
      { created_by: 2, created_by_name: 'Zoe' },
      { created_by: 1, created_by_name: 'Ana' },
      { created_by: 2, created_by_name: 'Zoe' },
    ])).toEqual([{ id: 1, name: 'Ana' }, { id: 2, name: 'Zoe' }])
  })

  it('skips rows with no creator — there is no id to filter by', () => {
    expect(distinctCreators([
      { created_by: null, created_by_name: '' },
      { created_by: undefined },
      { created_by: 5, created_by_name: 'Ana' },
    ])).toEqual([{ id: 5, name: 'Ana' }])
  })

  it('falls back to #id so an unresolved name stays selectable', () => {
    expect(distinctCreators([{ created_by: 7, created_by_name: '   ' }]))
      .toEqual([{ id: 7, name: '#7' }])
  })

  it('prefers a real name over the #id fallback regardless of row order', () => {
    expect(distinctCreators([
      { created_by: 7 },
      { created_by: 7, created_by_name: 'Ana' },
    ])).toEqual([{ id: 7, name: 'Ana' }])
  })

  it('handles an absent list', () => {
    expect(distinctCreators(undefined)).toEqual([])
  })
})
