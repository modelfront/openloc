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
// the dragged ratio, so they still follow the window width. #, Match and
// Status never get narrower than their defaults: those are the widths their
// header label and content need, and anything less runs into the next column.
//
// Status: as wide as the widest status chip the job shows (its chips stack,
// so the widest one decides), never under STATUS_MIN_WIDTH — 92px fits
// "Draft" and "Approved". Same cell padding as extras; a chip is its text +
// px-2 both sides (16px).
//
// Extras (the unnamed last column): as wide as the widest row in the task
// needs, capped at EXTRAS_MAX_WIDTH (items wrap to a second line beyond it),
// and 0 when no row has anything. The numbers mirror SegmentRow's classes —
// keep them in step:
//   cell          px-2                                   → 16px
//   item group    gap-1                                  → 4px between items
//   lock          🔒 in text-xs                          → text width
//   QE chip       default Chip: text-xs font-medium px-2 → text + 16px
//   QA dot        w-2                                    → 8px
//   comment chip  default Chip, px-1 gap-0.5, 12px icon  → text + 8 + 2 + 12px

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
  /** Body text-xs. */
  text: MeasureText
}

export const EXTRAS_MAX_WIDTH = 104
const CELL_PADDING = 16
const ITEM_GAP = 4
const QE_CHIP_PADDING = 16
const QA_DOT = 8
const COMMENT_CHIP_EXTRA = 8 + 2 + 12
/** Slack for sub-pixel rounding, so a row that only just fits stays on one line. */
const SLACK = 1

/** Content width of one row's extras on a single line; 0 when it has none. */
export function extrasContentWidth(e: SegmentExtras, w: ExtrasTextWidths): number {
  const items: number[] = []
  if (e.locked) items.push(w.text('🔒'))
  if (e.qeLabel) items.push(w.chip(e.qeLabel) + QE_CHIP_PADDING)
  if (e.hasQA) items.push(QA_DOT)
  if (e.commentCount > 0) items.push(w.chip(String(e.commentCount)) + COMMENT_CHIP_EXTRA)
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
  canvasWidths ??= { chip: canvasTextMeasure(500, 12, 7), text: canvasTextMeasure(400, 12, 7) }
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
// Match: px-2 + its widest chip ("101%"), and the MATCH header label.
const MATCH_WIDTH = 64
// Source and Translation wrap their text, so they only need room for the
// TRANSLATION header label.
const TEXT_COLUMN_MIN = 120

const STATUS_MIN_WIDTH = 92
const STATUS_CHIP_PADDING = 16

/** Width of the Status column for the chip labels the job shows. */
export function statusColumnWidth(chipLabels: Iterable<string>, w: ExtrasTextWidths = canvasExtrasTextWidths()): number {
  let widest = 0
  for (const label of chipLabels) widest = Math.max(widest, w.chip(label) + STATUS_CHIP_PADDING)
  return Math.max(STATUS_MIN_WIDTH, Math.ceil(widest + CELL_PADDING + SLACK))
}

/** Narrowest each column may be dragged to, in column order. */
export function segmentColumnMins(statusWidth: number): number[] {
  return [INDEX_WIDTH, TEXT_COLUMN_MIN, TEXT_COLUMN_MIN, MATCH_WIDTH, statusWidth, 0]
}

type ColumnWidths = { statusWidth: number; extrasWidth: number; widths: number[] | null }

/** The grid template: the dragged widths when there are any, else the defaults. */
export function segmentGridTemplate({ statusWidth, extrasWidth, widths }: ColumnWidths): string {
  if (widths) {
    const [num, source, target, match, status, extras] = widths
    return `${num}px ${source}fr ${target}fr ${match}px ${status}px ${extras}px`
  }
  return `${INDEX_WIDTH}px 1fr 1fr ${MATCH_WIDTH}px ${statusWidth}px ${extrasWidth}px`
}

export function segmentColumnsStyle(opts: ColumnWidths): CSSProperties {
  return { '--seg-cols': segmentGridTemplate(opts) } as CSSProperties
}
