import { describe, it, expect } from 'vitest'
import { OUTCOME_LABELS, STATUS_CHIP_COLORS, qeChipInfo, serializeEditor, termUnderlineClass } from './SegmentRow.shared'
import type { SegmentEditorHandle } from './SegmentEditor'
import type { TermLookupResult } from '../api'

describe('OUTCOME_LABELS', () => {
  it('has labels for all known outcomes', () => {
    expect(OUTCOME_LABELS.approved).toBe('QP approved')
    expect(OUTCOME_LABELS.ape_approved).toBe('APE approved')
    expect(OUTCOME_LABELS.ape_needs_review).toBe('APE (review)')
    expect(OUTCOME_LABELS.rejected).toBe('Rejected')
  })
})

// The server clears qe_note when it stops describing the text, so the chip
// shows the score it has and nothing else.
describe('qeChipInfo', () => {
  const ape = { predict_outcome: 'ape_approved', valid: true }

  it('returns null without a note', () => {
    expect(qeChipInfo(undefined)).toBeNull()
    expect(qeChipInfo({ qe_note: null })).toBeNull()
    expect(qeChipInfo({ qe_note: '' })).toBeNull()
  })

  it('states the outcome and the score', () => {
    const qe = qeChipInfo({ qe_note: '88', predict_outcome: ape })!
    expect(qe.label).toBe('QE 88')
    expect(qe.color).toBe('blue')
    expect(qe.title).toBe('AI quality check — APE approved: 88')
  })

  it('long free-text notes collapse to "QE" with the note in the tooltip', () => {
    const qe = qeChipInfo({ qe_note: 'terminology mismatch', predict_outcome: ape })!
    expect(qe.label).toBe('QE')
    expect(qe.title).toContain('terminology mismatch')
  })
})

describe('STATUS_CHIP_COLORS', () => {
  // Pins each DERIVED display status (deriveDisplayStatus) to a
  // Chip colour. A missing entry renders `<Chip color={undefined}>`, which
  // falls back to grey — the status column would silently lose its meaning
  // rather than break, so this map has to be checked explicitly.
  const EXPECTED = {
    untranslated: 'grey',
    pre_translated: 'yellow',
    auto_approved: 'green',
    needs_review: 'red',
    approved: 'blue',
  } as const

  for (const [status, color] of Object.entries(EXPECTED)) {
    it(`maps ${status} to ${color}`, () => {
      expect(STATUS_CHIP_COLORS[status]).toBe(color)
    })
  }

  // Approved and auto_approved are the two "done" states but must stay
  // visually distinct — a translator scanning the column needs to see which
  // rows a human confirmed.
  it('gives approved and auto_approved different colours', () => {
    expect(STATUS_CHIP_COLORS.approved).not.toBe(STATUS_CHIP_COLORS.auto_approved)
  })
})

// The live source-term underline turns green when the target
// carries the approved translation the right number of times, red when it's
// missing/undercounted, and neutral violet in read-only panes (no target).
describe('termUnderlineClass', () => {
  const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
    id: 1, concept_id: 1, source_term: 'table', target_term: 'Tabelle', mt_translation: '',
    status: 'approved', notes: '', domain: '', term_base_id: 1,
    term_base_name: 'tb', priority: 0, ...over,
  })

  it('is green when the approved term is satisfied in the target', () => {
    const cls = termUnderlineClass(term(), 'The table.', 'Die Tabelle.')
    expect(cls).toContain('decoration-green-500')
    expect(cls).not.toContain('decoration-red-500')
  })

  it('is red when the approved term is missing/undercounted', () => {
    const cls = termUnderlineClass(term(), 'The table and the table.', 'Nur eine Tabelle.')
    expect(cls).toContain('decoration-red-500')
  })

  it('is red when only a sub-word matches (Tabellerkennung)', () => {
    const cls = termUnderlineClass(term(), 'The table.', 'Die Tabellerkennung.')
    expect(cls).toContain('decoration-red-500')
  })

  it('stays neutral violet in read-only panes (no target supplied)', () => {
    const cls = termUnderlineClass(term(), 'The table.', undefined)
    expect(cls).toContain('decoration-violet-400')
  })

  // Forbidden terms: RED when the banned target term is present (a violation),
  // and NOT underlined at all when it's absent (nothing wrong, nothing to insert).
  it('is red for a forbidden term whose banned translation is present', () => {
    const cls = termUnderlineClass(term({ status: 'forbidden' }), 'The table.', 'Die Tabelle.')
    expect(cls).toContain('decoration-red-500')
  })

  it('is not underlined for a forbidden term whose banned translation is absent', () => {
    const cls = termUnderlineClass(term({ status: 'forbidden' }), 'The table.', 'Die Ansicht.')
    expect(cls).toBe('')
  })

  it('stays neutral violet for a pending term', () => {
    const cls = termUnderlineClass(term({ status: 'pending' }), 'The table.', 'Die Tabelle.')
    expect(cls).toContain('decoration-violet-400')
  })
})

// Formatting applied in the editor (Cmd+B) is HTML; a plain-text file never
// stores it.
describe('serializeEditor', () => {
  const bold = { getHTML: () => '<p><strong>Hallo</strong> Welt</p>', getRichText: () => null } as unknown as SegmentEditorHandle

  it('sends the HTML of a formatted editor', () => {
    expect(serializeEditor(bold, 'Hallo Welt').text).toBe('<strong>Hallo</strong> Welt')
  })

  it('sends the plain text for a plain-text file', () => {
    expect(serializeEditor(bold, 'Hallo Welt', true).text).toBe('Hallo Welt')
  })
})
