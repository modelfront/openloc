import { describe, it, expect } from 'vitest'
import { countByDisplayStatus, filterSegments, isFilterActive, DEFAULT_FILTERS, type SegmentFilters } from './segment-filters'
import { getRegexError } from './text-search'
import type { Segment, Translation } from '../api'

const segments: Segment[] = [
  { id: 1, file_id: 1, source_text: 'Hello world',    position: 0, segment_key: '1', word_count: 2 },
  { id: 2, file_id: 1, source_text: 'Reset password', position: 1, segment_key: '2', word_count: 2 },
  { id: 3, file_id: 1, source_text: 'Thank you',      position: 2, segment_key: '3', word_count: 2 },
]

const translations = new Map<number, Translation>([
  // Origin is the sticky automation-lineage object
  // {translation_origin, valid} with values tm|mt|ape|file_import.
  [1, { id: 10, segment_id: 1, target_language: 'es', status: 'approved', active_text: 'Hola mundo', tm_match_percent: null, origin: { translation_origin: 'mt', valid: true }, predict_outcome: { predict_outcome: '', valid: false } } as unknown as Translation],
  // step_edited → derives as needs_review (edited in this step), not
  // pre_translated (deriveDisplayStatus). origin='tm' still drives the origin
  // filter.
  [2, { id: 11, segment_id: 2, target_language: 'es', status: 'needs_review', active_text: 'Restablecer contraseña', edited_by: 1, step_edited: true, tm_match_percent: 80, origin: { translation_origin: 'tm', valid: true }, predict_outcome: { predict_outcome: '', valid: false } } as unknown as Translation],
  [3, { id: 12, segment_id: 3, target_language: 'es', status: 'untranslated', active_text: null, tm_match_percent: null, predict_outcome: { predict_outcome: '', valid: false } } as unknown as Translation],
])

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
})

describe('filterSegments', () => {
  it('returns all indices when no filter active', () => {
    expect(filterSegments(segments, translations, emptyQA, DEFAULT_FILTERS)).toEqual([0, 1, 2])
  })

  it('filters by status', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, statuses: ['approved'] }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([0])
  })

  it('filters by multiple statuses', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, statuses: ['approved', 'untranslated'] }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([0, 2])
  })

  it('filters by text search (source)', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, searchText: 'password' }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([1])
  })

  it('filters by text search (target)', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, searchText: 'contraseña' }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([1])
  })

  it('filters by origin', () => {
    // seg2 has origin 'tm'; seg1 has 'mt'; seg3 has none — filtering 'tm' selects only [1].
    const f: SegmentFilters = { ...DEFAULT_FILTERS, origins: ['tm'] }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([1])
  })

  it('combines status + search', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, statuses: ['needs_review'], searchText: 'password' }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([1])
  })

  it('returns empty for no matches', () => {
    const f: SegmentFilters = { ...DEFAULT_FILTERS, searchText: 'xyz123' }
    expect(filterSegments(segments, translations, emptyQA, f)).toEqual([])
  })
})

describe('countByDisplayStatus', () => {
  it('counts an earlier step\'s AI verdict as pretranslated, a verdict from this step as draft', () => {
    const scored = new Map(translations)
    scored.set(1, { ...translations.get(1)!, status: 'needs_review', predict_outcome: { predict_outcome: 'rejected', valid: true } })
    expect(countByDisplayStatus(segments, scored).needs_review).toBe(2)
    const before = countByDisplayStatus(segments, scored, new Set([10]))
    expect(before.needs_review).toBe(1)
    expect(before.pre_translated).toBe(1)
    expect(filterSegments(segments, scored, emptyQA, { ...DEFAULT_FILTERS, statuses: ['pre_translated'] }, undefined, new Set([10]))).toEqual([0])
  })

  it('counts each segment once, by the status the status filter matches on', () => {
    // Segment 4 has no translation row at all: still untranslated.
    const all = [...segments, { id: 4, file_id: 1, source_text: 'Bye', position: 3, segment_key: '4', word_count: 1 }]
    const counts = countByDisplayStatus(all, translations)
    expect(counts).toEqual({ untranslated: 2, pre_translated: 0, needs_review: 1, approved: 1, auto_approved: 0 })
    // Each count is exactly what that one-status filter keeps.
    for (const [status, n] of Object.entries(counts)) {
      expect(filterSegments(all, translations, emptyQA, { ...DEFAULT_FILTERS, statuses: [status] })).toHaveLength(n)
    }
  })
})

