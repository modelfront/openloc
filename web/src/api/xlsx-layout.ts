import { request } from './request'
import type { FileRecord } from './projects'

/**
 * The column layout of an Excel workbook without language codes in row 1:
 * the sheets to translate and, per sheet, the columns that hold the text.
 * Translations go back into their own cells. Mirrors
 * `internal/parser/xlsx_layout.go`.
 */
export interface XLSXSheetLayout {
  name: string
  /** Row 1 holds column titles, not text to translate. Absent means false. */
  header_row?: boolean
  /** Letters of the columns to translate; absent or empty for every text cell of the sheet. */
  columns?: string[]
}

export interface XLSXLayout {
  sheets: XLSXSheetLayout[]
}

/** A column that holds text, with its text in row 1. */
export interface XLSXColumnInfo {
  letter: string
  title?: string
}

/** A visible sheet and the columns that hold text, left to right. */
export interface XLSXSheetInfo {
  name: string
  columns: XLSXColumnInfo[]
}

export interface XLSXInspection {
  /** Row 1 holds language codes: the workbook needs no layout. */
  bilingual: boolean
  sheets: XLSXSheetInfo[]
  /** Absent for a bilingual workbook or one with no text to translate. */
  suggested_layout?: XLSXLayout
}

/** Reads an .xlsx without storing it. */
export const inspectXLSX = (projectId: number, file: File) => {
  const form = new FormData()
  form.append('file', file)
  return request<{ data: XLSXInspection }>(`/projects/${projectId}/files/inspect`, { method: 'POST', body: form })
    .then(r => r.data)
}

export interface StoredXLSXLayout {
  /** Null for a workbook read cell by cell or by its language codes. */
  layout: XLSXLayout | null
  inspection: XLSXInspection
}

export const getXLSXLayout = (projectId: number, fileId: number) =>
  request<{ data: StoredXLSXLayout }>(`/projects/${projectId}/files/${fileId}/xlsx-layout`).then(r => r.data)

/** Only while the project is a draft: the file is segmented again. */
export const setXLSXLayout = (projectId: number, fileId: number, layout: XLSXLayout) =>
  request<{ data: FileRecord }>(`/projects/${projectId}/files/${fileId}/xlsx-layout`, {
    method: 'PUT',
    body: JSON.stringify(layout),
  }).then(r => r.data)
