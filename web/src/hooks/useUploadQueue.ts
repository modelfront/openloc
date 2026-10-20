import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { inspectRefused, isXLSXFile } from '../lib/xlsx-columns'
import { inspectXLSX, uploadFile, type UploadResult, type XLSXInspection, type XLSXLayout } from '../api'

/** How often a failed read of an .xlsx is tried again, and the pause between. */
const INSPECT_RETRIES = 2
const INSPECT_RETRY_DELAY_MS = 500

export interface UploadProgress {
  /** Files finished so far in this batch. */
  current: number
  total: number
  /** Byte percentage (0–100) of the file uploading right now. */
  pct: number
  fileName: string
}

/** An .xlsx without language codes in row 1, waiting for its columns. */
export interface LayoutQuestion {
  file: File
  inspection: XLSXInspection
  suggested: XLSXLayout
}

/** A layout; null to translate every text cell; 'skip' to not upload the file. */
export type LayoutAnswer = XLSXLayout | null | 'skip'

export interface UploadBatchOptions {
  /** Whether the batch asks for an automatic analysis. */
  analyze?: boolean
  /** Answers already given for some of the files: those are not asked. */
  layouts?: ReadonlyMap<File, LayoutAnswer>
}

/**
 * Uploads a batch of files to a project one at a time, reporting progress.
 * Success and failure toasts are the hook's; `onUploaded` is where the caller
 * refreshes whatever the new file made stale.
 *
 * An .xlsx without language codes in row 1 and without an answer in
 * `layouts` waits in `layoutQuestion` until the caller's Excel columns dialog
 * calls `answerLayout`.
 */
export function useUploadQueue(onUploaded?: (projectId: number, file: File, result: UploadResult) => void) {
  const [progress, setProgress] = useState<UploadProgress | null>(null)
  const queueRef = useRef<File[]>([])
  const optionsRef = useRef<UploadBatchOptions>({})
  const projectIdRef = useRef<number>(0)
  const [layoutQuestion, setLayoutQuestion] = useState<LayoutQuestion | null>(null)
  const answerRef = useRef<((answer: LayoutAnswer) => void) | null>(null)
  const answeredRef = useRef(new Map<File, LayoutAnswer>())
  const onUploadedRef = useRef(onUploaded)
  useEffect(() => {
    onUploadedRef.current = onUploaded
  }, [onUploaded])

  // A workbook the server refuses is left to the upload, which reports the
  // problem in its own toast. Any other failed read is tried again, then the
  // file is skipped: it never uploads without the uploader's columns.
  const askLayout = useCallback(async (projectId: number, file: File): Promise<LayoutAnswer> => {
    if (!isXLSXFile(file)) return null
    const given = answeredRef.current.get(file)
    if (given !== undefined) {
      answeredRef.current.delete(file)
      return given
    }
    let inspection: XLSXInspection | undefined
    for (let attempt = 0; inspection === undefined; attempt++) {
      try {
        inspection = await inspectXLSX(projectId, file)
      } catch (err) {
        if (inspectRefused(err)) return null
        if (attempt >= INSPECT_RETRIES) {
          toast.error(`Could not read ${file.name}. Add it again to upload it.`)
          return 'skip'
        }
        await new Promise(resolve => setTimeout(resolve, INSPECT_RETRY_DELAY_MS))
      }
    }
    const suggested = inspection.suggested_layout
    if (inspection.bilingual || !suggested) return null
    return new Promise<LayoutAnswer>(resolve => {
      answerRef.current = resolve
      setLayoutQuestion({ file, inspection, suggested })
    })
  }, [])

  const answerLayout = useCallback((answer: LayoutAnswer) => {
    const resolve = answerRef.current
    answerRef.current = null
    setLayoutQuestion(null)
    resolve?.(answer)
  }, [])

  // One loop per batch: it ends when the queue runs dry. `start` only kicks
  // it off when no loop is running, so files never upload in parallel.
  const drain = useCallback(async () => {
    for (;;) {
      const file = queueRef.current.shift()
      if (!file) {
        setProgress(null)
        return
      }
      const projectId = projectIdRef.current
      setProgress(prev => prev ? { ...prev, current: prev.current + 1, pct: 0, fileName: file.name } : null)
      try {
        const answer = await askLayout(projectId, file)
        if (answer === 'skip') continue
        const result = await uploadFile(projectId, file, pct => {
          setProgress(prev => prev ? { ...prev, pct } : null)
        }, { analyze: optionsRef.current.analyze, xlsxLayout: answer ?? undefined })
        onUploadedRef.current?.(projectId, file, result)
        toast.success(`Uploaded ${file.name}`)
        for (const w of result.warnings ?? []) {
          toast.warning(w, { duration: 8000 })
        }
      } catch (err) {
        toast.error(`Failed to upload ${file.name}: ${(err as Error).message}`)
      }
    }
  }, [askLayout])

  /** Queue a batch. A batch already running is extended, not replaced. */
  const start = useCallback((projectId: number, files: File[], options: UploadBatchOptions = {}) => {
    if (files.length === 0) return
    const running = queueRef.current.length > 0 || progress !== null
    projectIdRef.current = projectId
    optionsRef.current = options
    options.layouts?.forEach((answer, file) => answeredRef.current.set(file, answer))
    queueRef.current.push(...files)
    if (running) {
      setProgress(prev => prev ? { ...prev, total: prev.total + files.length } : null)
      return
    }
    setProgress({ current: 0, total: files.length, pct: 0, fileName: '' })
    void drain()
  }, [drain, progress])

  return { progress, uploading: progress !== null, start, layoutQuestion, answerLayout }
}
