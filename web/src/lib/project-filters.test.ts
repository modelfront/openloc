// Pure-function unit tests for `parseProjectFilters`.
//
// `parseProjectFilters` is the canonical URL → filters parser used by BOTH
// ProjectList (drives useInfiniteQuery) and ProjectFilterBar (drives chip
// state). Drift between the two would mean the user sees a chip selected
// for a filter that the data fetch ignores. Pin the contract here.

import { describe, it, expect } from 'vitest'
import { parseProjectFilters } from './project-filters'

const params = (init: Record<string, string>) => new URLSearchParams(init)

describe('parseProjectFilters', () => {
  it('returns empty object when no params present', () => {
    expect(parseProjectFilters(params({}))).toEqual({})
  })

  it('parses status when valid', () => {
    expect(parseProjectFilters(params({ status: 'draft' }))).toEqual({ status: 'draft' })
    expect(parseProjectFilters(params({ status: 'in_progress' }))).toEqual({ status: 'in_progress' })
    expect(parseProjectFilters(params({ status: 'on_hold' }))).toEqual({ status: 'on_hold' })
  })

  it('drops invalid status values silently (no crash on garbage URL)', () => {
    expect(parseProjectFilters(params({ status: 'garbage' }))).toEqual({})
    expect(parseProjectFilters(params({ status: '' }))).toEqual({})
  })

  it('parses customer_id and created_by as numbers', () => {
    expect(parseProjectFilters(params({ customer_id: '42', created_by: '7' }))).toEqual({
      customer_id: 42,
      created_by: 7,
    })
  })

  it('parses source/target language codes verbatim (server already normalizes)', () => {
    expect(parseProjectFilters(params({ source: 'en', target: 'fr' }))).toEqual({
      source: 'en',
      target: 'fr',
    })
  })

  it('parses q (name search) verbatim, and drops it when empty', () => {
    expect(parseProjectFilters(params({ q: 'acme' }))).toEqual({ q: 'acme' })
    expect(parseProjectFilters(params({ q: '' }))).toEqual({})
  })

  it('parses all filters together (the all-fields-at-once round-trip)', () => {
    const all = params({
      status: 'on_hold',
      customer_id: '5',
      source: 'en',
      target: 'es',
      created_by: '1',
      q: 'acme',
    })
    expect(parseProjectFilters(all)).toEqual({
      status: 'on_hold',
      customer_id: 5,
      source: 'en',
      target: 'es',
      created_by: 1,
      q: 'acme',
    })
  })

  // The query-key for useInfiniteQuery JSON.stringifies the parsed object.
  // V8 enumerates keys in insertion order, so as long as parseProjectFilters
  // builds the object in a fixed order, the stringified key is stable. Pin
  // the order — a future refactor that uses spread or a different shape
  // would silently invalidate every cached page.
  it('builds the result in stable key insertion order (status → customer_id → source → target → created_by → q)', () => {
    // Set the URL params in REVERSE order from the parser's insertion order
    // — the parsed object should still serialize in the parser's order.
    const reversed = params({
      q: 'acme',
      created_by: '1',
      target: 'es',
      source: 'en',
      customer_id: '5',
      status: 'draft',
    })
    const parsed = parseProjectFilters(reversed)
    expect(JSON.stringify(parsed)).toBe(
      '{"status":"draft","customer_id":5,"source":"en","target":"es","created_by":1,"q":"acme"}',
    )
  })
})
