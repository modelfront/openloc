// The Match column on repetition rows. A repetition source whose text came
// from TM keeps its match percentage, with an arrow for "confirming this
// copies it on"; other sources show →R, and later repetitions show ⇢R.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, screen } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import { renderSegmentRow, rowTranslation } from '../test/segment-row'

const fromTM = (percent: number) => rowTranslation({
  status: 'approved',
  origin: { translation_origin: 'tm', valid: true },
  tm_match_percent: percent,
})

afterEach(cleanup)

describe('SegmentRow — Match chip on repetitions', () => {
  it('shows the TM percentage with an arrow on a repetition source from TM', () => {
    renderSegmentRow({ translation: fromTM(98), isActive: false, isRepetitionSource: true })

    const chip = screen.getByTestId('repetition-source-badge')
    expect(chip.textContent).toBe('98%→')
    expect(chip.className).toContain('bg-amber-100')
    expect(chip.getAttribute('title')).toMatch(/fuzzy TM match/i)
    expect(chip.getAttribute('title')).toMatch(/source of repetitions/i)
    expect(screen.queryByText('→R')).toBeNull()
  })

  it('keeps the exact-match wording for a 100% source', () => {
    renderSegmentRow({ translation: fromTM(100), isActive: false, isRepetitionSource: true })

    const chip = screen.getByTestId('repetition-source-badge')
    expect(chip.textContent).toBe('100%→')
    expect(chip.className).toContain('bg-emerald-100')
    expect(chip.getAttribute('title')).toMatch(/exact \(100%\) TM match/i)
  })

  it('shows →R on a repetition source that came from MT', () => {
    renderSegmentRow({
      translation: rowTranslation({ origin: { translation_origin: 'mt', valid: true } }),
      isActive: false,
      isRepetitionSource: true,
    })

    expect(screen.getByTestId('repetition-source-badge').textContent).toBe('→R')
    expect(screen.queryByText('MT')).toBeNull()
  })

  it('shows ⇢R, not the TM percentage, on a later repetition', () => {
    // Copying a confirmed translation onto a repetition keeps the row's own
    // origin and percentage, which then describe text the row no longer has.
    renderSegmentRow({ translation: fromTM(98), isActive: false, isRepetition: true })

    expect(screen.getByTestId('repetition-badge').textContent).toBe('⇢R')
    expect(screen.queryByText(/98%/)).toBeNull()
  })
})
