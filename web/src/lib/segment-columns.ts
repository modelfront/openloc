// The segment table's column widths, set as one CSS variable (`--seg-cols`)
// on the table container and read by SEGMENT_GRID_COLS in the header and every
// row. Columns: # | Source | Translation | Match | Status | extras.
//
// Defaults come from the job's data, worked out without the DOM: every row is
// its own grid (the list is virtualized), so the browser cannot size one
// column to the widest row.
//
// The user can drag the border between two columns (never saved: a reload
// restores the defaults). The table keeps its width, so a drag moves width
// from one neighbour to the other. Source and Translation stay `fr` tracks, in
// the dragged ratio, so they still follow the window width. # and Match never
// get narrower than their defaults: those are the widths their header label
// and content need, and anything less runs into the next column.
//
// Status: a fixed STATUS_WIDTH, just its one 20px icon, and no visible header
// label. Dragging either border beside it moves the whole column: Match slides
// along, and Translation and extras trade the width.
//
// Extras (the unnamed last column): as wide as the widest row in the task
// needs, capped at EXTRAS_MAX_WIDTH (items wrap to a second line beyond it),
// and 0 when no row has anything. The numbers mirror SegmentRow's classes —
// keep them in step:
//   cell          px-2                                   → 16px
//   item group    gap-1                                  → 4px between items
//   lock          LockIcon, size-6                       → LOCK_SLOT_WIDTH
//   QE chip       default Chip: text-xs font-medium px-2 → text + 16px
//   QA dot        w-2                                    → 8px
//   comment chip  default Chip, px-1 gap-0.5, 12px icon  → count + 8 + 2 + 12px;
//                 tabular-nums: every digit as wide as the widest

import type { CSSProperties } from 'react'
import { canvasTextMeasure, type MeasureText } from './text-width'

export interface SegmentExtras {
  locked: boolean
  qeLabel: string | null
  hasQA: boolean
  commentCount: number
}

export interface ExtrasTextWidths {
  /** Default Chip label: text-xs font-medium. */
  chip: MeasureText
}

export const EXTRAS_MAX_WIDTH = 104
/** The padlock's square in a locked row (`LockIcon`, an icon-xs button). */
export const LOCK_SLOT_WIDTH = 24
const CELL_PADDING = 16
const ITEM_GAP = 4
const QE_CHIP_PADDING = 16
const QA_DOT = 8
const COMMENT_CHIP_EXTRA = 8 + 2 + 12
/** Slack for sub-pixel rounding, so a row that only just fits stays on one line. */
const SLACK = 1

const DIGITS = '0123456789'.split('')

/** A tabular-nums digit's width in the chip font: the widest digit's. */
function widestDigit(w: ExtrasTextWidths): number {
  return Math.max(...DIGITS.map(d => w.chip(d)))
}

/** Content width of one row's extras on a single line; 0 when it has none. */
export function extrasContentWidth(e: SegmentExtras, w: ExtrasTextWidths): number {
  const items: number[] = []
  if (e.locked) items.push(LOCK_SLOT_WIDTH)
  if (e.qeLabel) items.push(w.chip(e.qeLabel) + QE_CHIP_PADDING)
  if (e.hasQA) items.push(QA_DOT)
  if (e.commentCount > 0) items.push(String(e.commentCount).length * widestDigit(w) + COMMENT_CHIP_EXTRA)
  if (items.length === 0) return 0
  return items.reduce((n, x) => n + x, 0) + ITEM_GAP * (items.length - 1)
}

export function extrasColumnWidth(rows: Iterable<SegmentExtras>, w: ExtrasTextWidths = canvasExtrasTextWidths()): number {
  const capContent = EXTRAS_MAX_WIDTH - CELL_PADDING - SLACK
  let widest = 0
  for (const r of rows) {
    widest = Math.max(widest, extrasContentWidth(r, w))
    if (widest >= capContent) break // the column can't get any wider
  }
  if (widest === 0) return 0
  return Math.min(EXTRAS_MAX_WIDTH, Math.ceil(widest + CELL_PADDING + SLACK))
}

