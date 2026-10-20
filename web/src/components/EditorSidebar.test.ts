import { describe, it, expect } from 'vitest'
import { editorSidebarEqual } from './EditorSidebar'
import type { TermLookupResult } from '../api'
import type { QaFinding } from '../lib/qa-findings'

// Minimal valid Props shape — callbacks/refs are not compared so any stub works.
type ComparatorProps = Parameters<typeof editorSidebarEqual>[0]

const noop = () => {}
const onMatchHover = () => {}

function makeProps(overrides: Partial<ComparatorProps> = {}): ComparatorProps {
  const baseTerms: TermLookupResult[] = [
    { term: { id: 10, source_term: 'password', target_term: 'Passwort' } } as unknown as TermLookupResult,
  ]
  return {
    jobId: 1,
    sourceText: 'Hello',
    editText: 'Hola',
    terms: baseTerms,
    onInsertMatch: noop,
    onTermClick: noop,
    onClose: noop,
    onMatchHover,
    translationId: 99,
    ...overrides,
  }
}

describe('editorSidebarEqual (memo comparator)', () => {
  it('returns true for identical props (same terms reference)', () => {
    const base = makeProps()
    expect(editorSidebarEqual(base, { ...base })).toBe(true)
  })

  it('returns false when sourceText changes (triggers TM re-fetch)', () => {
    const base = makeProps()
    expect(editorSidebarEqual(base, { ...base, sourceText: 'World' })).toBe(false)
  })

  it('returns false when editText changes (affects term applied check)', () => {
    const base = makeProps()
    expect(editorSidebarEqual(base, { ...base, editText: 'Mundo' })).toBe(false)
  })

  it('returns false when terms array reference changes (count==0 → count==1)', () => {
    const base = makeProps({ terms: [] })
    const next = makeProps({ terms: [{ term: { id: 11 } } as unknown as TermLookupResult] })
    expect(editorSidebarEqual(base, next)).toBe(false)
  })

  // Regression — switching A (1 term) → B (1 different term) must re-render.
  // Old comparator used `prev.terms.length === next.terms.length` and returned true here,
  // so the sidebar stayed stuck on segment A's terms.
  it('returns false when terms replaced with a NEW array of same length', () => {
    const base = makeProps({
      terms: [{ term: { id: 10, source_term: 'password' } } as unknown as TermLookupResult],
    })
    const next = makeProps({
      terms: [{ term: { id: 20, source_term: 'server' } } as unknown as TermLookupResult],
    })
    expect(editorSidebarEqual(base, next)).toBe(false)
  })

  it('returns false when translationId changes (switches comment panel)', () => {
    const base = makeProps()
    expect(editorSidebarEqual(base, { ...base, translationId: 100 })).toBe(false)
  })

  it('returns false when jobId changes', () => {
    const base = makeProps()
    expect(editorSidebarEqual(base, { ...base, jobId: 2 })).toBe(false)
  })
})

// The QA findings section renders from `qaIssues`, so the
// comparator has to see a change the parent makes in place.
describe('editorSidebarEqual — qaIssues', () => {
  const issue = (over: Partial<QaFinding> = {}): QaFinding => ({
    id: 1, translation_id: 99, check_type: 'tag_missing', severity: 'error',
    message: 'Source tag(s) missing from target: </1>',
    created_at: '2026-09-07T10:00:00Z', ignored: false, ...over,
  })

  it('returns true for the same findings in a new array (no needless re-render)', () => {
    const base = makeProps({ qaIssues: [issue()] })
    expect(editorSidebarEqual(base, { ...base, qaIssues: [issue()] })).toBe(true)
  })

  // The load-bearing one: ignore/un-ignore keeps the list the same length and
  // flips one boolean. A length or reference check misses it and the section
  // keeps showing "Ignore" on a finding that is already ignored.
  it('returns false when a finding flips to ignored', () => {
    const base = makeProps({ qaIssues: [issue()] })
    expect(editorSidebarEqual(base, { ...base, qaIssues: [issue({ ignored: true })] })).toBe(false)
  })

  it('returns false when a finding is added or removed', () => {
    const base = makeProps({ qaIssues: [] })
    expect(editorSidebarEqual(base, { ...base, qaIssues: [issue()] })).toBe(false)
  })

  it('treats undefined and [] as the same (section absent vs. clean segment)', () => {
    const base = makeProps({ qaIssues: undefined })
    expect(editorSidebarEqual(base, { ...base, qaIssues: [] })).toBe(true)
  })
})
