// The TM matches section of the Resources sidebar: the fuzzy-diff highlight on
// each hit's source line, and the hover that re-targets the segment source's
// own diff. Rendering is exercised against the real SegmentText, because the
// highlight is produced there — a stub would make these tests vacuous.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EditorSidebarTMSection } from './EditorSidebarTMSection'
import type { TMMatch } from '../api'
import { TM_DIFF_TINT } from '../lib/tm-diff'
import { markTexts } from '../test/text-marks'
import { formatDateTime } from '../utils'
import { tmStampText } from '../lib/tm-entry-stamps'

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
  created_at: '2026-09-03T09:10:00Z',
  updated_at: '2026-10-12T14:32:00Z',
  created_by: 'Daniel Weber <daniel@test.com>',
  updated_by: 'Anna Miller <anna@test.com>',
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

// A docked hit can read 99% with nothing highlighted: the 1% came from the
// settings, not the text. The card strikes out the text match beside the
// percent and lists each deduction in a tooltip.
describe('EditorSidebarTMSection penalty breakdown', () => {
  const docked = () => match({
    score: 0.99,
    percent: 99,
    source_text: SEGMENT_SOURCE,
    penalties: [{ kind: 'source_variant', percent: 1, tm_language: 'en', project_language: 'en-us' }],
  })

  it('strikes out the text match before the percent of a docked hit', () => {
    renderSection({ matches: [docked()] })
    const before = screen.getByTestId('tm-match-text-percent')
    expect(before.tagName).toBe('S')
    expect(before.textContent).toBe('100%')
    expect(screen.getByTestId('tm-match-score').textContent).toBe('100%99%')
  })

  it('names no reason beside the percent — the tooltip has them', () => {
    renderSection({ matches: [docked()] })
    expect(screen.getByTestId('tm-match-score').textContent).not.toContain('Language variant')
  })

  it('shows only the percent on a hit nothing docked', () => {
    renderSection()
    expect(screen.getByText('88%')).toBeTruthy()
    expect(screen.queryByTestId('tm-match-text-percent')).toBeNull()
  })

  it('shows only the percent on a stale hit — it belongs to another segment', () => {
    renderSection({ matches: [docked()], stale: true })
    expect(screen.getByText('99%')).toBeTruthy()
    expect(screen.queryByTestId('tm-match-text-percent')).toBeNull()
  })

  it('lists each deduction when the score is hovered', async () => {
    renderSection({ matches: [docked()] })
    await userEvent.hover(screen.getByTestId('tm-match-score'))
    const tip = await screen.findByTestId('tm-match-penalty-breakdown')
    expect(tip.textContent).toContain('100% text match')
    expect(tip.textContent).toContain('−1%')
    expect(tip.textContent).toContain('Source language variant (TM: en, project: en-us)')
  })

  it('opens the breakdown for a keyboard user on focus, and closes it on blur', async () => {
    renderSection({ matches: [docked()] })
    const card = screen.getByTestId('tm-match-card')
    card.focus()
    expect((await screen.findByTestId('tm-match-penalty-breakdown')).textContent).toContain('100% text match')
    fireEvent.blur(card)
    await waitFor(() => expect(screen.queryByTestId('tm-match-penalty-breakdown')).toBeNull())
  })

  it('describes the breakdown to assistive technology', () => {
    renderSection({ matches: [docked()] })
    const card = screen.getByTestId('tm-match-card')
    const description = document.getElementById(card.getAttribute('aria-describedby') ?? '')
    expect(description?.textContent).toBe(
      `100% text match; −1% Source language variant (TM: en, project: en-us); Created ${tmStampText(docked().created_at, docked().created_by)}`,
    )
  })
})

// The card says when its entry last changed and by whom; the tooltip adds when
// it was created, so a translator can judge how fresh and whose a match is.
describe('EditorSidebarTMSection entry stamps', () => {
  it('shows when the entry last changed, and by whom', () => {
    renderSection()
    const stamp = screen.getByTestId('tm-match-stamp').textContent
    expect(stamp).toBe(`${formatDateTime(match().updated_at)} · Anna Miller <anna@test.com>`)
    expect(stamp).not.toContain('Daniel Weber')
  })

  it('shows the date alone when no user was recorded', () => {
    renderSection({ matches: [match({ updated_by: '' })] })
    expect(screen.getByTestId('tm-match-stamp').textContent).toBe(formatDateTime(match().updated_at))
  })

  it('lists the creation and the last change when the stamp is hovered', async () => {
    renderSection()
    await userEvent.hover(screen.getByTestId('tm-match-stamp'))
    const tip = await screen.findByTestId('tm-match-stamps')
    expect(tip.textContent).toContain(`Created ${formatDateTime(match().created_at)} · Daniel Weber <daniel@test.com>`)
    expect(tip.textContent).toContain(`Changed ${formatDateTime(match().updated_at)} · Anna Miller <anna@test.com>`)
  })

  it('opens the stamps for a keyboard user on focus, and closes them on blur', async () => {
    renderSection()
    const card = screen.getByTestId('tm-match-card')
    card.focus()
    expect((await screen.findByTestId('tm-match-stamps')).textContent).toContain('Created ')
    fireEvent.blur(card)
    await waitFor(() => expect(screen.queryByTestId('tm-match-stamps')).toBeNull())
  })

  it('describes the creation to assistive technology on an undocked hit too', () => {
    renderSection()
    const card = screen.getByTestId('tm-match-card')
    const description = document.getElementById(card.getAttribute('aria-describedby') ?? '')
    expect(description?.textContent).toBe(`Created ${tmStampText(match().created_at, match().created_by)}`)
  })
})
