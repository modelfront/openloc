import { describe, it, expect } from 'vitest'
import { toLangResources } from './settings-editor-options'

describe('toLangResources', () => {
  it('keeps each resource\'s entry count for the pickers', () => {
    const rows = toLangResources([
      { id: 1, name: 'Counted', source_language: 'en', target_languages: ['de'], entry_count: 42 },
      { id: 2, name: 'Uncounted', source_language: 'en', target_languages: null, entry_count: null },
    ])
    expect(rows).toEqual([
      { id: 1, name: 'Counted', source_language: 'en', target_languages: ['de'], entry_count: 42 },
      { id: 2, name: 'Uncounted', source_language: 'en', target_languages: [], entry_count: undefined },
    ])
  })
})
