// AnalysisBreakdown — the per-(target_language, file) tables shown in an
// opened row of ProjectAnalysesTab. Pure: takes the breakdown rows
// + a per-language totals row derived via sumBand.

import { memo, useMemo } from 'react'
import { ANALYSIS_BANDS, sumBand, type JobAnalysis } from '../api/analyses'
import { LanguagePair } from '@/components/ui/language-code'
import { ColumnResizeHandle } from '@/components/ui/column-resize-handle'
import { EditableTitle } from '@/components/list/EditableTitle'
import { usePanelResize } from '../hooks/usePanelResize'

export type AnalysisMetric = 'words' | 'segments' | 'characters'

interface Props {
  rows: JobAnalysis[]
  metric: AnalysisMetric
  /** The analysis's source language, for each table's "EN → DE" heading. */
  sourceLanguage: string
}

// metricKey maps the UI-level metric to the corresponding columns on a
// job_analysis row. Centralised so adding a new metric (e.g.
// 'weighted_words') is a one-line change.
const metricKey: Record<AnalysisMetric, { total: 'segments' | 'words' | 'characters'; aa: string }> = {
  words:      { total: 'words',      aa: 'words_auto_approved' },
  segments:   { total: 'segments',   aa: 'segments_auto_approved' },
  characters: { total: 'characters', aa: 'characters_auto_approved' },
}

// The file column starts narrow enough that every table fits its frame; the
// user can drag it wider (one width for every language table, remembered in
// this browser), and only then does the table scroll sideways. Each number
// column keeps a floor so a wide file column pushes the table out instead of
// crushing the counts.
export const FILE_COLUMN_DEFAULT = 200
export const NUMBER_COLUMN_MIN = 84
/** Every band, plus Total. */
export const NUMBER_COLUMNS = ANALYSIS_BANDS.length + 1

export default function AnalysisBreakdown({ rows, metric, sourceLanguage }: Props) {
  const fileColumn = usePanelResize({
    storageKey: 'analysis-file-column-width',
    min: 120,
    max: 900,
    fallback: FILE_COLUMN_DEFAULT,
    label: 'Resize file column',
    edge: 'right',
  })

  // Grouped by target_language so each language gets its own table + totals
  // row. Memoized so a column resize leaves every table body untouched.
  const byLang = useMemo(() => rows.reduce<Record<string, JobAnalysis[]>>((acc, r) => {
    ;(acc[r.target_language] ??= []).push(r)
    return acc
  }, {}), [rows])

  if (rows.length === 0) {
    return (
      <p className="text-sm text-gray-500">
        No breakdown rows. The worker may still be running, or every job in the analysis had zero
        segments.
      </p>
    )
  }

  return (
    <div className="space-y-6">
      {Object.entries(byLang).map(([lang, langRows]) => (
        <div key={lang}>
          <h4 className="mb-2 text-gray-700 dark:text-gray-300">
            <LanguagePair source={sourceLanguage} targets={lang} />
          </h4>
          <div className="overflow-x-auto rounded-md border border-gray-200 bg-card dark:border-gray-800">
            <table
              className="w-full table-fixed text-xs"
              style={{ minWidth: fileColumn.width + NUMBER_COLUMNS * NUMBER_COLUMN_MIN }}
            >
              <colgroup>
                <col style={{ width: fileColumn.width }} />
                {Array.from({ length: NUMBER_COLUMNS }, (_, i) => <col key={i} />)}
              </colgroup>
              <thead className="bg-muted text-left font-medium uppercase text-gray-500 dark:text-gray-400">
                <tr>
                  <th className="px-3 py-2 sticky left-0 z-10 bg-muted font-medium border-r border-gray-200 dark:border-gray-800">
                    File
                    <ColumnResizeHandle
                      variant="grip"
                      active={fileColumn.isResizing}
                      title="Drag to resize, double-click to reset"
                      {...fileColumn.separatorProps}
                    />
                  </th>
                  {ANALYSIS_BANDS.map(b => (
                    <th key={b.id} className="px-2 py-2 text-right font-medium">{b.label}</th>
                  ))}
                  <th className="px-2 py-2 text-right font-medium border-l border-gray-200 dark:border-gray-800">Total</th>
                </tr>
              </thead>
              <LanguageBody lang={lang} rows={langRows} metric={metric} />
            </table>
          </div>
        </div>
      ))}
    </div>
  )
}

