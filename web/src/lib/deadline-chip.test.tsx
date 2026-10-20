// Deadline chip — band classification + boundary
// conditions. The 24h boundary between `approaching` (yellow) and `soon`
// (blue) is load-bearing: it matches the deadline cron's 24h notification
// window. Visual + email signals must stay in lockstep.
//
// Anti-tautology: shifting the 24h boundary in computeDeadlinePill (e.g.
// to 12h or 48h) makes the boundary tests fail RED — the band classifier
// would put the same input into a different band.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Chip } from '@/components/ui/chip'
import { computeDeadlinePill, deadlineChipProps } from './deadline'

const NOW = new Date('2026-09-15T12:00:00Z')
const DAY_MS = 24 * 60 * 60 * 1000

describe('computeDeadlinePill', () => {
  describe('null / invalid', () => {
    it('returns "No due date" + band=none for null', () => {
      const r = computeDeadlinePill(null, NOW)
      expect(r.band).toBe('none')
      expect(r.text).toBe('No due date')
    })

    it('returns "No due date" + band=none for undefined', () => {
      const r = computeDeadlinePill(undefined, NOW)
      expect(r.band).toBe('none')
      expect(r.text).toBe('No due date')
    })

    // A date the browser cannot parse is a different case from no date at all:
    // the dash says "there is a deadline here and it is unreadable".
    it('returns "—" + band=none for un-parseable string', () => {
      const r = computeDeadlinePill('not a date', NOW)
      expect(r.band).toBe('none')
      expect(r.text).toBe('—')
      expect(r.title).toBe('Invalid deadline')
    })
  })

  describe('overdue (deadline < now)', () => {
    it('classifies past deadlines as overdue', () => {
      const past = new Date(NOW.getTime() - 3 * 24 * 60 * 60 * 1000).toISOString()
      const r = computeDeadlinePill(past, NOW)
      expect(r.band).toBe('overdue')
      expect(r.text).toMatch(/^Overdue/)
      expect(r.color).toBe('red')
    })

    it('renders minutes/hours/days based on magnitude', () => {
      const min30 = new Date(NOW.getTime() - 30 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(min30, NOW).text).toBe('Overdue 30m')

      const hr5 = new Date(NOW.getTime() - 5 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(hr5, NOW).text).toBe('Overdue 5h')

      const day3 = new Date(NOW.getTime() - 3 * 24 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(day3, NOW).text).toBe('Overdue 3d')
    })
  })

  describe('approaching (0 < diff ≤ 24h) — matches cron window', () => {
    it('classifies 5h-out as approaching with yellow', () => {
      const future = new Date(NOW.getTime() + 5 * 60 * 60 * 1000).toISOString()
      const r = computeDeadlinePill(future, NOW)
      expect(r.band).toBe('approaching')
      expect(r.text).toBe('Due in 5h')
      expect(r.color).toBe('yellow')
    })

    it('renders "Due now" when within 60s', () => {
      const verySoon = new Date(NOW.getTime() + 30 * 1000).toISOString()
      expect(computeDeadlinePill(verySoon, NOW).text).toBe('Due now')
    })

    it('boundary: exactly 24h out is still approaching (≤ 24h)', () => {
      const exact = new Date(NOW.getTime() + 24 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(exact, NOW).band).toBe('approaching')
    })

    it('boundary: 24h shows "Due in 24h" (NOT "Due in 1d") to avoid the colour+text double-jump at the band switch', () => {
      // Without the cap, this would render "Due in 1d" yellow, then 1 minute
      // later flip to "Due in 2d" neutral — a confusing double-jump.
      const exact = new Date(NOW.getTime() + 24 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(exact, NOW).text).toBe('Due in 24h')
    })

    it('approaching always uses hour precision (never "Nd")', () => {
      const h23 = new Date(NOW.getTime() + 23 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(h23, NOW).text).toBe('Due in 23h')
    })

    it('boundary: 24h + 1 minute out shifts to soon', () => {
      const justOver = new Date(NOW.getTime() + 24 * 60 * 60 * 1000 + 60 * 1000).toISOString()
      expect(computeDeadlinePill(justOver, NOW).band).toBe('soon')
    })
  })

  describe('soon (1–7 days)', () => {
    it('classifies 3 days out as soon', () => {
      const d3 = new Date(NOW.getTime() + 3 * 24 * 60 * 60 * 1000).toISOString()
      const r = computeDeadlinePill(d3, NOW)
      expect(r.band).toBe('soon')
      expect(r.text).toBe('Due in 3d')
      expect(r.color).toBe('blue')
    })

    it('renders "Due in 2d" for just-over-24h (no calendar-tomorrow special case)', () => {
      // The approaching branch (≤ 24h) covers hour-precision urgency;
      // soon branch always uses day precision. Math.ceil rounds 25h up
      // to 2 days — the user is told "give yourself 2 days of headroom"
      // rather than the misleading "1 day" (less than 24h would imply).
      const justOver = new Date(NOW.getTime() + 25 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(justOver, NOW).text).toBe('Due in 2d')
    })

    it('boundary: exactly 7 days out is still soon', () => {
      const d7 = new Date(NOW.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString()
      expect(computeDeadlinePill(d7, NOW).band).toBe('soon')
    })

    it('boundary: 7 days + 1 second shifts to far', () => {
      const justOver = new Date(NOW.getTime() + 7 * 24 * 60 * 60 * 1000 + 1000).toISOString()
      expect(computeDeadlinePill(justOver, NOW).band).toBe('far')
    })
  })

  describe('far future (> 7 days)', () => {
    it('renders exact date for > 7 days out', () => {
      const d30 = new Date(NOW.getTime() + 30 * 24 * 60 * 60 * 1000).toISOString()
      const r = computeDeadlinePill(d30, NOW)
      expect(r.band).toBe('far')
      expect(r.text).toMatch(/^Due /)
      // Locale-dependent; year-included only when crossing year boundary
    })
  })
})

describe('deadlineChipProps (rendered through Chip)', () => {
  it('carries the band through as data-band for inspection', () => {
    const past = new Date(NOW.getTime() - DAY_MS).toISOString()
    render(<Chip {...deadlineChipProps(past, { now: NOW })} />)
    const el = screen.getByTestId('deadline-pill')
    expect(el).toHaveAttribute('data-band', 'overdue')
    expect(el).toHaveAttribute('title')
  })

  it('renders "No due date" for a missing deadline', () => {
    render(<Chip {...deadlineChipProps(null, { now: NOW })} />)
    expect(screen.getByTestId('deadline-pill').textContent).toBe('No due date')
  })

  // The palette class is asserted (not just `data-band`) because band→colour
  // is the step the props helper adds on top of computeDeadlinePill: a colour
  // dropped on the way to Chip fails here, not in the classifier tests above.
  it.each([
    ['overdue', -DAY_MS, 'bg-rose-100'],
    ['approaching', 5 * 60 * 60 * 1000, 'bg-amber-100'],
    ['soon', 3 * DAY_MS, 'bg-blue-100'],
    ['far', 30 * DAY_MS, 'bg-gray-100'],
  ] as const)('renders the %s band with its colour', (band, offsetMs, bg) => {
    const iso = new Date(NOW.getTime() + offsetMs).toISOString()
    const { unmount } = render(<Chip {...deadlineChipProps(iso, { now: NOW })} />)
    const el = screen.getByTestId('deadline-pill')
    expect(el).toHaveAttribute('data-band', band)
    expect(el).toHaveAttribute('data-slot', 'chip')
    expect(el.className).toContain(bg)
    unmount()
  })
})

// Finished work is not late: a completed project or job keeps its due date,
// in grey, instead of counting days overdue.
describe('finished work', () => {
  it('a past deadline on finished work shows the date, not Overdue', () => {
    const past = new Date(NOW.getTime() - 10 * DAY_MS).toISOString()
    const r = computeDeadlinePill(past, NOW, true)
    expect(r.band).toBe('done')
    expect(r.color).toBe('grey')
    expect(r.text).toMatch(/^Due /)
    expect(r.text).not.toMatch(/Overdue/)
    expect(computeDeadlinePill(past, NOW).band).toBe('overdue')
  })

  it('a future deadline on finished work is neutral too', () => {
    const soon = new Date(NOW.getTime() + 2 * 60 * 60 * 1000).toISOString()
    expect(computeDeadlinePill(soon, NOW, true).color).toBe('grey')
    expect(deadlineChipProps(soon, { now: NOW, done: true })['data-band']).toBe('done')
  })
})

// The Tasks page's extended text. Pinned to UTC so the zone name and the
// clock time do not depend on the machine running the test.
describe('deadlineChipProps with exact', () => {
  const at = (offsetMs: number) => new Date(NOW.getTime() + offsetMs).toISOString()
  const text = (iso: string | null) =>
    deadlineChipProps(iso, { exact: true, now: NOW, timeZone: 'UTC' }).children

  it.each([
    ['overdue', -2 * 60 * 60 * 1000, /^Overdue 2h · Sep 15, 10:00\s?AM UTC$/],
    ['approaching', 5 * 60 * 60 * 1000, /^Due in 5h · Sep 15, 5:00\s?PM UTC$/],
    ['soon', 3 * DAY_MS, /^Due in 3d · Sep 18, 12:00\s?PM UTC$/],
  ] as const)('adds the exact date, time and zone to the %s band', (_band, offsetMs, want) => {
    expect(text(at(offsetMs))).toMatch(want)
  })

  // The far band's text is already a date; repeating it would read "Due Oct 15 · Oct 15, …".
  it('gives the far band the time once, not the date twice', () => {
    expect(text(at(30 * DAY_MS))).toMatch(/^Due Oct 15, 12:00\s?PM UTC$/)
  })

  it('names the year only when it is not the current one', () => {
    expect(text('2027-01-10T09:00:00Z')).toMatch(/^Due Jan 10, 2027, 9:00\s?AM UTC$/)
  })

  it('gives finished work the date and time once, not Overdue', () => {
    const iso = at(-2 * 60 * 60 * 1000)
    expect(deadlineChipProps(iso, { exact: true, now: NOW, timeZone: 'UTC', done: true }).children)
      .toMatch(/^Due Sep 15, 10:00\s?AM UTC$/)
  })

  it('leaves "No due date" alone', () => {
    expect(text(null)).toBe('No due date')
  })

  it('is off by default, so the compact chips elsewhere keep their short text', () => {
    expect(deadlineChipProps(at(3 * DAY_MS), { now: NOW }).children).toBe('Due in 3d')
  })
})