// One pair for the page, so their caches survive between recomputations.
let canvasWidths: ExtrasTextWidths | undefined
function canvasExtrasTextWidths(): ExtrasTextWidths {
  canvasWidths ??= { chip: canvasTextMeasure(500, 12, 7) }
  return canvasWidths
}

/**
 * Move the border between column `i` and `i + 1` by `dx` px: one grows by
 * what the other gives up, and neither goes below its minimum.
 */
export function moveColumnBorder(widths: number[], i: number, dx: number, min: number[]): number[] {
  const d = Math.min(Math.max(dx, min[i] - widths[i]), widths[i + 1] - min[i + 1])
  const out = [...widths]
  out[i] += d
  out[i + 1] -= d
  return out
}

// #: px-2 + the 14px selection checkbox + gap-1.5 + a five-digit segment number.
const INDEX_WIDTH = 76
// Match: px-2 + its widest chip ("100%→", 61px), and the MATCH header label.
const MATCH_WIDTH = 78
// Source and Translation wrap their text, so they only need room for the
// TRANSLATION header label.
const TEXT_COLUMN_MIN = 120

// Status: px-2 + the 20px status icon.
const STATUS_WIDTH = 36
const TRANSLATION_COLUMN = 2
const STATUS_COLUMN = 4
const EXTRAS_COLUMN = 5

/**
 * Whether the border after column `i` can be dragged. Match | Status moves
 * Status to widen extras, so it has nothing to do while no row has extras.
 */
export function isResizableBorder(i: number, hasExtras: boolean): boolean {
  return hasExtras || i !== STATUS_COLUMN - 1
}

/**
 * Drag the border after column `i` by `dx` px. Either border beside Status
 * moves the whole column at its fixed width: Match slides along, and
 * Translation and extras trade the width. Every other border is
 * moveColumnBorder.
 */
export function dragSegmentBorder(widths: number[], i: number, dx: number, min: number[]): number[] {
  if (i !== STATUS_COLUMN - 1 && i !== STATUS_COLUMN) return moveColumnBorder(widths, i, dx, min)
  const t = TRANSLATION_COLUMN
  const x = EXTRAS_COLUMN
  const d = Math.min(Math.max(dx, min[t] - widths[t]), widths[x] - min[x])
  const out = [...widths]
  out[t] += d
  out[x] -= d
  return out
}

/** Narrowest each column may be dragged to, in column order. */
export function segmentColumnMins(extrasWidth: number): number[] {
  return [INDEX_WIDTH, TEXT_COLUMN_MIN, TEXT_COLUMN_MIN, MATCH_WIDTH, STATUS_WIDTH, extrasWidth]
}

/**
 * Overlay scrollbars take no room and widen to about 15px on hover, over the
 * row's last cell (the QA dot, or the status icon when extras is empty). With
 * them the grid ends in an empty track this wide; with the last cell's 8px
 * right padding, its icon sits about 16px from the edge, just clear of the
 * scrollbar.
 */
export const OVERLAY_SCROLLBAR_GUTTER = 8

type ColumnWidths = { extrasWidth: number; widths: number[] | null; overlayScrollbar?: boolean }

/**
 * The grid template: the dragged widths when there are any, else the defaults.
 * Extras follows the rows even after a drag, which measured it as it was then:
 * 0 while no row has any, and never narrower than they need.
 */
export function segmentGridTemplate({ extrasWidth, widths, overlayScrollbar }: ColumnWidths): string {
  const gutter = overlayScrollbar ? ` ${OVERLAY_SCROLLBAR_GUTTER}px` : ''
  if (widths) {
    const [num, source, target, match, , dragged] = widths
    const extras = extrasWidth === 0 ? 0 : Math.max(dragged, extrasWidth)
    return `${num}px ${source}fr ${target}fr ${match}px ${STATUS_WIDTH}px ${extras}px${gutter}`
  }
  return `${INDEX_WIDTH}px 1fr 1fr ${MATCH_WIDTH}px ${STATUS_WIDTH}px ${extrasWidth}px${gutter}`
}

export function segmentColumnsStyle(opts: ColumnWidths): CSSProperties {
  return { '--seg-cols': segmentGridTemplate(opts) } as CSSProperties
}