// One language's rows and totals. It never sees the file column's width, so
// dragging the column re-renders the column widths, not thousands of cells.
const LanguageBody = memo(function LanguageBody({ lang, rows, metric }: {
  lang: string
  rows: JobAnalysis[]
  metric: AnalysisMetric
}) {
  return (
    <tbody>
      {rows.map(r => (
        <tr key={r.id} className="border-t border-gray-100 dark:border-gray-800">
          <td className="px-3 py-2 sticky left-0 bg-card text-gray-900 dark:text-gray-100 border-r border-gray-200 dark:border-gray-800">
            <span className="flex min-w-0 items-center gap-1">
              <EditableTitle name={r.job_name_snapshot} canEdit={false} />
              {r.job_id === null && <span className="shrink-0 text-gray-400">(deleted)</span>}
            </span>
          </td>
          {ANALYSIS_BANDS.map(b => {
            const row = r as unknown as Record<string, number>
            const total = row[`${b.id}_${metricKey[metric].total}`] ?? 0
            const aa = row[`${b.id}_${metricKey[metric].aa}`] ?? 0
            return (
              <td key={b.id} className="px-2 py-2 text-right tabular-nums">
                {total.toLocaleString()}
                {aa > 0 && (
                  <div className="text-[10px] text-gray-500">
                    {aa.toLocaleString()} auto-approved
                  </div>
                )}
              </td>
            )
          })}
          {/* Row total — sum of all bands (the file's total wordcount). */}
          {(() => {
            const row = r as unknown as Record<string, number>
            const rowTotal = ANALYSIS_BANDS.reduce((acc, b) => acc + (row[`${b.id}_${metricKey[metric].total}`] ?? 0), 0)
            const rowAA = ANALYSIS_BANDS.reduce((acc, b) => acc + (row[`${b.id}_${metricKey[metric].aa}`] ?? 0), 0)
            return (
              <td className="px-2 py-2 text-right tabular-nums font-semibold border-l border-gray-200 dark:border-gray-800">
                {rowTotal.toLocaleString()}
                {rowAA > 0 && (
                  <div className="text-[10px] font-normal text-gray-500">
                    {rowAA.toLocaleString()} auto-approved
                  </div>
                )}
              </td>
            )
          })()}
        </tr>
      ))}
      {/* Per-language totals row */}
      <tr className="border-t border-gray-200 dark:border-gray-800 font-semibold bg-muted">
        <td className="px-3 py-2 sticky left-0 bg-inherit border-r border-gray-200 dark:border-gray-800">Total</td>
        {ANALYSIS_BANDS.map(b => {
          const sum = sumBand(rows, b.id, lang)
          const total = sum[metricKey[metric].total]
          const aa = sum.auto_approved[metricKey[metric].total]
          return (
            <td key={b.id} className="px-2 py-2 text-right tabular-nums">
              {total.toLocaleString()}
              {aa > 0 && (
                <div className="text-[10px] font-normal text-gray-500">
                  {aa.toLocaleString()} auto-approved
                </div>
              )}
            </td>
          )
        })}
        {/* Grand total — sum of every band across all files. */}
        {(() => {
          let grandTotal = 0
          let grandAA = 0
          for (const b of ANALYSIS_BANDS) {
            const s = sumBand(rows, b.id, lang)
            grandTotal += s[metricKey[metric].total]
            grandAA += s.auto_approved[metricKey[metric].total]
          }
          return (
            <td className="px-2 py-2 text-right tabular-nums border-l border-gray-200 dark:border-gray-800">
              {grandTotal.toLocaleString()}
              {grandAA > 0 && (
                <div className="text-[10px] font-normal text-gray-500">
                  {grandAA.toLocaleString()} auto-approved
                </div>
              )}
            </td>
          )
        })()}
      </tr>
    </tbody>
  )
})
