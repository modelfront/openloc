import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { WhitespaceText, findTextRange, segmentRowEqual as realSegmentRowEqual } from './SegmentRow'
import { HIGHLIGHT_TINT } from '../lib/text-highlight'

// ---------- findTextRange (select match in editor on Find navigation) ----------

describe('findTextRange', () => {
  it('finds first occurrence (case-insensitive)', () => {
    expect(findTextRange('Wir haben das Wir', 'wir')).toEqual({ from: 0, to: 3 })
  })

  it('returns null when no match', () => {
    expect(findTextRange('Hello world', 'xyz')).toBeNull()
  })

  it('returns null for empty term', () => {
    expect(findTextRange('Hello', '')).toBeNull()
    expect(findTextRange('Hello', null)).toBeNull()
  })

  it('finds match at end of text', () => {
    expect(findTextRange('das ist Wir', 'wir')).toEqual({ from: 8, to: 11 })
  })

  it('handles special regex characters in term', () => {
    expect(findTextRange('price is $100.00', '$100.00')).toEqual({ from: 9, to: 16 })
  })
})

// ---------- WhitespaceText (highlight + whitespace coexistence) ----------

describe('WhitespaceText', () => {
  it('renders plain text without highlights when no highlightTerm', () => {
    const { container } = render(WhitespaceText({ text: 'Hello world', highlightTerm: null }))
    expect(container.textContent).toBe('Hello world')
    expect(container.querySelector('mark')).toBeNull()
  })

  it('renders whitespace markers without highlights when no highlightTerm', () => {
    const { container } = render(WhitespaceText({ text: 'Hello world' }))
    // Space between Hello and world should have ws-dot class
    const wsDots = container.querySelectorAll('.ws-dot')
    expect(wsDots.length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('mark')).toBeNull()
  })

  it('renders highlight marks AND whitespace markers together', () => {
    const { container } = render(WhitespaceText({ text: 'Wir haben das', highlightTerm: 'Wir' }))
    // Should have yellow highlight
    const marks = container.querySelectorAll('mark')
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('Wir')
    expect(marks[0].className).toContain(HIGHLIGHT_TINT)
    // Should still have whitespace markers for spaces
    const wsDots = container.querySelectorAll('.ws-dot')
    expect(wsDots.length).toBeGreaterThanOrEqual(1)
  })

  it('highlights multiple occurrences (no lastIndex bug)', () => {
    const { container } = render(WhitespaceText({ text: 'Wir kennen Wir und Wir', highlightTerm: 'Wir' }))
    const marks = container.querySelectorAll('mark')
    // Must find all 3 occurrences (a stateful /g regex would skip some).
    expect(marks.length).toBe(3)
    for (const mark of marks) {
      expect(mark.textContent).toBe('Wir')
    }
  })

  it('highlights case-insensitively', () => {
    const { container } = render(WhitespaceText({ text: 'wir WIR Wir', highlightTerm: 'wir' }))
    const marks = container.querySelectorAll('mark')
    expect(marks.length).toBe(3)
  })

  it('renders no highlights when term does not match', () => {
    const { container } = render(WhitespaceText({ text: 'Hello world', highlightTerm: 'xyz' }))
    expect(container.querySelector('mark')).toBeNull()
    expect(container.textContent).toBe('Hello world')
  })

  it('renders newlines as ¶ inside highlighted text', () => {
    const { container } = render(WhitespaceText({ text: 'a\nb', highlightTerm: 'a' }))
    const marks = container.querySelectorAll('mark')
    expect(marks.length).toBe(1)
    // The ¶ should still appear for the newline (outside the mark)
    expect(container.textContent).toContain('¶')
  })

  it('handles empty highlightTerm gracefully', () => {
    const { container } = render(WhitespaceText({ text: 'Hello', highlightTerm: '' }))
    expect(container.querySelector('mark')).toBeNull()
    expect(container.textContent).toBe('Hello')
  })

  it('handles adjacent matches without skipping (regression: lastIndex bug)', () => {
    // "WirWirWir" — 3 adjacent matches with no separator
    const { container } = render(WhitespaceText({ text: 'WirWirWir', highlightTerm: 'Wir' }))
    const marks = container.querySelectorAll('mark')
    expect(marks.length).toBe(3)
  })
})

// ---------- segmentRowEqual (memo comparator) ----------

// The REAL comparator, imported — not a copy. A local replica drifts from
// production silently and can invert an assertion without ever going red.
//
// `MinimalProps` keeps the fixtures readable: the comparator reads ~20 of
// SegmentRowProps' fields and ignores the rest, so the tests build only what it
// touches and cast at the call boundary.
interface MinimalProps {
  segment: { id: number }
  translation?: {
    id: number
    status: string
    active_text: string | null
    active_rich_text: string | null
    predict_outcome: string | null
    locked: boolean
    tm_match_percent?: number | null
    qe_note?: string | null
    edited_by?: number | null
  }
  isActive: boolean
  readOnly?: boolean
  showWhitespace?: boolean
  spellCheck?: boolean
  findHighlightTerm?: string | null
  tmDiffReference?: string | null
  qaIssues: { id: number; check_type?: string; severity?: string; message?: string; ignored?: boolean }[]
  isAdmin?: boolean
  // Callbacks are deliberately absent from the comparator; the fixture carries
  // them so a test can prove that.
  onActivate?: () => void
  onNavigate?: () => void
  refTerms?: unknown
  inFirstStep?: boolean
  isRepetition?: boolean
  isRepetitionSource?: boolean
  commentCount?: number
}

const segmentRowEqual = (prev: MinimalProps, next: MinimalProps): boolean =>
  realSegmentRowEqual(prev as never, next as never)

