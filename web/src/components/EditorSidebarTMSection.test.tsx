// The TM matches section of the Resources sidebar: the fuzzy-diff highlight on
// each hit's source line, and the hover that re-targets the segment source's
// own diff. Rendering is exercised against the real SegmentText, because the
// highlight is produced there — a stub would make these tests vacuous.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EditorSidebarTMSection } from './EditorSidebarTMSection'
import type { TMMatch } from '../api'
import { TM_DIFF_TINT } from '../lib/tm-diff'
import { markTexts } from '../test/text-marks'

const SEGMENT_SOURCE = 'Reset your password to continue.'

const match = (over: Partial<TMMatch> = {}): TMMatch => ({
  score: 0.88,
  percent: 88,
  source_text: 'Reset your admin password to continue.',
  target_text: 'Setzen Sie Ihr Admin-Passwort zurück, um fortzufahren.',
  source_rich_text: null,
  target_rich_text: null,
  tm_name: 'Demo TM',
  tm_id: 1,
  ...over,
})

function renderSection(over: {
  matches?: TMMatch[]
  stale?: boolean
  sourceText?: string
  onMatchHover?: (s: string | null) => void
} = {}) {
  return render(
    <EditorSidebarTMSection
      matches={over.matches ?? [match()]}
      fetching={false}
      stale={over.stale ?? false}
      sourceText={over.sourceText ?? SEGMENT_SOURCE}
      onInsertMatch={vi.fn()}
      onMatchHover={over.onMatchHover}
      modKey="⌘"
    />,
  )
}

const highlights = (c: HTMLElement) => markTexts(c, `span.${TM_DIFF_TINT}`)

describe('EditorSidebarTMSection fuzzy-diff highlight', () => {
  it('highlights what the hit says that the segment does not', () => {
    const { container } = renderSection()
    expect(highlights(container)).toEqual(['admin '])
    // Whole hit still rendered — the painter must not eat the untouched parts.
    expect(container.textContent).toContain('Reset your admin password to continue.')
  })

  it('leaves an exact hit unhighlighted', () => {
    const { container } = renderSection({ matches: [match({ score: 1, source_text: SEGMENT_SOURCE })] })
    expect(highlights(container)).toEqual([])
  })

  it('does not diff stale hits — they describe another segment', () => {
    const { container } = renderSection({ stale: true })
    expect(highlights(container)).toEqual([])
  })

  it('reports the pointed-at hit, and clears it on leave', () => {
    const onMatchHover = vi.fn()
    const second = match({ tm_id: 2, source_text: 'Reset the password to continue.' })
    renderSection({ matches: [match(), second], onMatchHover })

    const cards = screen.getAllByTestId('tm-match-card')
    expect(cards.length).toBe(2)

    fireEvent.mouseEnter(cards[1])
    expect(onMatchHover).toHaveBeenCalledWith(second.source_text)

    fireEvent.mouseLeave(cards[1])
    expect(onMatchHover).toHaveBeenLastCalledWith(null)
  })

  it('does not report a stale hit — diffing against it would compare two segments', () => {
    const onMatchHover = vi.fn()
    renderSection({ stale: true, onMatchHover })
    const card = screen.getByTestId('tm-match-card')
    fireEvent.mouseEnter(card)
    // Not `not.toHaveBeenCalledWith(any(String))`: that also passes when the
    // handler fired with null. Assert nothing was reported at all.
    expect(onMatchHover).not.toHaveBeenCalled()
  })

  // A card is a real focusable control, so a keyboard user gets the same
  // retargeting a mouse user gets. As a plain <div> the focus handler was
  // unreachable and this test could not pass.
  it('is keyboard-focusable and reports the hit on focus', () => {
    const onMatchHover = vi.fn()
    renderSection({ onMatchHover })
    const card = screen.getByTestId('tm-match-card')

    expect(card.tagName).toBe('BUTTON')
    card.focus()
    expect(document.activeElement).toBe(card)
    expect(onMatchHover).toHaveBeenCalledWith(match().source_text)

    fireEvent.blur(card)
    expect(onMatchHover).toHaveBeenLastCalledWith(null)
  })

  it('still renders the percentage and TM name beside the diff', () => {
    renderSection()
    expect(screen.getByText('88%')).toBeTruthy()
    expect(screen.getByText('Demo TM')).toBeTruthy()
  })
})

// The card shows the server's percent, the number the Match column stores:
// an in-context exact match reads 101%, which the score alone cannot express.
describe('EditorSidebarTMSection percentage', () => {
  it('shows the in-context 101% from the server, not one derived from the score', () => {
    renderSection({ matches: [match({ score: 1, percent: 101, source_text: SEGMENT_SOURCE })] })
    expect(screen.getByText('101%')).toBeTruthy()
    expect(screen.queryByText('100%')).toBeNull()
  })
})
