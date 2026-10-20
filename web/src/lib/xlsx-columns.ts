// Helpers for choosing what to translate in an Excel workbook without
// language codes in row 1. The backend repeats the checks
// (internal/parser/xlsx_layout.go).

import { ApiError, type XLSXColumnInfo, type XLSXInspection, type XLSXLayout, type XLSXSheetInfo } from '@/api'

/**
 * Whether an inspection failed because the server refused the workbook (a 4xx:
 * unreadable, not an .xlsx). Its upload then reports the problem itself. Any
 * other failure is the network's or the server's and is worth another try.
 */
export function inspectRefused(err: unknown): boolean {
  return err instanceof ApiError && err.status >= 400 && err.status < 500
}

/** Whether a picked file is an Excel workbook the upload may ask about. */
export function isXLSXFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.xlsx')
}

/** "A" → 1, "AA" → 27; 0 for anything that is not a column. */
export function columnNumber(letter: string): number {
  if (!/^[A-Z]{1,3}$/.test(letter)) return 0
  let n = 0
  for (const c of letter) n = n * 26 + (c.charCodeAt(0) - 64)
  return n
}

/** Longest row 1 text shown after a column letter. */
const LABEL_TEXT_MAX = 30

/** "B (English)": the letter, then the column's text in row 1 when it has any. */
export function columnLabel({ letter, title }: XLSXColumnInfo): string {
  const text = (title ?? '').trim()
  if (!text) return letter
  return `${letter} (${text.length > LABEL_TEXT_MAX ? `${text.slice(0, LABEL_TEXT_MAX)}…` : text})`
}

/**
 * The columns a sheet offers, left to right: those that hold text, and any
 * already chosen. Empty columns are never offered, so a stray value far to
 * the right adds nothing.
 */
export function sheetColumns(sheet: XLSXSheetInfo | undefined, chosen: string[] = []): XLSXColumnInfo[] {
  const cols = [...(sheet?.columns ?? [])]
  const have = new Set(cols.map(c => c.letter))
  for (const letter of chosen) if (!have.has(letter)) cols.push({ letter })
  return cols.sort((a, b) => columnNumber(a.letter) - columnNumber(b.letter))
}

/** One sheet's choices. No columns means every text cell of the sheet. */
export interface SheetChoice {
  name: string
  header_row: boolean
  columns: string[]
  included: boolean
}

/**
 * The sheets on offer, in workbook order: those of `initial` (the stored
 * layout or the suggestion) included, any other suggested sheet left out.
 */
export function sheetChoices(inspection: XLSXInspection, initial: XLSXLayout): SheetChoice[] {
  const chosen = new Map(initial.sheets.map(s => [s.name, s]))
  const suggested = new Map((inspection.suggested_layout?.sheets ?? []).map(s => [s.name, s]))
  const out: SheetChoice[] = []
  for (const { name } of inspection.sheets) {
    const base = chosen.get(name) ?? suggested.get(name)
    if (!base) continue
    out.push({ name, header_row: base.header_row ?? false, columns: base.columns ?? [], included: chosen.has(name) })
  }
  return out
}

/** Column letters in sheet order. */
function sortColumns(columns: string[]): string[] {
  return [...columns].sort((a, b) => columnNumber(a) - columnNumber(b))
}

/** A collapsed sheet's one-line summary; `offered` is the sheet's column count. */
export function sheetSummary(c: SheetChoice, offered: number): string {
  if (!c.included) return 'Not translated'
  if (c.columns.length === 0) return 'Whole sheet'
  if (offered > 1 && c.columns.length >= offered) return 'All columns'
  return `${c.columns.length === 1 ? 'Column' : 'Columns'} ${sortColumns(c.columns).join(', ')}`
}

/** The first problem with the choices, as a sentence; null when there is none. */
export function layoutProblem(choices: SheetChoice[]): string | null {
  return choices.some(c => c.included) ? null : 'Choose at least one sheet to translate.'
}

/** The layout the choices make, columns in sheet order. */
export function choicesToLayout(choices: SheetChoice[]): XLSXLayout {
  return {
    sheets: choices.filter(c => c.included).map(({ name, header_row, columns }) =>
      columns.length > 0
        ? { name, header_row, columns: sortColumns(columns) }
        : { name, header_row }),
  }
}
