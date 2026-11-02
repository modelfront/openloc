// Segment table columns: Status is a fixed 36px, its icon; the unnamed extras column is
// as wide as its widest row, capped, and takes no space when every row is
// empty; with overlay scrollbars the grid ends in an empty gutter track; a
// dragged border moves width between its neighbours, and never
// squeezes # or Match below the width their header and content need.
import { describe, it, expect } from 'vitest'
import { EXTRAS_MAX_WIDTH, LOCK_SLOT_WIDTH, OVERLAY_SCROLLBAR_GUTTER, extrasColumnWidth, dragSegmentBorder, extrasContentWidth, isResizableBorder, moveColumnBorder, segmentColumnMins, segmentColumnsStyle, segmentGridTemplate, type ExtrasTextWidths, type SegmentExtras } from './segment-columns'
import { SEGMENT_GRID_COLS } from '../components/SegmentRow'

// Round numbers keep the arithmetic readable: 5px per chip char.
const w: ExtrasTextWidths = { chip: t => t.length * 5 }
const none: SegmentExtras = { locked: false, qeLabel: null, hasQA: false, commentCount: 0 }

describe('extrasContentWidth', () => {
  it('is 0 for a row with nothing to show', () => {
    expect(extrasContentWidth(none, w)).toBe(0)
  })
  it('adds each item and a 4px gap between items', () => {
    expect(extrasContentWidth({ ...none, hasQA: true }, w)).toBe(8)
    expect(LOCK_SLOT_WIDTH).toBe(24)
    expect(extrasContentWidth({ ...none, locked: true, hasQA: true }, w)).toBe(24 + 4 + 8)
    // "QE 0.9" = 6 chars → 30 + 16 padding; "3" → 5 + 22 chip extras.
    expect(extrasContentWidth({ locked: true, qeLabel: 'QE 0.9', hasQA: true, commentCount: 3 }, w))
      .toBe(24 + 46 + 8 + 27 + 3 * 4)
  })
  it('measures the comment count in tabular digits, each as wide as the widest', () => {
    // As in the real font, "1" is narrower than the other digits; the chip
    // draws the count with tabular-nums, so "11" is two widest digits wide.
    const narrowOne: ExtrasTextWidths = { chip: t => [...t].reduce((n, c) => n + (c === '1' ? 5 : 8), 0) }
    expect(extrasContentWidth({ ...none, commentCount: 11 }, narrowOne)).toBe(2 * 8 + 22)
    expect(extrasContentWidth({ ...none, locked: true, commentCount: 12 }, narrowOne)).toBe(24 + 4 + 2 * 8 + 22)
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
    expect(extrasColumnWidth([none, dotOnly, lockAndDot], w)).toBe(36 + 16 + 1)
  })
  it('never grows past the cap — a crowded row wraps instead', () => {
    const crowded: SegmentExtras = { locked: true, qeLabel: 'QE 0.95', hasQA: true, commentCount: 128 }
    expect(extrasColumnWidth([crowded], w)).toBe(EXTRAS_MAX_WIDTH)
  })
})

