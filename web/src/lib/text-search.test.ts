import { describe, it, expect } from 'vitest'
import { searchHighlightRegex, searchRanges, searchOptionsActive, searchScopeFlags, searchScopeOf, type SearchScope, type TextSearchOptions } from './text-search'

const BASE: TextSearchOptions = { searchSource: true, searchTarget: true, caseSensitive: false, useRegex: false, exactMatch: false }
const hits = (text: string, query: string, o: Partial<TextSearchOptions> = {}) => {
  const re = searchHighlightRegex(query, { ...BASE, ...o })
  return re ? searchRanges(text, re).map(r => text.slice(r.start, r.end)) : null
}

describe('searchHighlightRegex + searchRanges', () => {
  it('finds every occurrence, any case, by default', () => {
    expect(hits('Hello, hello, HELLO', 'hello')).toEqual(['Hello', 'hello', 'HELLO'])
  })

  it('treats plain text literally', () => {
    expect(hits('10x5 and 10.5', '10.5')).toEqual(['10.5'])
    expect(hits('cost (net)', '(net)')).toEqual(['(net)'])
  })

  it('respects case sensitivity', () => {
    expect(hits('Hello hello', 'Hello', { caseSensitive: true })).toEqual(['Hello'])
  })

  it('exact match tints the whole text only when it all matches', () => {
    expect(hits('Hello', 'hello', { exactMatch: true })).toEqual(['Hello'])
    expect(hits('Hello world', 'hello', { exactMatch: true })).toEqual([])
    expect(hits('cancel', 'cancel|abort', { exactMatch: true, useRegex: true })).toEqual(['cancel'])
  })

  it('runs regex patterns', () => {
    expect(hits('server servers observer', '\\bserver\\b', { useRegex: true })).toEqual(['server'])
  })

  it('skips empty matches instead of looping', () => {
    expect(hits('baaab', 'a*', { useRegex: true })).toEqual(['aaa'])
  })

  it('has nothing to tint for empty text or an invalid pattern', () => {
    expect(searchHighlightRegex('', BASE)).toBeNull()
    expect(searchHighlightRegex('(', { ...BASE, useRegex: true })).toBeNull()
    // The same text is fine as plain text.
    expect(hits('a (b', '(')).toEqual(['('])
  })
})

describe('searchOptionsActive', () => {
  it('is false only for the defaults', () => {
    expect(searchOptionsActive(BASE)).toBe(false)
    expect(searchOptionsActive({ ...BASE, exactMatch: true })).toBe(true)
    expect(searchOptionsActive({ ...BASE, searchSource: false })).toBe(true)
    expect(searchOptionsActive({ ...BASE, useRegex: true })).toBe(true)
  })
})

describe('search scope', () => {
  it('round-trips each choice through the two flags', () => {
    for (const scope of ['both', 'source', 'target'] as SearchScope[]) {
      expect(searchScopeOf({ ...BASE, ...searchScopeFlags(scope) })).toBe(scope)
    }
  })

  it('maps each choice to the fields it searches', () => {
    expect(searchScopeFlags('both')).toEqual({ searchSource: true, searchTarget: true })
    expect(searchScopeFlags('source')).toEqual({ searchSource: true, searchTarget: false })
    expect(searchScopeFlags('target')).toEqual({ searchSource: false, searchTarget: true })
  })
})
