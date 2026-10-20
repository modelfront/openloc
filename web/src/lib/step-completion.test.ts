import { describe, it, expect } from 'vitest'
import { unapprovedCounts, unapprovedSentence, blockedSegmentNumbers } from './step-completion'
import type { Translation } from '../api'

function tr(id: number, status: string, text: string | null): Translation {
  return { id, segment_id: id, target_language: 'de', status, active_text: text, tm_match_percent: null,
    predict_outcome: { predict_outcome: '', valid: false } } as Translation
}

describe('unapprovedCounts', () => {
  // Pretranslated (machine or earlier-step text, no chip) counts as a draft:
  // it has text and is not approved, so "Approve all" approves it too.
  it('counts every unapproved segment with text as a draft, the rest as untranslated', () => {
    const rows = [
      tr(1, 'needs_review', 'Hallo'),
      tr(2, 'needs_review', 'Welt'),
      tr(3, 'needs_review', null),
      tr(4, 'needs_review', ''),
      tr(5, 'approved', 'Ja'),
      tr(6, 'auto_approved', 'Nein'),
    ]
    expect(unapprovedCounts(rows)).toEqual({ drafts: 2, untranslated: 2 })
  })

  it('is zero when everything is approved or auto-approved', () => {
    expect(unapprovedCounts([tr(1, 'approved', 'a'), tr(2, 'auto_approved', 'b')])).toEqual({ drafts: 0, untranslated: 0 })
  })
})

describe('unapprovedSentence', () => {
  it('names both numbers', () => {
    expect(unapprovedSentence({ drafts: 3, untranslated: 2 })).toBe('There are 3 drafts and 2 untranslated segments.')
  })

  it('leaves out a part that is zero, and agrees with one', () => {
    expect(unapprovedSentence({ drafts: 1, untranslated: 0 })).toBe('There is 1 draft.')
    expect(unapprovedSentence({ drafts: 2, untranslated: 0 })).toBe('There are 2 drafts.')
    expect(unapprovedSentence({ drafts: 0, untranslated: 1 })).toBe('There is 1 untranslated segment.')
    expect(unapprovedSentence({ drafts: 0, untranslated: 3 })).toBe('There are 3 untranslated segments.')
  })

  it('agrees the verb with the first number', () => {
    expect(unapprovedSentence({ drafts: 1, untranslated: 2 })).toBe('There is 1 draft and 2 untranslated segments.')
  })
})

describe('blockedSegmentNumbers', () => {
  const position = new Map([[10, 7], [11, 2], [12, 4], [13, 0]])
  const positionOf = (id: number) => position.get(id)

  it('lists the 1-based numbers of rows skipped for a QA error or a conflict, in order', () => {
    const skipped = [
      { id: 10, reason: 'qa_error' as const },
      { id: 11, reason: 'conflict' as const },
      { id: 12, reason: 'empty' as const },
    ]
    expect(blockedSegmentNumbers(skipped, positionOf)).toEqual({ toFix: [3, 8], held: [] })
  })

  it('a row holding a tag its source lacks is one to fix', () => {
    expect(blockedSegmentNumbers([{ id: 11, reason: 'tag_not_in_source' }], positionOf)).toEqual({ toFix: [3], held: [] })
  })

  // Someone else has the draft open, so it stays unapproved: completing now
  // would hand it on as a draft.
  it('a row someone else is editing blocks completing too, listed on its own', () => {
    const skipped = [
      { id: 12, reason: 'held' as const },
      { id: 10, reason: 'qa_error' as const },
      { id: 13, reason: 'held' as const },
    ]
    expect(blockedSegmentNumbers(skipped, positionOf)).toEqual({ toFix: [8], held: [1, 5] })
    expect(blockedSegmentNumbers([{ id: 12, reason: 'held' }], positionOf)).toEqual({ toFix: [], held: [5] })
  })

  it('is null when every skipped row is one Approve all never approves', () => {
    const skipped = [
      { id: 12, reason: 'empty' as const },
      { id: 11, reason: 'unchanged' as const },
      { id: 10, reason: 'auto_approved' as const },
    ]
    expect(blockedSegmentNumbers(skipped, positionOf)).toBeNull()
  })
})