const baseProps: MinimalProps = {
  segment: { id: 1 },
  translation: {
    id: 10,
    status: 'pre_translated',
    active_text: 'Hello',
    active_rich_text: null,
    predict_outcome: null,
    locked: false,
  },
  isActive: false,
  readOnly: false,
  showWhitespace: false,
  spellCheck: true,
  qaIssues: [],
}

function withChanges(overrides: Partial<MinimalProps>): MinimalProps {
  return { ...baseProps, ...overrides }
}

function withTranslation(overrides: Partial<NonNullable<MinimalProps['translation']>>): MinimalProps {
  return { ...baseProps, translation: { ...baseProps.translation!, ...overrides } }
}

describe('segmentRowEqual (memo comparator)', () => {
  it('returns true for identical inactive props', () => {
    expect(segmentRowEqual(baseProps, { ...baseProps })).toBe(true)
  })

  it('returns false when row becomes active', () => {
    expect(segmentRowEqual(baseProps, withChanges({ isActive: true }))).toBe(false)
  })

  it('returns false when row was active', () => {
    const active = withChanges({ isActive: true })
    expect(segmentRowEqual(active, baseProps)).toBe(false)
  })

  it('returns false when translation status changes', () => {
    expect(segmentRowEqual(baseProps, withTranslation({ status: 'approved' }))).toBe(false)
  })

  it('returns false when active_text changes', () => {
    expect(segmentRowEqual(baseProps, withTranslation({ active_text: 'Hola' }))).toBe(false)
  })

  it('returns false when active_rich_text changes', () => {
    expect(segmentRowEqual(baseProps, withTranslation({ active_rich_text: 'Edited' }))).toBe(false)
  })

  it('returns false when predict_outcome changes', () => {
    expect(segmentRowEqual(baseProps, withTranslation({ predict_outcome: 'approved' }))).toBe(false)
  })

  it('returns false when locked changes', () => {
    expect(segmentRowEqual(baseProps, withTranslation({ locked: true }))).toBe(false)
  })

  it('returns false when readOnly changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ readOnly: true }))).toBe(false)
  })

  it('returns false when showWhitespace changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ showWhitespace: true }))).toBe(false)
  })

  it('returns false when spellCheck changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ spellCheck: false }))).toBe(false)
  })

  it('returns false when findHighlightTerm changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ findHighlightTerm: 'Wir' }))).toBe(false)
  })

  // A hover in the TM panel re-points the source cell's diff; the row must
  // re-render or the highlight would describe the previously hovered hit.
  it('returns false when tmDiffReference changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ tmDiffReference: 'Reset your password.' }))).toBe(false)
  })

  it('returns false when QA issues count changes', () => {
    expect(segmentRowEqual(baseProps, withChanges({ qaIssues: [{ id: 1 }] }))).toBe(false)
  })

  // When a user's admin status flips (e.g. role change
  // mid-session) the lock toggle button must re-render to swap between the
  // interactive button and the "🔒 Locked" indicator. Without isAdmin in
  // the comparator the row would stay stale until some unrelated prop
  // changed.
  it('returns false when isAdmin changes (lock toggle visibility)', () => {
    expect(segmentRowEqual(
      withChanges({ isAdmin: false }),
      withChanges({ isAdmin: true }),
    )).toBe(false)
  })

  it('returns true when isAdmin matches on both sides', () => {
    expect(segmentRowEqual(
      withChanges({ isAdmin: true }),
      withChanges({ isAdmin: true }),
    )).toBe(true)
  })

  // The dot's appearance depends on the finding's content
  // and its ignored flag, so equal COUNTS are not equal props.
  it('returns false when a QA issue is replaced by a different one', () => {
    const prev = withChanges({ qaIssues: [{ id: 1, check_type: 'tag_missing', severity: 'error', message: 'a' }] })
    const next = withChanges({ qaIssues: [{ id: 2, check_type: 'punctuation', severity: 'warning', message: 'b' }] })
    expect(segmentRowEqual(prev, next)).toBe(false)
  })

  it('returns false when only the ignored flag flips (dot must re-render)', () => {
    const issue = { id: 1, check_type: 'tag_missing', severity: 'error', message: 'a' }
    const prev = withChanges({ qaIssues: [{ ...issue, ignored: false }] })
    const next = withChanges({ qaIssues: [{ ...issue, ignored: true }] })
    expect(segmentRowEqual(prev, next)).toBe(false)
  })

  it('returns true when the QA issue list is unchanged', () => {
    const issue = { id: 1, check_type: 'tag_missing', severity: 'error', message: 'a' }
    expect(segmentRowEqual(withChanges({ qaIssues: [issue] }), withChanges({ qaIssues: [{ ...issue }] }))).toBe(true)
  })

  it('returns true when only callbacks change (not compared)', () => {
    // Parent re-renders hand the row fresh closures every time. The comparator
    // omits them on purpose — comparing them would make every prop set unequal
    // and defeat React.memo for the whole grid. Distinct identities, so this
    // does not collapse into the identical-props case above.
    expect(segmentRowEqual(
      withChanges({ onActivate: () => {}, onNavigate: () => {} }),
      withChanges({ onActivate: () => {}, onNavigate: () => {} }),
    )).toBe(true)
  })

  it('returns false when translation goes from undefined to defined', () => {
    const noTranslation = withChanges({ translation: undefined })
    expect(segmentRowEqual(noTranslation, baseProps)).toBe(false)
  })

  it('returns true when both have no translation', () => {
    const a = withChanges({ translation: undefined })
    const b = withChanges({ translation: undefined })
    expect(segmentRowEqual(a, b)).toBe(true)
  })
})
