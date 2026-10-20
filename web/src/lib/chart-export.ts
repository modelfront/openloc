// A chart as a spreadsheet.
//
// The table is built from `pivotChart` — the function SpecChart draws from —
// so the file carries exactly the series on screen: the split into bands, the
// top eight plus "Other", the column the unit control picked. A separate
// shaping pass would be a second opinion about what the chart shows, and the
// two would drift.
//
// Dates are real date cells rather than the axis labels, so the sheet sorts
// and filters as dates. They are parsed with `parseLocalDate`: `new Date()` on
// a bare YYYY-MM-DD is UTC midnight, which lands on the previous day for every
// reader west of Greenwich.
import writeXlsxFile, { type Cell, type Row } from 'write-excel-file/browser'
import type { ChartSpec } from '@/api'
import { pivotChart } from './chart-pivot'
import { grainOf, parseLocalDate, type TimeGrain } from './chart-axis-format'
import { metricLabel } from './metric-labels'

const AXIS_HEADER: Record<TimeGrain, string> = { day: 'Date', week: 'Week starting', month: 'Month' }

// Excel number formats: the tooltip's reading of each grain, with the year.
const DATE_FORMAT: Record<TimeGrain, string> = { day: 'd mmm yyyy', week: 'd mmm yyyy', month: 'mmm yyyy' }

export type TableValue = string | number | Date | null

export interface ChartTable {
  headers: string[]
  rows: TableValue[][]
  /** Set when the first column is a date. */
  grain: TimeGrain | null
}

/** The chart's rows as the chart draws them: the axis first, one column per series. */
export function chartTable(spec: ChartSpec): ChartTable {
  const { data, series } = pivotChart(spec)
  const grain = grainOf(spec.x_key)
  return {
    headers: [
      grain ? AXIS_HEADER[grain] : metricLabel(spec.x_key),
      // A split's series are category values ("DeepL"), already in the reader's
      // words. Otherwise they are column names.
      ...series.map(s => (spec.series_key ? s : metricLabel(s))),
    ],
    rows: data.map(row => [axisValue(row[spec.x_key], grain), ...series.map(s => numberOrEmpty(row[s]))]),
    grain,
  }
}

function axisValue(value: unknown, grain: TimeGrain | null): TableValue {
  if (grain) return parseLocalDate(value) ?? String(value ?? '')
  return value == null ? '' : String(value)
}

// Empty stays empty: an average over no events is "no value", and writing 0
// would claim a perfect score for a week nobody worked.
function numberOrEmpty(value: unknown): number | null {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  if (typeof value === 'string' && value.trim() !== '' && Number.isFinite(Number(value))) return Number(value)
  return null
}

export interface ExportContext {
  title: string
  /** The measure's definition — what the numbers count. */
  definition?: string
  /** Label → value, in the filter bar's words (`describeFilters`). */
  filters: [string, string][]
  exportedAt: Date
}

/**
 * Two sheets: the table, and what it is. The table is left clean — headers in
 * row 1, frozen — so it sorts and pivots without anyone deleting a preamble
 * first; the context goes on its own sheet, because a table of numbers with no
 * record of its filters is easy to misread later.
 */
export function chartWorkbook(table: ChartTable, context: ExportContext): Promise<Blob> {
  const header: Row = table.headers.map(h => ({ value: h, fontWeight: 'bold' }))
  const body: Row[] = table.rows.map(row => row.map((v, i) => cell(v, i === 0 ? table.grain : null)))

  const label = (text: string): Cell => ({ value: text, fontWeight: 'bold' })
  const about: Row[] = [
    [label('Chart'), context.title],
    ...(context.definition ? [[label('What it counts'), { value: context.definition, wrap: true }] as Row] : []),
    ...context.filters.map(([k, v]): Row => [label(k), v]),
    [label('Exported'), { value: context.exportedAt, type: Date, format: 'd mmm yyyy hh:mm' }],
  ]

  return writeXlsxFile([
    {
      sheet: 'Data',
      data: [header, ...body],
      stickyRowsCount: 1,
      columns: table.headers.map((_, i) => ({ width: i === 0 ? 20 : 14 })),
    },
    { sheet: 'About', data: about, columns: [{ width: 18 }, { width: 70 }] },
  ]).toBlob()
}

function cell(value: TableValue, grain: TimeGrain | null): Cell {
  if (value === null) return null
  if (value instanceof Date) return { value, type: Date, format: DATE_FORMAT[grain ?? 'day'] }
  if (typeof value === 'number') return { value, type: Number }
  return { value, type: String }
}

/**
 * "<title> <yyyy-mm-dd>.xlsx", with the characters no file system accepts
 * swapped out. Dated so two downloads of the same chart do not collide, and so
 * the file says when its numbers were true.
 */
export function exportFileName(title: string, at: Date): string {
  const day = `${at.getFullYear()}-${String(at.getMonth() + 1).padStart(2, '0')}-${String(at.getDate()).padStart(2, '0')}`
  const safe = title.replace(/[\\/:*?"<>|]+/g, '-').replace(/\s+/g, ' ').trim().slice(0, 100) || 'chart'
  return `${safe} ${day}.xlsx`
}
