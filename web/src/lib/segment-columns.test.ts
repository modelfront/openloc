// Segment table columns: Status fits its widest chip (min 92px); the unnamed
// extras column is as wide as its widest row, capped, and takes no space when
// every row is empty; a dragged border moves width between its neighbours, and
// never squeezes Match or Status below the width their header and chips need.
import { describe, it, expect } from 'vitest'
import { EXTRAS_MAX_WIDTH, extrasColumnWidth, extrasContentWidth, moveColumnBorder, segmentColumnMins, segmentColumnsStyle, segmentGridTemplate, statusColumnWidth, type ExtrasTextWidths, type SegmentExtras } from './segment-columns'
import { SEGMENT_GRID_COLS } from '../components/SegmentRow'

// Round numbers keep the arithmetic readable: 5px per chip char, 12px lock.
const w: ExtrasTextWidths = { chip: t => t.length * 5, text: () => 12 }
const none: SegmentExtras = { locked: false, qeLabel: null, hasQA: false, commentCount: 0 }

describe('extrasContentWidth', () => {
  it('is 0 for a row with nothing to show', () => {
    expect(extrasContentWidth(none, w)).toBe(0)
  })
  it('adds each item and a 4px gap between items', () => {
    expect(extrasContentWidth({ ...none, hasQA: true }, w)).toBe(8)
    expect(extrasContentWidth({ ...none, locked: true, hasQA: true }, w)).toBe(12 + 4 + 8)
    // "QE 0.9" = 6 chars → 30 + 16 padding; "3" → 5 + 22 chip extras.
    expect(extrasContentWidth({ locked: true, qeLabel: 'QE 0.9', hasQA: true, commentCount: 3 }, w))
      .toBe(12 + 46 + 8 + 27 + 3 * 4)
  })
})

describe('extrasColumnWidth', () => {
  it('takes no space when every row is empty', () => {
    expect(extrasColumnWidth([none, none], w)).toBe(0)
    expect(extrasColumnWidth([], w)).toBe(0)
  })
  it('fits the widest row, plus cell padding and 1px slack', () => {
    const dotOnly = { ...none, hasQA: true }
    const lockAndDot = { ...none, locked: true, hasQA: true }
    expect(extrasColumnWidth([none, dotOnly, lockAndDot], w)).toBe(24 + 16 + 1)
  })
  it('never grows past the cap — a crowded row wraps instead', () => {
    const crowded: SegmentExtras = { locked: true, qeLabel: 'QE 0.95', hasQA: true, commentCount: 128 }
    expect(extrasColumnWidth([crowded], w)).toBe(EXTRAS_MAX_WIDTH)
  })
})

describe('statusColumnWidth', () => {
  it('stays at the 92px minimum for short chips', () => {
    expect(statusColumnWidth([], w)).toBe(92)
    expect(statusColumnWidth(['Draft', 'Approved'], w)).toBe(92)
  })
  it('fits the widest chip the job shows — chips stack, so only the widest counts', () => {
    // "Confirmed by assistant" = 22 chars → 110 + 16 chip padding + 16 cell padding + 1 slack.
    expect(statusColumnWidth(['Approved', 'Confirmed by assistant'], w)).toBe(143)
    expect(statusColumnWidth(['Auto-approved'], w)).toBe(98)
  })
})

describe('segmentGridTemplate', () => {
  it('defaults: Status and extras take their computed widths', () => {
    expect(segmentGridTemplate({ statusWidth: 92, extrasWidth: 0, widths: null })).toBe('76px 1fr 1fr 64px 92px 0px')
    expect(segmentGridTemplate({ statusWidth: 143, extrasWidth: 41, widths: null })).toBe('76px 1fr 1fr 64px 143px 41px')
  })
  it('after a drag: every column fixed except Source and Translation, which keep their dragged ratio', () => {
    expect(segmentGridTemplate({ statusWidth: 92, extrasWidth: 0, widths: [60, 300, 200, 64, 92, 25] }))
      .toBe('60px 300fr 200fr 64px 92px 25px')
  })
  it('reaches the grid through one variable that the shared template reads', () => {
    expect(segmentColumnsStyle({ statusWidth: 92, extrasWidth: 0, widths: null }))
      .toEqual({ '--seg-cols': '76px 1fr 1fr 64px 92px 0px' })
    // Header and rows both use SEGMENT_GRID_COLS; a literal template there
    // would silently ignore what the container sets.
    expect(SEGMENT_GRID_COLS).toContain('grid-cols-(--seg-cols)')
  })
})

describe('moveColumnBorder', () => {
  const widths = [60, 300, 300, 64, 92, 40]
  const noMin = [0, 0, 0, 0, 0, 0]
  const sum = (w: number[]) => w.reduce((a, b) => a + b, 0)

  it('moves width from one neighbour to the other, leaving the total unchanged', () => {
    const out = moveColumnBorder(widths, 1, 50, noMin)
    expect(out).toEqual([60, 350, 250, 64, 92, 40])
    expect(sum(out)).toBe(sum(widths))
  })
  it('works in both directions and on any border', () => {
    expect(moveColumnBorder(widths, 3, -10, noMin)).toEqual([60, 300, 300, 54, 102, 40])
    expect(moveColumnBorder(widths, 4, 20, noMin)).toEqual([60, 300, 300, 64, 112, 20])
  })
  it('stops at the minimum of whichever neighbour is shrinking', () => {
    const min = [32, 120, 120, 40, 48, 0]
    // Translation can give up 180px before it hits 120.
    expect(moveColumnBorder(widths, 1, 1000, min)).toEqual([60, 480, 120, 64, 92, 40])
    // "#" can give up 28px before it hits 32.
    expect(moveColumnBorder(widths, 0, -1000, min)).toEqual([32, 328, 300, 64, 92, 40])
  })
})

describe('segmentColumnMins', () => {
  // #, Match and Status render at their defaults: the width the header label
  // and the widest content need. Narrower, the text runs into the next column.
  it('Match stops at its default width', () => {
    const widths = [76, 300, 300, 64, 92, 40]
    expect(moveColumnBorder(widths, 3, -100, segmentColumnMins(92))).toEqual(widths)
    // The border can still move the other way: Match grows, Status gives up
    // only what it has over its own minimum.
    expect(moveColumnBorder([76, 300, 300, 64, 120, 40], 3, 100, segmentColumnMins(92)))
      .toEqual([76, 300, 300, 92, 92, 40])
  })
  it('Status stops at the width the job\'s chips need, from either border', () => {
    const widths = [76, 300, 300, 64, 143, 40]
    expect(moveColumnBorder(widths, 3, 100, segmentColumnMins(143))).toEqual(widths)
    expect(moveColumnBorder(widths, 4, -100, segmentColumnMins(143))).toEqual(widths)
  })
  it('# stops at the width a five-digit segment number needs', () => {
    const widths = [76, 300, 300, 64, 92, 40]
    expect(moveColumnBorder(widths, 0, -100, segmentColumnMins(92))).toEqual(widths)
  })
  it('every default is within its minimum, so a reset never renders a squeezed column', () => {
    for (const statusWidth of [92, 143]) {
      const template = segmentGridTemplate({ statusWidth, extrasWidth: 0, widths: null }).split(' ')
      const mins = segmentColumnMins(statusWidth)
      template.forEach((track, i) => {
        if (track.endsWith('px')) expect(parseInt(track)).toBeGreaterThanOrEqual(mins[i])
      })
    }
  })
})
