import { useCallback, useState } from 'react'
import type { RefObject } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { listJobTermBases } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useLastSelection } from '@/hooks/useLastSelection'
import { quickAddBlockedReason } from '@/lib/term-quick-add'
import type { SpellRunScope } from '@/lib/spellcheck-run'
import { DEFAULT_RUN_SCOPE } from '@/lib/spellcheck-scope'
import type { ActiveSegment } from './useActiveSegment'

interface Options {
  jid: number
  isManager: boolean
  segmentScrollRef: RefObject<HTMLDivElement | null>
  active: Pick<ActiveSegment, 'activeSegmentIdxRef' | 'activeInsertRef'>
}

/**
 * Open state of the editor's job-level tools: Concordance, Find & replace,
 * Spell check, Jump to segment and term quick-add.
 */
export function useEditorDialogs({ jid, isManager, segmentScrollRef, active }: Options) {
  const { activeSegmentIdxRef, activeInsertRef } = active

  const [concordanceOpen, setConcordanceOpen] = useState(false)
  const [concordanceQuery, setConcordanceQuery] = useState('')
  // Concordance/terminology lookups start from whatever the linguist highlighted
  // in the segment list — source cell or target editor, either side is valid
  // (the query is matched against both columns of the TM).
  const { getSelectionText } = useLastSelection(segmentScrollRef)

  // Both entry points (Mod+K, toolbar button) route through here, so the query is
  // always the phrase highlighted in the segment list — source cell or target
  // editor, both inside `segmentScrollRef`. Read via useLastSelection rather than
  // window.getSelection(): a button's mousedown collapses the selection before
  // its onClick runs. No selection → empty query, and the modal focuses its input.
  const openConcordance = useCallback(() => {
    setConcordanceQuery(getSelectionText())
    setConcordanceOpen(true)
  }, [getSelectionText])

  const [findReplaceOpen, setFindReplaceOpen] = useState(false)
  // Whether Find & replace opens limited to the segment selection (its bar button).
  const [findInSelection, setFindInSelection] = useState(false)
  const [findHighlightTerm, setFindHighlightTerm] = useState<string | null>(null)
  const openFindReplace = useCallback((inSelection: boolean) => {
    setFindInSelection(inSelection)
    setFindReplaceOpen(true)
  }, [])
  const closeFindReplace = useCallback(() => {
    setFindReplaceOpen(false)
    setFindHighlightTerm(null)
  }, [])

  // Spell check review dialog, and the scope it opens on.
  const [spellCheckOpen, setSpellCheckOpen] = useState(false)
  const [spellCheckScope, setSpellCheckScope] = useState<SpellRunScope>(DEFAULT_RUN_SCOPE)
  const openSpellCheck = useCallback((scope: SpellRunScope) => {
    setSpellCheckScope(scope)
    setSpellCheckOpen(true)
  }, [])

  // Jump to segment dialog
  const [jumpOpen, setJumpOpen] = useState(false)
  const [jumpInput, setJumpInput] = useState('')
  const openJump = useCallback(() => {
    setJumpOpen(true)
    setJumpInput('')
  }, [])

  // Term quick-add (Alt+Shift+G, the Terminology panel's +), prefilled from
  // what is selected in the open segment's source and translation.
  const [quickAddTerm, setQuickAddTerm] = useState<{ source: string; target: string } | null>(null)
  // Whether this user may add terms here: the project lets them (managers
  // always may) and a term base for the job's pair allows Write. The + shows
  // only then, or after a failed load so a click can try again; it refreshes
  // when the tab comes back into focus.
  const { data: jobTermBases, isError: jobTermBasesFailed, refetch: refetchJobTermBases } = useQuery({
    queryKey: queryKeys.jobTermBases(jid),
    queryFn: () => listJobTermBases(jid),
    enabled: Number.isFinite(jid),
    refetchOnWindowFocus: true,
  })
  const quickAddOpen = jobTermBasesFailed || quickAddBlockedReason(jobTermBases, { isManager }) === null
  const openQuickAddTerm = useCallback(async () => {
    if (activeSegmentIdxRef.current === null) return
    // The selection first, then a fresh list: a manager may have changed the
    // setting since the editor opened.
    const selection = activeInsertRef.current.getTermSelection?.() ?? { source: '', target: '' }
    const fresh = await refetchJobTermBases()
    const reason = quickAddBlockedReason(fresh.data, { failed: fresh.isError, isManager })
    if (reason !== null) {
      if (reason) (fresh.isError ? toast.error : toast.warning)(reason)
      return
    }
    setQuickAddTerm(selection)
  }, [refetchJobTermBases, isManager, activeSegmentIdxRef, activeInsertRef])

  return {
    concordanceOpen,
    setConcordanceOpen,
    concordanceQuery,
    openConcordance,
    findReplaceOpen,
    findInSelection,
    findHighlightTerm,
    setFindHighlightTerm,
    openFindReplace,
    closeFindReplace,
    spellCheckOpen,
    setSpellCheckOpen,
    spellCheckScope,
    openSpellCheck,
    jumpOpen,
    setJumpOpen,
    jumpInput,
    setJumpInput,
    openJump,
    quickAddTerm,
    setQuickAddTerm,
    quickAddOpen,
    openQuickAddTerm,
  }
}

export type EditorDialogs = ReturnType<typeof useEditorDialogs>