describe('advanced search', () => {
  const run = (overrides: Partial<SegmentFilters>) =>
    filterSegments(segments, translations, emptyQA, { ...DEFAULT_FILTERS, ...overrides })

  it('case sensitive: exact case matches, wrong case does not', () => {
    expect(run({ searchText: 'hello', caseSensitive: true })).toEqual([])
    expect(run({ searchText: 'Hello', caseSensitive: true })).toEqual([0])
  })

  it('scope source-only ignores target text', () => {
    // 'contraseña' exists only in seg2's target — source-only must miss it.
    expect(run({ searchText: 'contraseña', searchTarget: false })).toEqual([])
    expect(run({ searchText: 'password', searchTarget: false })).toEqual([1])
  })

  it('scope target-only ignores source text', () => {
    expect(run({ searchText: 'password', searchSource: false })).toEqual([])
    expect(run({ searchText: 'Hola', searchSource: false })).toEqual([0])
  })

  it('both scopes off matches nothing (defensive — UI prevents this state)', () => {
    expect(run({ searchText: 'Hello', searchSource: false, searchTarget: false })).toEqual([])
  })

  it('regex: anchors and classes work', () => {
    expect(run({ searchText: '^Hello', useRegex: true })).toEqual([0])
    expect(run({ searchText: 'rese[t]', useRegex: true })).toEqual([1]) // case-insensitive by default
  })

  it('regex respects case sensitivity', () => {
    expect(run({ searchText: 'rese[t]', useRegex: true, caseSensitive: true })).toEqual([])
    expect(run({ searchText: 'Rese[t]', useRegex: true, caseSensitive: true })).toEqual([1])
  })

  it('invalid regex matches nothing and does not throw', () => {
    expect(run({ searchText: '(', useRegex: true })).toEqual([])
  })

  it('regex metacharacters are literal when regex is off', () => {
    // '(' as plain substring: no segment contains it → empty, but no throw either.
    expect(run({ searchText: '(' })).toEqual([])
    expect(run({ searchText: 'Hello world' })).toEqual([0])
  })

  // translation IDs: seg 1 → 10, seg 2 → 11, seg 3 → 12
  describe('hasComments', () => {
    const counts = new Map<number, number>([[11, 2]])
    const runWithCounts = (overrides: Partial<SegmentFilters>, c = counts) =>
      filterSegments(segments, translations, emptyQA, { ...DEFAULT_FILTERS, ...overrides }, c)

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
      expect(filterSegments(segments, translations, emptyQA, { ...DEFAULT_FILTERS, hasComments: true })).toEqual([])
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

  it('advanced flags alone do not activate filtering', () => {
    expect(isFilterActive({ ...DEFAULT_FILTERS, caseSensitive: true, useRegex: true, searchTarget: false })).toBe(false)
    expect(run({ caseSensitive: true, useRegex: true, searchTarget: false })).toEqual([0, 1, 2])
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

describe('QA filter with ignored findings', () => {
  const qa = (over: Record<string, unknown>) => ({
    id: 1, translation_id: 10, check_type: 'punctuation', severity: 'error',
    message: 'm', created_at: '', ignored: false, ...over,
  })
  // seg 1 (tr 10): one active error. seg 2 (tr 11): one IGNORED error only.
  const qaMap = new Map([
    [10, [qa({ id: 1, translation_id: 10 })]],
    [11, [qa({ id: 2, translation_id: 11, ignored: true })]],
  ])
  const run = (qaFilter: SegmentFilters['qaFilter']) =>
    filterSegments(segments, translations, qaMap as never, { ...DEFAULT_FILTERS, qaFilter })

  it('has_errors excludes a segment whose only error is ignored', () => {
    expect(run('has_errors')).toEqual([0])
  })
  it('no_issues includes a segment whose only finding is ignored', () => {
    expect(run('no_issues')).toEqual([1, 2])
  })
  it('ignored selects only segments with an ignored finding', () => {
    expect(run('ignored')).toEqual([1])
  })
})