describe('segmentGridTemplate', () => {
  it('defaults: Status is fixed, extras takes its computed width', () => {
    expect(segmentGridTemplate({ extrasWidth: 0, widths: null })).toBe('76px 1fr 1fr 78px 36px 0px')
    expect(segmentGridTemplate({ extrasWidth: 41, widths: null })).toBe('76px 1fr 1fr 78px 36px 41px')
  })
  it('after a drag: every column fixed except Source and Translation, which keep their dragged ratio', () => {
    expect(segmentGridTemplate({ extrasWidth: 25, widths: [60, 300, 200, 64, 36, 25] }))
      .toBe('60px 300fr 200fr 64px 36px 25px')
  })
  // A drag measures the extras column as it is then; the rows can change after.
  it('after a drag, extras still follows the rows: it opens for a new lock and closes when none is left', () => {
    expect(segmentGridTemplate({ extrasWidth: 41, widths: [60, 300, 200, 64, 36, 0] }))
      .toBe('60px 300fr 200fr 64px 36px 41px')
    expect(segmentGridTemplate({ extrasWidth: 0, widths: [60, 300, 200, 64, 36, 40] }))
      .toBe('60px 300fr 200fr 64px 36px 0px')
    // Dragged wider than its content, it keeps the dragged width.
    expect(segmentGridTemplate({ extrasWidth: 41, widths: [60, 300, 200, 64, 36, 60] }))
      .toBe('60px 300fr 200fr 64px 36px 60px')
  })
  // An overlay scrollbar takes no room and widens over the last cell on hover,
  // where the QA dot and the status icon sit.
  it('ends in an empty track with overlay scrollbars, so no icon sits under the scrollbar', () => {
    // The gutter plus the last cell's 8px right padding (px-2) must clear a
    // hovered overlay scrollbar, about 15px wide.
    expect(OVERLAY_SCROLLBAR_GUTTER + 8).toBeGreaterThan(15)
    expect(segmentGridTemplate({ extrasWidth: 25, widths: null, overlayScrollbar: true }))
      .toBe(`76px 1fr 1fr 78px 36px 25px ${OVERLAY_SCROLLBAR_GUTTER}px`)
    expect(segmentGridTemplate({ extrasWidth: 0, widths: [60, 300, 200, 64, 36, 0], overlayScrollbar: true }))
      .toBe(`60px 300fr 200fr 64px 36px 0px ${OVERLAY_SCROLLBAR_GUTTER}px`)
  })
  it('adds no track with classic scrollbars, which already take their own room', () => {
    expect(segmentGridTemplate({ extrasWidth: 25, widths: null, overlayScrollbar: false }))
      .toBe('76px 1fr 1fr 78px 36px 25px')
  })
  it('reaches the grid through one variable that the shared template reads', () => {
    expect(segmentColumnsStyle({ extrasWidth: 0, widths: null }))
      .toEqual({ '--seg-cols': '76px 1fr 1fr 78px 36px 0px' })
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
  // # and Match render at their defaults: the width the header label and the
  // widest content need. Narrower, the content runs into the next column.
  const mins = segmentColumnMins(40)
  it('Match stops at its default width', () => {
    const widths = [76, 300, 300, 78, 36, 40]
    expect(moveColumnBorder(widths, 2, 100, mins)).toEqual(widths)
  })
  it('# stops at the width a five-digit segment number needs', () => {
    const widths = [76, 300, 300, 78, 36, 40]
    expect(moveColumnBorder(widths, 0, -100, mins)).toEqual(widths)
  })
  it('every default is within its minimum, so a reset never renders a squeezed column', () => {
    for (const extrasWidth of [0, 41]) {
      const template = segmentGridTemplate({ extrasWidth, widths: null }).split(' ')
      template.forEach((track, i) => {
        if (track.endsWith('px')) expect(parseInt(track)).toBeGreaterThanOrEqual(segmentColumnMins(extrasWidth)[i])
      })
    }
  })
})

describe('Status moves at a fixed width', () => {
  const mins = segmentColumnMins(40)
  const widths = [76, 300, 300, 78, 36, 40]
  it('either border beside it moves the column: Translation and extras trade the width', () => {
    for (const border of [3, 4]) {
      expect(dragSegmentBorder(widths, border, -50, mins)).toEqual([76, 300, 250, 78, 36, 90])
    }
  })
  it('stops when extras is down to what its rows need, or Translation to its minimum', () => {
    expect(dragSegmentBorder(widths, 4, 100, mins)).toEqual(widths)
    expect(dragSegmentBorder(widths, 4, -1000, mins)).toEqual([76, 300, 120, 78, 36, 220])
  })
  it('other borders move width between their own neighbours', () => {
    expect(dragSegmentBorder(widths, 1, 50, mins)).toEqual([76, 350, 250, 78, 36, 40])
  })
  it('Match | Status has nothing to do while no row has extras', () => {
    expect(isResizableBorder(3, false)).toBe(false)
    expect(isResizableBorder(3, true)).toBe(true)
    expect([0, 1, 2, 4].every(i => isResizableBorder(i, false))).toBe(true)
  })
  it('keeps its width in a dragged layout', () => {
    expect(segmentGridTemplate({ extrasWidth: 40, widths: [76, 300, 300, 78, 120, 40] }))
      .toBe('76px 300fr 300fr 78px 36px 40px')
  })
})
