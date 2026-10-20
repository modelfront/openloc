import { useState, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { importTermCSV, importTermTBX, exportTermCSV, exportTermTBX } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { errorText } from '@/lib/error-text'

export type TermExportFormat = 'csv' | 'tbx'

export function useTerminologyImportExport(terminologyId: number, terminologyName: string) {
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null)
  // Once the bytes are all sent (100%), the request stays open while the server
  // parses + inserts — which can take minutes for a large TBX. Flip to an
  // "importing" phase so the banner stops claiming it's still uploading.
  const [importing, setImporting] = useState(false)
  const uploadAbortRef = useRef<(() => void) | null>(null)

  const handleImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    e.target.value = ''
    // Dispatch by file extension: .tbx / .xml → concept-native TBX importer (TBX
    // is XML and is commonly exported with a .xml extension; the backend validates
    // the root element and errors clearly if it isn't TBX). Everything else → CSV.
    // CSV bilingual rows import as concepts using the base's default language pair,
    // unless the CSV carries source_language/target_language columns (which the
    // backend honors per row) — no UI pair selection needed.
    const lower = file.name.toLowerCase()
    const isTBX = lower.endsWith('.tbx') || lower.endsWith('.xml')
    try {
      setUploadProgress(0)
      setImporting(false)
      const onProgress = (pct: number) => {
        setUploadProgress(pct)
        if (pct >= 100) setImporting(true) // bytes sent — server is now processing
      }
      const { promise, abort } = isTBX
        ? importTermTBX(terminologyId, file, onProgress)
        : importTermCSV(terminologyId, file, onProgress)
      uploadAbortRef.current = abort
      const result = await promise
      uploadAbortRef.current = null
      setUploadProgress(null)
      setImporting(false)
      // A 0-import result is a failure, not a success — every row was skipped
      // (e.g. duplicate/invalid rows). Don't dress it up as a green success.
      if (result.imported === 0) {
        toast.error(`No terms imported${result.skipped > 0 ? ` (${result.skipped} skipped)` : ''}`)
        return
      }
      toast.success(`Imported ${result.imported} terms${result.skipped > 0 ? ` (${result.skipped} skipped)` : ''}`)
      // Refresh the concept list + language chips + base list (counts) so the
      // newly imported concepts appear without a manual reload.
      queryClient.invalidateQueries({ queryKey: queryKeys.concepts.allByBase(terminologyId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.termBaseLanguages(terminologyId) })
      queryClient.invalidateQueries({ queryKey: queryKeys.terminology.all })
    } catch (err) {
      uploadAbortRef.current = null
      setUploadProgress(null)
      setImporting(false)
      const msg = errorText(err, 'Unknown error')
      if (msg !== 'Upload cancelled') toast.error(`Import failed: ${msg}`)
    }
  }

  // Export the whole base: source → all languages present. CSV uses the base's
  // source_language + a target_language column for every other language; TBX is
  // the multilingual concept export. No per-language options.
  const handleExport = async (format: TermExportFormat = 'csv') => {
    try {
      if (format === 'tbx') {
        await exportTermTBX(terminologyId, terminologyName)
        toast.success('TBX file downloaded')
      } else {
        await exportTermCSV(terminologyId, terminologyName, { targets: [] })
        toast.success('CSV downloaded (all languages)')
      }
    } catch (err) {
      toast.error(`Export failed: ${errorText(err, 'Unknown error')}`)
    }
  }

  const cancelUpload = () => {
    uploadAbortRef.current?.()
    toast.info('Upload cancelled')
  }

  return { fileInputRef, uploadProgress, importing, handleImport, handleExport, cancelUpload }
}
