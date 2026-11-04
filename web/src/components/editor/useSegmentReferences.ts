import { useCallback, useMemo } from 'react'
import type { SegmentReference } from '@/components/EditorSidebar'
import { useEditorRefs } from '@/hooks/useEditorRows'
import { segmentRefKeys, type RefTerms } from '@/lib/ref-terms'
import { segmentNumber, type IndexLookup, type IndexRow } from '@/lib/editor-index'
import type { EditorId } from '@/lib/editor-scope'

interface Options {
  jid: EditorId
  enabled: boolean
  rows: IndexRow[]
  indexLookup: IndexLookup
  /** The open segment's tagged source. */
  activeSourceRich: string | undefined
  /** The open segment's file: its chips point at keys in that file. */
  activeFileId: number | undefined
  scrollToTranslation: (translationId: number) => Promise<void>
}

/**
 * Cross-reference terms, from the server's list of referenced segments: the
 * terms every row's chips render, the open segment's references for the
 * sidebar, and the jump to the segment a reference points at.
 */
export function useSegmentReferences({ jid, enabled, rows, indexLookup, activeSourceRich, activeFileId, scrollToTranslation }: Options) {
  // Each map keeps its identity until a referenced target changes (a save
  // patches the cached list): SegmentRow's memo compares it by reference, and
  // a map that churned per keystroke would re-render every visible row. One
  // map per file: two files may use the same key.
  const { data: refs } = useEditorRefs(jid, enabled)
  const refTermsByFile = useMemo(() => {
    const out = new Map<number, RefTerms>()
    for (const r of refs ?? []) {
      let terms = out.get(r.file_id)
      if (!terms) out.set(r.file_id, terms = new Map())
      terms.set(r.key, { source: r.source, target: r.target })
    }
    return out
  }, [refs])
  const refTerms: RefTerms = (activeFileId !== undefined ? refTermsByFile.get(activeFileId) : undefined) ?? NO_TERMS
  // Where each referenced segment of the open row's file sits in the index, by key.
  const refRowByKey = useMemo(() => {
    const out = new Map<string, number>()
    for (const r of refs ?? []) {
      if (r.file_id !== activeFileId) continue
      const idx = indexLookup.bySegment.get(r.segment_id)
      if (idx !== undefined) out.set(r.key, idx)
    }
    return out
  }, [refs, indexLookup, activeFileId])

  // Jump to the segment a cross-reference points at, via the same helper F&R
  // uses. Called from the sidebar's References section — the chips themselves
  // are deliberately not clickable (a jump on plain click teleported linguists
  // away when they merely meant to activate the row).
  const jumpToReference = useCallback((refKey: string) => {
    const idx = refRowByKey.get(refKey)
    const translationId = idx !== undefined ? rows[idx].translationId : null
    if (translationId != null) void scrollToTranslation(translationId)
  }, [refRowByKey, rows, scrollToTranslation])

  // The active segment's cross-references for the sidebar's References section:
  // the term each chip renders + the row that defines it. Identity is stable
  // across unrelated edits (deps change only on segment switch or when a
  // referenced target changes), which is what the sidebar's memo comparator
  // relies on.
  const activeReferences = useMemo<SegmentReference[]>(() => {
    return segmentRefKeys(activeSourceRich).map(key => {
      const idx = refRowByKey.get(key)
      const term = refTerms.get(key)
      return {
        refKey: key,
        source: term?.source ?? '',
        target: term?.target ?? '',
        segmentNumber: idx !== undefined ? segmentNumber(rows[idx]) : null,
      }
    })
  }, [activeSourceRich, refRowByKey, refTerms, rows])

  return { refTermsByFile, activeReferences, jumpToReference }
}

const NO_TERMS: RefTerms = new Map()
