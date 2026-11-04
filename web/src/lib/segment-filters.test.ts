import { describe, it, expect } from 'vitest'
import { countByDisplayStatus, filterSegments, isFilterActive, DEFAULT_FILTERS, type QAFilter, type SegmentFilters } from './segment-filters'
import { getRegexError } from './text-search'
import type { IndexRow } from './editor-index'

function row(segmentId: number, translationId: number | null, over: Partial<IndexRow> = {}): IndexRow {
  return {
    segmentId, translationId, position: segmentId - 1, subPosition: 0, joinsNext: false, wordCount: 2,
    status: 'needs_review', hasText: false, stepEdited: false, predicted: false, origin: '', tmMatch: null,
    locked: false, inFlight: '', confirmedVia: '', qeNoteHead: '', repetition: 0, fileIdx: 0, ...over,
  }
}

// translation IDs: seg 1 → 10, seg 2 → 11, seg 3 → 12
const segments: IndexRow[] = [
  // Origin is the sticky automation lineage: tm|mt|ape|file_import.
  row(1, 10, { status: 'approved', hasText: true, origin: 'mt' }),
  // stepEdited → derives as needs_review (edited in this step), not
  // pre_translated. origin='tm' still drives the origin filter.
  row(2, 11, { hasText: true, stepEdited: true, origin: 'tm', tmMatch: 80 }),
  row(3, 12),
]

const emptyQA = new Map()

describe('isFilterActive', () => {
  it('returns false for default filters', () => {
    expect(isFilterActive(DEFAULT_FILTERS)).toBe(false)
  })
  it('returns true when status filter set', () => {
    expect(isFilterActive({ ...DEFAULT_FILTERS, statuses: ['approved'] })).toBe(true)
  })
  it('returns true when search text set', () => {
    expect(isFilterActive({ ...DEFAULT_FILTERS, searchText: 'hello' })).toBe(true)
  })
  it('advanced flags alone do not activate filtering', () => {
    expect(isFilterActive({ ...DEFAULT_FILTERS, caseSensitive: true, useRegex: true, searchTarget: false })).toBe(false)
  })
})

describe('filterSegments', () => {
  const run = (overrides: Partial<SegmentFilters>, textMatches: ReadonlySet<number> | null = null, counts?: Map<number, number>) =>
    filterSegments(segments, emptyQA, { ...DEFAULT_FILTERS, ...overrides }, textMatches, counts)

  it('returns all indices when no filter active', () => {
    expect(run({})).toEqual([0, 1, 2])
  })

  it('filters by status', () => {
    expect(run({ statuses: ['approved'] })).toEqual([0])
  })

  it('filters by multiple statuses', () => {
    expect(run({ statuses: ['approved', 'untranslated'] })).toEqual([0, 2])
  })

  it('filters by origin', () => {
    // seg2 has origin 'tm'; seg1 has 'mt'; seg3 has none — filtering 'tm' selects only [1].
    expect(run({ origins: ['tm'] })).toEqual([1])
  })

  // The text search itself runs on the server; the segments it found arrive
  // as a set of segment ids.
  it('keeps the segments the server search found', () => {
    expect(run({ searchText: 'password' }, new Set([2]))).toEqual([1])
  })

  it('applies no text filter without search matches', () => {
    expect(run({ searchText: 'password' }, null)).toEqual([0, 1, 2])
  })

  it('shows nothing when the search found nothing', () => {
    expect(run({ searchText: 'xyz123' }, new Set())).toEqual([])
  })

  it('combines status + search (AND)', () => {
    expect(run({ statuses: ['needs_review'], searchText: 'o' }, new Set([1, 2]))).toEqual([1])
  })

  it('ignores stale matches once the search text is cleared', () => {
    expect(run({ searchText: '  ' }, new Set([2]))).toEqual([0, 1, 2])
  })

  describe('hasComments', () => {
    const counts = new Map<number, number>([[11, 2]])
    const runWithCounts = (overrides: Partial<SegmentFilters>, c = counts) =>
      run(overrides, null, c)

    it('keeps only segments whose translation has comments', () => {
      expect(runWithCounts({ hasComments: true })).toEqual([1])
    })

    it('is inactive by default', () => {
      expect(isFilterActive({ ...DEFAULT_FILTERS, hasComments: false })).toBe(false)
      expect(runWithCounts({})).toEqual([0, 1, 2])
    })

    it('counts as an active filter on its own', () => {
      expect(isFilterActive({ ...DEFAULT_FILTERS, hasComments: true })).toBe(true)
    })

    it('matches nothing when no counts map is supplied', () => {
      expect(run({ hasComments: true })).toEqual([])
    })

    it('ignores zero counts (a deleted-to-empty thread is not "commented")', () => {
      expect(runWithCounts({ hasComments: true }, new Map([[11, 0]]))).toEqual([])
    })

    it('composes with other filters (AND, not OR)', () => {
      // Segment 2 has comments but is needs_review, so an approved-only
      // status filter must drop it rather than let the comment filter win.
      expect(runWithCounts({ hasComments: true, statuses: ['approved'] })).toEqual([])
      expect(runWithCounts({ hasComments: true, statuses: ['needs_review'] })).toEqual([1])
    })
  })

})

