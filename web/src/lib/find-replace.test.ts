import { describe, expect, it } from 'vitest'

import { replaceSummary, sumReplaceCounts } from './find-replace'

const zero = { replaced: 0, segments: 0, locked: 0, held: 0, skipped_tags: 0, conflicts: 0, rejected: 0, failed: 0 }

describe('replaceSummary', () => {
  it('reports what was written and every reason a match was left alone', () => {
    expect(replaceSummary({ ...zero, replaced: 1, segments: 1 })).toBe('Replaced 1 occurrence in 1 segment')
    expect(replaceSummary({ replaced: 4, segments: 3, locked: 1, held: 1, skipped_tags: 2, conflicts: 1, rejected: 1, failed: 2 }))
      .toBe('Replaced 4 occurrences in 3 segments · 1 locked · 1 being edited by someone else · 2 cross a tag · 1 changed meanwhile · 1 would be left empty · 2 could not be saved, try again')
  })
})

describe('sumReplaceCounts', () => {
  it('adds chunk results field by field', () => {
    expect(sumReplaceCounts([{ ...zero, replaced: 2, segments: 1, locked: 1, held: 1 }, { ...zero, replaced: 3, segments: 2, skipped_tags: 1, held: 2 }]))
      .toEqual({ ...zero, replaced: 5, segments: 3, locked: 1, held: 3, skipped_tags: 1 })
  })
})
