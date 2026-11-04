import { useState } from 'react'
import { useQueries, useQueryClient } from '@tanstack/react-query'

import { inspectXLSX, type XLSXInspection } from '@/api'
import type { LayoutAnswer } from '@/hooks/useUploadQueue'
import { useOnChange } from '@/hooks/useOnChange'
import { queryKeys } from '@/lib/query-keys'
import { NO_SHEET, choicesToLayout, inspectRefused, isXLSXFile, layoutProblem, sheetChoices, type SheetChoice } from '@/lib/xlsx-columns'

export interface XLSXUploadEntry {
  file: File
  /** Stable per file of the batch, for keys and test ids. */
  key: string
  /** Still being read, or waiting for the workbook before it. */
  pending: boolean
  /** The read failed for a reason other than the workbook: worth another try. */
  failed: boolean
  error: unknown
  retry: () => void
  inspection: XLSXInspection | undefined
  /** Null when the workbook asks nothing: bilingual, no text, or refused. */
  choices: SheetChoice[] | null
  setChoices: (choices: SheetChoice[]) => void
}

/**
 * The Excel part of an upload dialog: reads each .xlsx of the batch, one at a
 * time, and holds what to translate in it, starting from the suggestion.
 * `answers` is what the upload queue takes as `layouts`. A workbook the
 * server refuses asks nothing: its upload reports the problem. Any other
 * failed read holds the upload until a retry succeeds. `filterProfileOf`
 * names the filter profile the uploader picked for a workbook, if any.
 */
export function useXLSXUploadChoices(
  projectId: number,
  files: File[],
  enabled: boolean,
  filterProfileOf: (file: File) => number | undefined = () => undefined,
) {
  const queryClient = useQueryClient()
  const xlsx = files.filter(isXLSXFile)
  const keys = xlsx.map((f, i) => `${i}-${f.name}-${f.size}-${f.lastModified}`)
  const profiles = xlsx.map(filterProfileOf)
  // A workbook is read again for each filter profile it is picked with: the
  // suggestion comes from that profile's Excel rules.
  const readKeys = keys.map((k, i) => (profiles[i] === undefined ? k : `${k}@${profiles[i]}`))
  const settled = (key: string) => {
    const status = queryClient.getQueryState(queryKeys.xlsxInspect.byFile(projectId, key))?.status
    return status === 'success' || status === 'error'
  }
  const queries = useQueries({
    queries: xlsx.map((file, i) => ({
      queryKey: queryKeys.xlsxInspect.byFile(projectId, readKeys[i]),
      queryFn: () => inspectXLSX(projectId, file, profiles[i]),
      // Each workbook goes up whole: one at a time keeps a batch of large
      // files from landing on the server together.
      enabled: enabled && readKeys.slice(0, i).every(settled),
      staleTime: Infinity,
      retry: false,
    })),
  })
  const [edits, setEdits] = useState<Record<string, SheetChoice[]>>({})
  // Each opening starts from the suggestions. Keyed on the files' names, not
  // the array: a closed dialog passes a fresh empty one on every render.
  useOnChange([keys.join('|'), enabled], () => setEdits({}))

  const entries: XLSXUploadEntry[] = xlsx.map((file, i) => {
    const key = keys[i]
    // Edits belong to one reading: a new profile starts from its suggestion.
    const readKey = readKeys[i]
    const query = queries[i]
    const inspection = query.data
    const suggested = inspection && !inspection.bilingual ? inspection.suggested_layout : undefined
    return {
      file,
      key,
      pending: enabled && query.status === 'pending',
      failed: query.status === 'error' && !inspectRefused(query.error),
      error: query.error,
      retry: () => { void query.refetch() },
      inspection,
      choices: suggested ? edits[readKey] ?? sheetChoices(inspection!, suggested) : null,
      setChoices: choices => setEdits(prev => ({ ...prev, [readKey]: choices })),
    }
  })

  const pending = entries.some(e => e.pending)
  const failed = entries.find(e => e.failed)
  const unchosen = entries
    .map(e => ({ name: e.file.name, problem: e.choices ? layoutProblem(e.choices) : null }))
    .find(e => e.problem !== null)
  /** A choice the uploader still has to make; a failed read shows in its own entry. */
  const problem = !unchosen ? null : unchosen.problem === NO_SHEET
    ? `Choose at least one sheet to translate in ${unchosen.name}.`
    : `${unchosen.name}: ${unchosen.problem}`
  /** Why the upload is held, or null when it may go. */
  const hold = pending
    ? 'Reading the Excel files…'
    : failed ? `Could not read ${failed.file.name}. Retry it, or cancel.` : problem
  const answers = () => new Map<File, LayoutAnswer>(entries.map(e => [e.file, e.choices ? choicesToLayout(e.choices) : null]))

  return { entries, problem, hold, answers }
}