describe('countByDisplayStatus', () => {
  it('counts an earlier step\'s AI verdict as pretranslated, a verdict from this step as draft', () => {
    const scored = [row(1, 10, { hasText: true, predicted: true }), segments[1], segments[2]]
    expect(countByDisplayStatus(scored).needs_review).toBe(2)
    const before = countByDisplayStatus(scored, new Set([10]))
    expect(before.needs_review).toBe(1)
    expect(before.pre_translated).toBe(1)
    expect(filterSegments(scored, emptyQA, { ...DEFAULT_FILTERS, statuses: ['pre_translated'] }, null, undefined, new Set([10]))).toEqual([0])
  })

  it('counts each segment once, by the status the status filter matches on', () => {
    // Segment 4 has no translation row at all: still untranslated.
    const all = [...segments, row(4, null)]
    const counts = countByDisplayStatus(all)
    expect(counts).toEqual({ untranslated: 2, pre_translated: 0, needs_review: 1, approved: 1, auto_approved: 0 })
    // Each count is exactly what that one-status filter keeps.
    for (const [status, n] of Object.entries(counts)) {
      expect(filterSegments(all, emptyQA, { ...DEFAULT_FILTERS, statuses: [status] }, null)).toHaveLength(n)
    }
  })
})

describe('getRegexError', () => {
  it('returns a message for an invalid pattern', () => {
    expect(getRegexError('(')).toBeTruthy()
  })
  it('returns null for a valid pattern', () => {
    expect(getRegexError('a|b')).toBeNull()
  })
})

describe('QA filters', () => {
  const qa = (over: Record<string, unknown>) => ({
    id: 1, translation_id: 10, check_type: 'punctuation', severity: 'error',
    message: 'm', created_at: '', ignored: false, ...over,
  })
  // seg 1 (tr 10, approved): one active error. seg 2 (tr 11): one IGNORED
  // error only. seg 3 (tr 12): one active warning. seg 4 (tr 13, approved):
  // no findings. seg 5: no translation yet.
  const qaSegments: IndexRow[] = [
    ...segments,
    row(4, 13, { status: 'approved', hasText: true }),
    row(5, null),
  ]
  const qaMap = new Map([
    [10, [qa({ id: 1, translation_id: 10 })]],
    [11, [qa({ id: 2, translation_id: 11, ignored: true })]],
    [12, [qa({ id: 3, translation_id: 12, severity: 'warning' })]],
  ])
  const run = (qaFilters: QAFilter[], extra: Partial<SegmentFilters> = {}) =>
    filterSegments(qaSegments, qaMap as never, { ...DEFAULT_FILTERS, qaFilters, ...extra }, null)

  it('has_errors excludes a segment whose only error is ignored', () => {
    expect(run(['has_errors'])).toEqual([0])
  })
  it('has_warnings selects warnings only, not segments with just an error', () => {
    expect(run(['has_warnings'])).toEqual([2])
  })
  it('no_issues includes a segment whose only finding is ignored, and segments with no findings', () => {
    expect(run(['no_issues'])).toEqual([1, 3, 4])
  })
  it('ignored selects only segments with an ignored finding', () => {
    expect(run(['ignored'])).toEqual([1])
  })
  it('several picked filters show segments matching any of them', () => {
    expect(run(['has_errors', 'has_warnings'])).toEqual([0, 2])
    expect(run(['has_errors', 'ignored'])).toEqual([0, 1])
  })
  it('the picked QA filters still narrow the status filter', () => {
    expect(run(['has_errors', 'has_warnings'], { statuses: ['approved'] })).toEqual([0])
  })
  it('a picked QA filter turns filtering on', () => {
    expect(isFilterActive({ ...DEFAULT_FILTERS, qaFilters: ['ignored'] })).toBe(true)
  })
})
