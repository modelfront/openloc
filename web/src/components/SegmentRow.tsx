import { useState, useRef, useEffect, useEffectEvent, useCallback, useMemo, memo } from 'react'
import { useLatestRef } from '@/hooks/useLatestRef'
import { useOnChange } from '@/hooks/useOnChange'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  updateTranslationHuman,
  saveTranslationDraft,
  lockTranslation,
  unlockTranslation,
  lookupTerms,
  propagateTranslation,
  runTranslationQA,
  type Segment,
  type Translation,
  type TermLookupResult,
} from '../api'
import { useClearSegment } from '../hooks/useClearTranslations'
import { refResolver, type RefTerms } from '../lib/ref-terms'
import ConfirmDialog from './ConfirmDialog'
import { isSaveConflictError, isSegmentHeldError, isSuggestionGoneError, getConflictPayload, type ConflictPayload } from '../lib/save-conflict'
import { SegmentHeldIcon, SegmentHeldNotice } from './SegmentHoldIndicators'
import { handleTaskLocked, handleTaskReopened, isTaskLockedError } from '../lib/task-edit'
import { warnHeldSkipped } from '../lib/segment-bulk'
import { isQABlockError, getQAIssuesFromError, formatQAErrorToast } from '../lib/qa-block'
import { highlightText } from '../lib/text-highlight'
import { qaIssuesEqual, type QaFinding } from '../lib/qa-findings'
import QASummaryDot from './QASummaryDot'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext, applyTMUpdatesAfterSave } from '../lib/query-invalidation'
import { applySegmentSaveSuccess, patchPropagatedSiblings, optimisticAdjustLeverage } from '../lib/segment-save'
import { deriveDisplayStatus, showStatusChip, deriveMatchChip, isAssistantConfirmed, ASSISTANT_CONFIRMED_LABEL } from '../lib/segment-status'
import { useInFlightGuard } from '../hooks/useInFlightGuard'
import { trackPropagation, reconcilePropagationBaseline } from '../lib/propagation-guard'
import { smartReplace, getTermSelection } from '../lib/term-insert'
import { plainOffsetToPm } from '../lib/pm-offset'
import SegmentEditor, { type SegmentEditorHandle } from './SegmentEditor'
import type { SpellFixActions } from '../lib/spellcheck-scope'
import SegmentEditorToolbar from './SegmentEditorToolbar'
import MissingTagsBar from './MissingTagsBar'
import { getMissingTags, type MissingTag } from '../lib/missing-tags'
import { MessageSquare } from 'lucide-react'
import { Chip } from '@/components/ui/chip'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { SafeHTML } from './SafeHTML'
import ConflictBanner from './ConflictBanner'
import { useSegmentEditingTimer } from '../hooks/useSegmentEditingTimer'

// Shared grid template for the segment table. The header row (CATEditor) and
// every body row (below) MUST use the exact same column definition or their
// cells drift out of alignment. Keep this the single source of truth — never
// inline the `grid-cols-[...]` literal in either place again.
// Column widths come from `--seg-cols`, set on the table container
// (lib/segment-columns). Match holds only the match chip; the extras column
// holds everything else — lock, QE chip, QA dot, comment count. The Match,
// Status and extras cells use px-2, not the table's px-3; their header cells
// must match.
export const SEGMENT_GRID_COLS = 'grid grid-cols-(--seg-cols) gap-0'


// Shared utilities + render helpers. External callers may also import these
// directly from SegmentRow.shared.
import {
  WhitespaceText,
  RichSourceText,
  SegmentText,
  findTextRange,
  serializeEditor,
  termLookupCache,
  hasXliffTags,
  hasHTMLFormatting,
  STATUS_CHIP_COLORS,
  qeChipInfo,
  modKey,
  statusLabel,
  SEGMENT_PREVIEW_CLAMP,
} from './SegmentRow.shared'
import { PREVIOUS_AUTO_APPROVED_TOOLTIP } from './step-status-labels'

// Re-exported for external callers (CATEditor, tests) that import them from
// this file.
export {
  WhitespaceText,
  RichSourceText,
  HighlightedSource,
  findTextRange,
  WS_MARKERS,
  STATUS_CHIP_COLORS,
  STATUS_LABELS,
  statusLabel,
  OUTCOME_LABELS,
  isMac,
  modKey,
} from './SegmentRow.shared'

// XLIFF tag wrappers, dropped when the stored text is compared with the
// editor's plain text for dirty detection.
const XLIFF_WRAPPER_RE = /<(bpt|ept|ph|it|x|g|hi|sub)(\s[^>]*)?>(.+?)<\/\1>|<(bpt|ept|ph|it|x|g|hi|sub)(\s[^>]*)?\/>/g
const withoutXliffTags = (text: string) => (hasXliffTags(text) ? text.replace(XLIFF_WRAPPER_RE, '') : text)

// The parts of a row version the editor loads.
type RowVersion = { active_text?: string | null; active_rich_text?: string | null; updated_at?: string | null }

export interface SegmentRowProps {
  segment: Segment
  translation?: Translation
  index: number
  jobId: number
  fileId: number
  projectId: number | null
  targetLanguage: string
  sourceLanguage: string
  isActive: boolean
  onActivate: () => void
  onNavigate: (dir: 'next' | 'prev') => void
  onDeactivate: () => void
  dirtyRef: React.MutableRefObject<boolean>
  onTermsLoaded?: (terms: TermLookupResult[]) => void
  onEditTextChange?: (text: string) => void
  insertRef?: React.MutableRefObject<{
    selectAndReplace: (src: string, tgt: string, mt?: string) => void
    selectTerm: (src: string, tgt: string, mt?: string) => void
    /** Replaces the target. With an insert token (a Resources-panel insert)
     *  it saves the draft at once, recording where the text came from. */
    replaceAll?: (text: string, insertToken?: string) => void
    confirm?: () => void
    copySource?: () => void
    clear?: () => void
  }>
  flushDraftRef?: React.MutableRefObject<() => Promise<void>>
  readOnly?: boolean
  qaIssues: QaFinding[]
  onQARefresh?: (translationId: number, issues: QaFinding[]) => void
  showWhitespace?: boolean
  /** Cross-reference terms for this file. Identity-stable — see
   *  lib/ref-terms.ts — so it can be compared by reference in the memo. */
  refTerms?: RefTerms
  spellCheck?: boolean
  spellFixActions?: SpellFixActions
  findHighlightTerm?: string | null
  /** Source text of the TM hit this segment is being compared with. The parts
   *  of this segment's source that the hit does NOT have are highlighted, the
   *  mirror image of the highlight on the hit's own card. Active row only. */
  tmDiffReference?: string | null
  /**
   * Current user's ID — passed through to the conflict banner so it can
   * distinguish "another user edited this" from "you edited this from
   * another tab/session." `null` while auth/me is still resolving.
   */
  currentUserId?: number | null
  /** True for owner/admin; gates the per-segment Clear button. */
  isAdmin?: boolean
  /**
   * True for managers (owner/admin/project manager), who may lock — locking
   * approves the segment (mirrors RequireManager on POST /lock). Anyone who
   * may edit the row may unlock it. Undefined hides Lock while auth/me loads.
   */
  canLock?: boolean
  // Defaults true; gates the per-segment Clear button.
  inFirstStep?: boolean
  /** True when this segment's source text appears earlier in the same file.
   *  Drives the "⇢R" chip in the Match column: only later occurrences are
   *  tagged as repetitions.
   *  Computed by the parent (CATEditor) so the per-row component stays a
   *  pure renderer + the comparator stays simple. Mutually exclusive with
   *  isRepetitionSource. */
  isRepetition?: boolean
  /** True when this segment is the FIRST occurrence of a source_text that
   *  appears again later in the file. Drives the "→R" chip, which marks the
   *  source of repetitions apart from the auto-propagated targets. Mutually
   *  exclusive with
   *  isRepetition. */
  isRepetitionSource?: boolean
  /** Number of comments on this segment's translation. Drives the comment
   *  indicator in the meta column; 0/undefined renders nothing. Sourced from
   *  the batched per-file count endpoint (no N+1 per row). */
  commentCount?: number
  /** In the editor's multi-segment selection. */
  isSelected?: boolean
  /** Checkbox in the # cell: (translationId, shiftKey). Identity-stable (it is
   *  not compared), so it must read state through refs. Absent = no checkbox. */
  onSelectClick?: (translationId: number, shift: boolean) => void
  /** Another user has this segment open: their name, and the row is read-only. */
  heldByName?: string | null
  /** The last finished workflow step left this segment auto-approved. */
  autoApprovedBeforeStep?: boolean
  /** The row's AI verdict came from an earlier step (see deriveDisplayStatus). */
  predictedBeforeStep?: boolean
  /** The file's strings are plain text ("Strings contain HTML" off): a save
   *  sends the editor's text, never its HTML. */
  plainText?: boolean
}

export function segmentRowEqual(prev: SegmentRowProps, next: SegmentRowProps): boolean {
  // Always re-render the active row (it has internal editor state)
  if (prev.isActive || next.isActive) return false

  // For inactive rows, only re-render when data changes
  return (
    prev.segment.id === next.segment.id &&
    prev.translation?.id === next.translation?.id &&
    // The status badge keys off the DERIVED display status, which
    // depends on status + active_text + edited_by + origin + predict_outcome —
    // compare the derived value so a lineage change repaints the chip.
    deriveDisplayStatus(prev.translation, { predictedBeforeStep: prev.predictedBeforeStep }) ===
      deriveDisplayStatus(next.translation, { predictedBeforeStep: next.predictedBeforeStep }) &&
    prev.translation?.active_text === next.translation?.active_text &&
    prev.translation?.active_rich_text === next.translation?.active_rich_text &&
    // The Match chip reads tm_match_percent (e.g. 100% → 101% context match).
    prev.translation?.tm_match_percent === next.translation?.tm_match_percent &&
    prev.translation?.predict_outcome === next.translation?.predict_outcome &&
    // The QE chip reads qe_note (LLM quality score).
    prev.translation?.qe_note === next.translation?.qe_note &&
    prev.translation?.locked === next.translation?.locked &&
    prev.translation?.confirmed_via === next.translation?.confirmed_via &&
    prev.readOnly === next.readOnly &&
    prev.showWhitespace === next.showWhitespace &&
    prev.plainText === next.plainText &&
    prev.refTerms === next.refTerms &&
    prev.spellCheck === next.spellCheck &&
    prev.findHighlightTerm === next.findHighlightTerm &&
    prev.tmDiffReference === next.tmDiffReference &&
    qaIssuesEqual(prev.qaIssues, next.qaIssues) &&
    prev.isAdmin === next.isAdmin &&
    prev.canLock === next.canLock &&
    prev.inFirstStep === next.inFirstStep &&
    prev.isRepetition === next.isRepetition &&
    prev.isRepetitionSource === next.isRepetitionSource &&
    prev.commentCount === next.commentCount &&
    prev.isSelected === next.isSelected &&
    (prev.heldByName ?? null) === (next.heldByName ?? null) &&
    !prev.onSelectClick === !next.onSelectClick &&
    prev.autoApprovedBeforeStep === next.autoApprovedBeforeStep
  )
}

// One empty list for every row without missing tags, so callbacks keyed on it
// keep their identity.
const NO_MISSING_TAGS: MissingTag[] = []
const NO_TERMS: TermLookupResult[] = []

// The text a version loads into the editor. Rich text (with tags) first:
// editText starts raw (may include XLIFF tags) for textToContent(); after
// mount, onTextChange replaces it with plain text from editor.getText().
function rowText(v: RowVersion | undefined): string {
  return v?.active_rich_text || v?.active_text || ''
}

/**
 * Memoized SegmentRow — inactive rows skip re-render unless their data changes.
 * Active row always re-renders (editor has internal state).
 * Refs and callbacks are excluded from comparison (they're stable or ref-based).
 */
export const SegmentRow = memo(function SegmentRow({
  segment,
  translation,
  index,
  jobId,
  fileId,
  projectId,
  targetLanguage,
  sourceLanguage,
  isActive,
  onActivate,
  onNavigate,
  onDeactivate,
  dirtyRef,
  onTermsLoaded,
  onEditTextChange,
  insertRef,
  flushDraftRef,
  readOnly,
  qaIssues,
  onQARefresh,
  showWhitespace,
  refTerms,
  spellCheck,
  spellFixActions,
  findHighlightTerm,
  tmDiffReference,
  currentUserId,
  isAdmin,
  canLock,
  inFirstStep = true,
  isRepetition = false,
  isRepetitionSource = false,
  commentCount = 0,
  isSelected = false,
  onSelectClick,
  heldByName = null,
  autoApprovedBeforeStep,
  predictedBeforeStep,
  plainText = false,
}: SegmentRowProps) {
  // Source column mirrors the source document, target the deliverable — so a
  // reference reads "Wet" on the left and "Feucht" on the right.
  const resolveRefSource = useMemo(
    () => (refTerms ? refResolver(refTerms, 'source') : undefined), [refTerms])
  const resolveRefTarget = useMemo(
    () => (refTerms ? refResolver(refTerms, 'target') : undefined), [refTerms])

  // A row that mounts active starts loaded; one that turns active loads in
  // the activation hook below.
  const [editText, setEditText] = useState(() => (isActive ? rowText(translation) : ''))  // raw on init, then plain text from editor.getText()
  const [originalText, setOriginalText] = useState(() => (isActive ? withoutXliffTags(rowText(translation)) : ''))  // plain text baseline for dirty detection
  // True once the row's text is loaded into editText. Until then the editor
  // mounts with editorInitialText; after that an empty editText means "cleared".
  const [editTextReady, setEditTextReady] = useState(isActive)

  // Compute the raw rich text for editor init. Used as fallback when editText is still
  // empty (first render before useEffect fires). This ensures SegmentEditor mounts with
  // XLIFF-tagged content so tags render as atomic chips from the start.
  const editorInitialText = useMemo(() => {
    if (!isActive) return ''
    return translation?.active_rich_text
      || translation?.active_text
      || ''
  }, [isActive, translation?.active_rich_text, translation?.active_text])
  // Terminology for the active row: from the lookup cache, else from the
  // lookup this row ran for the same key.
  const termKey = isActive && projectId && sourceLanguage && targetLanguage && segment.source_text
    ? `${projectId}:${sourceLanguage}:${targetLanguage}:${segment.source_text}`
    : null
  const [lookedUpTerms, setLookedUpTerms] = useState<{ key: string; terms: TermLookupResult[] } | null>(null)
  const matchingTerms = termKey == null
    ? NO_TERMS
    : termLookupCache.get(termKey) ?? (lookedUpTerms?.key === termKey ? lookedUpTerms.terms : NO_TERMS)
  // The editor handle: in a ref for callbacks and effects, in state for render.
  const editorRef = useRef<SegmentEditorHandle | null>(null)
  const [editorHandle, setEditorHandle] = useState<SegmentEditorHandle | null>(null)
  const attachEditor = useCallback((handle: SegmentEditorHandle | null) => {
    editorRef.current = handle
    setEditorHandle(handle)
  }, [])
  const cursorPosRef = useRef<{ start: number; end: number }>({ start: 0, end: 0 })
  // Bumped on every editor transaction (text edit OR atom-node insert/delete).
  // `editText` alone is plain text, so atom-only changes (chip insert via F8,
  // chip delete via Backspace) don't change it. The re-render this forces
  // recomputes the missing tags from the live document.
  const [, setDocTick] = useState(0)
  // Editing-time accounting: start() on activate, stop() on deactivate, consume() on confirm.
  const editingTimer = useSegmentEditingTimer(translation?.id)
  const queryClient = useQueryClient()

  // Hoisted before the rest of the hooks so callbacks can include it in
  // their dep array without hitting the temporal dead zone (TS2454).
  const isLocked = translation?.locked === true
  const effectiveReadOnly = readOnly || isLocked || !!heldByName
  // Background of the action bar floating on the editor's top border — the
  // editor's own background (index.css `--seg-editor-bg`).
  const editorChromeBg = effectiveReadOnly ? 'bg-(--seg-editor-bg-readonly)' : 'bg-(--seg-editor-bg)'

  // Optimistic locking: the updated_at we saw when the user
  // started editing this row. Sent with every save — if the server's current
  // updated_at differs, another user has edited the row and we get 409.
  // Updated on every successful save so back-to-back saves by the same user
  // keep working. Cleared when the row is deactivated.
  const baselineUpdatedAtRef = useRef<string | null>(null)

  // Pending-guard for the lock/unlock toggle button. With admin-only unlock,
  // two requests in flight from a double-click could lock-then-unlock (or vice
  // versa) and leave the segment in a state the user did not click for.
  const lockToggleInFlightRef = useRef<boolean>(false)

  const [clearConfirmOpen, setClearConfirmOpen] = useState(false)
  const clearMut = useClearSegment({ fileId, jobId, projectId: projectId ?? 0 })
  // Clear shows on every row the user may clear, translated or not. A saved
  // translation is cleared on the server after a confirm; with nothing saved,
  // Clear only empties the editor.
  const canClear = !!isAdmin && inFirstStep && !!translation && !effectiveReadOnly
  const hasSavedTranslation = deriveDisplayStatus(translation, { predictedBeforeStep }) !== 'untranslated'
  const requestClear = useCallback(() => {
    if (hasSavedTranslation) {
      setClearConfirmOpen(true)
      return
    }
    setEditText('')
    editorRef.current?.setText('')
  }, [hasSavedTranslation])
  // The user emptied a target that had text. Leaving it clears the translation
  // when the Clear button is allowed; otherwise the saved text stays.
  const emptiedRef = useRef(false)

  // When a 409 comes back, we render a side-by-side banner above the editor.
  // The payload carries the
  // server's current row state so the user sees both versions and picks one
  // explicitly — no dismiss trap, no silent overwrite. Cleared on row
  // deactivation; re-emerges on the next 409 if the conflict persists.
  const [conflictPayload, setConflictPayload] = useState<ConflictPayload | null>(null)

  // Shared in-flight guard for saveMut + saveDraft. Both save paths call
  // `guard(fn)` to serialise; the second caller awaits the first's completion
  // before issuing its own PUT, so neither races the other.
  const guardSave = useInFlightGuard()

  const saveMut = useMutation({
    mutationFn: () => guardSave(async () => {
      // After awaiting any prior in-flight save (saveDraft auto-save, draft
      // flush, or another saveMut in flight), `baselineUpdatedAtRef` has
      // already advanced to T1, so the PUT /human we issue uses the FRESH
      // baseline and the server doesn't 409 against our own previous write.
      //
      // `applySegmentSaveSuccess` MUST run inside this guarded block (before
      // the sentinel resolves) so any saveDraft queued behind us wakes up
      // observing the FRESH baseline + cleared dirty. Moving it to onSuccess
      // (outside guardSave) lets queued saves read a stale baseline and 409.
      //
      // If an auto-propagation to THIS row's source is still in flight, it has
      // already bumped our server-side updated_at; wait for it and adopt the
      // fresh baseline so we don't 409 against the user's own propagation.
      await reconcilePropagationBaseline(jobId, segment.source_text, translation?.id, baselineUpdatedAtRef)
      const delta = editingTimer.consume()
      const { text, richText } = serializeEditor(editorRef.current, editText, plainText)
      let result: Awaited<ReturnType<typeof updateTranslationHuman>>
      try {
        result = await updateTranslationHuman(
          fileId,
          translation!.id,
          text,
          delta,
          richText,
          baselineUpdatedAtRef.current,
        )
      } catch (err) {
        // A refused confirm recorded no time: the next confirm reports it.
        editingTimer.restore(delta)
        throw err
      }
      const { translation: saved, updatedTmIds, taskReopened } = result
      applySegmentSaveSuccess({
        queryClient, jobId, saved,
        baselineUpdatedAtRef, dirtyRef, setOriginalText,
      })
      // Instant status-bar update — patch the leverage cache synchronously
      // before invalidateJobContext fires the (slower) refetch. Without
      // this, Cmd+Enter spam confirmed segments faster than the bar could
      // observe them. See lib/segment-save.ts for the band-mapping logic.
      optimisticAdjustLeverage(queryClient, jobId, translation, saved, segment.word_count, segment.source_text.length)
      // Invalidate the TM browser caches that just received the new entry +
      // show the throttled "TM updated" toast. One helper, shared with the
      // use-mine conflict-resolution path so the two can't drift.
      applyTMUpdatesAfterSave(queryClient, updatedTmIds, toast.success)
      if (taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
      return saved
    }),
    onSuccess: (saved) => {
      // The 4 invariants (baseline / cache / originalText / dirty) are
      // applied inside mutationFn BEFORE the sentinel resolves — see comment
      // there. Side effects below don't affect this row's baseline state and
      // can run after the sentinel resolves without exposing queued callers
      // to stale data.
      if (projectId) invalidateJobContext(queryClient, projectId, jobId)
      // Auto-run QA for this translation after confirm. POST recomputes +
      // persists so badges reflect the just-saved text — a
      // bare GET would return whatever the last full-job run left behind.
      if (translation?.id && onQARefresh) {
        runTranslationQA(translation.id).then(issues => onQARefresh(translation!.id, issues)).catch((err) => console.warn('QA refresh failed:', err))
      }
      // Auto-propagation: apply same translation to all segments with same source text.
      // Forward the rich-text form so identical-source siblings inherit the
      // user's tag chips.
      if (translation?.id) {
        // Track the propagation so a sibling save (same source) can await it
        // and adopt the post-propagation baseline — closing the in-flight
        // window patchPropagatedSiblings (response-time only) leaves open.
        const propagated = propagateTranslation(
          fileId,
          segment.source_text,
          targetLanguage,
          editText,
          translation.id,
          saved?.active_rich_text ?? undefined,
        )
          .then(r => {
            if (r.taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
            if (r.propagated > 0) {
              // Patch the cache for each sibling synchronously BEFORE
              // invalidation refetches, so a sibling re-activated within the
              // refetch window doesn't capture a stale `baselineUpdatedAtRef`
              // and false-409 on its next save.
              patchPropagatedSiblings({ queryClient, jobId, siblings: r.siblings })
              toast.success(`Propagated to ${r.propagated} segment${r.propagated > 1 ? 's' : ''}`)
              if (projectId) invalidateJobContext(queryClient, projectId, jobId)
            }
            warnHeldSkipped(r.held, 'repetition')
            return new Map<number, string>(
              r.siblings.filter(s => s.updated_at).map(s => [s.id, s.updated_at!]),
            )
          })
          .catch((err) => { console.warn('QA/propagation failed:', err); return new Map<number, string>() })
        trackPropagation(jobId, segment.source_text, propagated)
      }
    },
    onError: (err: Error) => {
      if (isTaskLockedError(err)) { handleTaskLocked(queryClient, projectId ?? 0, jobId, err); return }
      if (isSegmentHeldError(err)) { toast.error(err.message); return }
      if (isSaveConflictError(err)) {
        setConflictPayload(getConflictPayload(err))
        return
      }
      if (isQABlockError(err)) {
        // The server's QA gate refused the confirm. The issues in the
        // response were computed on the text just sent, so they — not the
        // cached findings in the QA panel — are what the toast names.
        const issues = getQAIssuesFromError(err)
        toast.error(formatQAErrorToast(issues))
        return
      }
      toast.error(`Save failed: ${err.message}`)
    },
  })

  // Loads a version of the row into the editor: its text, the plain-text
  // baseline for dirty detection, and its updated_at as the optimistic-lock
  // baseline (everything typed from here is an edit against this version).
  // loadVersionText is the state half, safe during render; loadVersion adds
  // the baseline, for callbacks.
  const loadVersionText = useCallback((v: RowVersion | undefined) => {
    const raw = rowText(v)
    setEditText(raw)
    setEditTextReady(true)
    setOriginalText(withoutXliffTags(raw))
  }, [])
  const loadVersion = useCallback((v: RowVersion | undefined) => {
    loadVersionText(v)
    baselineUpdatedAtRef.current = v?.updated_at ?? null
  }, [loadVersionText])

  // When becoming active, load the row (its text here, during render; its
  // baseline in the effect) and start the editing timer.
  useOnChange([isActive], () => {
    if (isActive) {
      loadVersionText(translation)
    } else {
      setEditTextReady(false)
      // Clear conflict banner state on deactivate. If the conflict still exists
      // when the user comes back, the next save will re-trigger the 409 and the
      // banner will reappear. State doesn't need to survive virtualization
      // unmount — the optimistic-lock baseline does that work.
      setConflictPayload(null)
    }
  })
  useEffect(() => {
    if (isActive) {
      baselineUpdatedAtRef.current = translation?.updated_at ?? null
      editingTimer.start()
    } else {
      editingTimer.stop()
      if (emptiedRef.current) {
        toast.warning('Empty translation not saved')
        emptiedRef.current = false
      }
      baselineUpdatedAtRef.current = null
      onTermsLoaded?.([])
    }
  }, [isActive]) // eslint-disable-line react-hooks/exhaustive-deps -- the row as it is at activation; newer versions go through the effect below

  // A newer version of the row while it is active: a refetch, another user's
  // save, pre-translation, the echo of this row's own save. The editor follows
  // it only when it holds no unsaved typing, or is read-only (nothing typed
  // there is saved). Unsaved typing is never replaced: its baseline stays, so
  // the next save meets the optimistic lock and the conflict banner decides.
  // Turning read-only alone loads nothing: the typing stays readable.
  // The text loads during render; the effect then moves the baseline, under
  // the same test on the state the render left (equal text once loaded).
  const followsNewVersion = isActive && editTextReady && (effectiveReadOnly || withoutXliffTags(editText) === originalText)
  useOnChange([translation?.active_text, translation?.active_rich_text, translation?.updated_at], () => {
    if (followsNewVersion) loadVersionText(translation)
  })
  useEffect(() => {
    if (followsNewVersion) baselineUpdatedAtRef.current = translation?.updated_at ?? null
  }, [translation?.active_text, translation?.active_rich_text, translation?.updated_at]) // eslint-disable-line react-hooks/exhaustive-deps -- the state as of this render

  // Select the find match in the editor when activated via Find navigation
  // Only on activation, not on every findHighlightTerm change.
  const selectFindMatch = useEffectEvent(() => {
    if (!findHighlightTerm) return
    const text = editorRef.current?.getText() ?? ''
    const range = findTextRange(text, findHighlightTerm)
    if (range) {
      editorRef.current?.selectPlainRange(range.from, range.to)
    }
  })
  useEffect(() => {
    if (!isActive) return
    // Editor needs a tick to mount and focus before we can set selection
    const timer = setTimeout(() => selectFindMatch(), 50)
    return () => clearTimeout(timer)
  }, [isActive])

  // Sync dirty state and editText to parent (for unsaved changes warning + term panel)
  useEffect(() => {
    if (isActive) {
      const ep = withoutXliffTags(editText)
      emptiedRef.current = ep.trim() === '' && originalText.trim() !== ''
      dirtyRef.current = (ep.trim() !== '' && ep !== originalText) || (emptiedRef.current && canClear)
      onEditTextChange?.(editText)
    }
  }, [isActive, editText, originalText, dirtyRef, onEditTextChange, canClear])

  // --- Centralized draft save ---
  // Single save function used by auto-save, flush (before QA), deactivate and
  // a Resources-panel insert. `guardSave` serialises concurrent calls so a
  // second saveDraft (or a saveMut) that arrives mid-flight queues behind the
  // first instead of racing with the same baseline.
  // `insert` is a panel insert: it always saves, with the text just inserted
  // (editText still holds the previous text in this closure) and the insert
  // token, so the server records the TM match or MT suggestion.
  // The parent passes an inline arrow, new every render: read it through a ref
  // so saveDraft keeps its identity and the autosave timer below isn't reset.
  const onQARefreshRef = useLatestRef(onQARefresh)
  const saveDraft = useCallback(async (insert?: { text: string; insertToken: string }) => {
    if (!translation || effectiveReadOnly) return
    await guardSave(async () => {
      // Re-check INSIDE the guarded section: after awaiting any prior
      // in-flight save, the dirty/baseline state may have changed, and a PUT
      // /draft with no pending edit 400s.
      if (!dirtyRef.current && !insert) return
      if (emptiedRef.current && !insert) {
        const { translation: cleared } = await clearMut.mutateAsync(translation.id)
        loadVersion(cleared)
        emptiedRef.current = false
        dirtyRef.current = false
        return
      }
      // Same in-flight propagation reconcile as saveMut — an autosave/flush on
      // a propagated sibling must not 409 against the user's own propagation.
      await reconcilePropagationBaseline(jobId, segment.source_text, translation.id, baselineUpdatedAtRef)
      const { text, richText } = serializeEditor(editorRef.current, insert?.text ?? editText, plainText)
      try {
        const { translation: saved, taskReopened } = await saveTranslationDraft(
          fileId,
          translation.id,
          text,
          0, // a draft reports no editing time; the confirm reports it
          richText,
          baselineUpdatedAtRef.current,
          insert?.insertToken,
        )
        // Post-save sync (advance baseline, patch cache, sync originalText,
        // clear dirty) — single helper, used by saveMut + saveDraft + onUseMine.
        applySegmentSaveSuccess({
          queryClient, jobId, saved,
          baselineUpdatedAtRef, dirtyRef, setOriginalText,
        })
        // Same instant leverage-bar update as the main saveMut path.
        optimisticAdjustLeverage(queryClient, jobId, translation, saved, segment.word_count, segment.source_text.length)
        if (taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
        // Re-run QA for THIS segment after every autosave.
        // POST recomputes + persists so the badges track the draft as the user
        // types; scoped to the edited segment, not the whole job.
        const refreshQA = onQARefreshRef.current
        if (refreshQA) {
          runTranslationQA(translation.id).then(issues => refreshQA(translation.id, issues)).catch((err) => console.warn('QA refresh failed:', err))
        }
      } catch (err) {
        if (isTaskLockedError(err)) { handleTaskLocked(queryClient, projectId ?? 0, jobId, err); return }
        if (isSegmentHeldError(err)) { toast.error((err as Error).message); return }
        if (isSuggestionGoneError(err)) {
          // The text stays dirty, so the autosave keeps it as an ordinary edit.
          toast.error('That match is no longer in the translation memory, so it is kept as your own edit.')
          return
        }
        if (isSaveConflictError(err)) {
          setConflictPayload(getConflictPayload(err))
          return
        }
        throw err
      }
    })
  }, [translation, effectiveReadOnly, editText, fileId, projectId, dirtyRef, queryClient, jobId, guardSave, clearMut, loadVersion, onQARefreshRef, segment.source_text, segment.word_count, plainText])

  // Auto-save: debounced 3s after edit, only if dirty.
  // `isActive` is in deps so deactivating the segment (any
  // transition path) runs the cleanup `clearTimeout(timer)` and no draft PUT
  // fires after deactivate.
  useEffect(() => {
    if (!isActive || !translation || !dirtyRef.current || emptiedRef.current || effectiveReadOnly) return
    const timer = setTimeout(() => { saveDraft().catch(() => toast.error('Draft save failed')) }, 3000)
    return () => clearTimeout(timer)
  }, [editText, isActive]) // eslint-disable-line react-hooks/exhaustive-deps

  // Expose flush for parent (before QA, on deactivate). Always saves — no dirty check.
  useEffect(() => {
    if (!flushDraftRef || !isActive) return
    flushDraftRef.current = saveDraft
    return () => { if (flushDraftRef) flushDraftRef.current = async () => {} }
  }, [flushDraftRef, isActive, saveDraft])

  // Look up terminology when segment becomes active (cached by sourceText)
  useEffect(() => {
    if (!isActive || !projectId || !sourceLanguage || !targetLanguage || !segment.source_text) {
      return
    }
    const cacheKey = `${projectId}:${sourceLanguage}:${targetLanguage}:${segment.source_text}`
    const cached = termLookupCache.get(cacheKey)
    if (cached) {
      onTermsLoaded?.(cached)
      return
    }
    let cancelled = false
    lookupTerms(projectId, sourceLanguage, targetLanguage, segment.source_text)
      .then(terms => {
        if (cancelled) return
        termLookupCache.set(cacheKey, terms)
        setLookedUpTerms({ key: cacheKey, terms })
        onTermsLoaded?.(terms)
      })
      // Propagate the empty result to the sidebar too, so a failed lookup
      // never leaves the PREVIOUS segment's terms in the Terminology panel
      .catch(() => { if (!cancelled) { setLookedUpTerms({ key: cacheKey, terms: NO_TERMS }); onTermsLoaded?.([]) } })
    return () => { cancelled = true }
  }, [isActive, projectId, sourceLanguage, targetLanguage, segment.source_text]) // eslint-disable-line react-hooks/exhaustive-deps

  // Select a term's match in the editor (for hover preview and auto-select)
  // Uses source term match + MT translation match for cross-language detection
  const selectTermInTextarea = (sourceTerm: string, targetTerm?: string, mtTranslation?: string) => {
    const editor = editorRef.current
    if (!editor) return
    // Hover-preview must NOT clobber an existing manual selection: the linguist
    // may have selected a specific (wrong) occurrence to replace on click. Only
    // auto-highlight the term when nothing is currently selected.
    const cur = cursorPosRef.current
    if (cur.start !== cur.end) return
    const text = editor.getText()
    const sel = getTermSelection(text, sourceTerm, targetTerm, mtTranslation)
    if (sel) {
      cursorPosRef.current = sel
      editor.selectPlainRange(sel.start, sel.end)
    }
  }

  // Smart replace: find source term in translation and replace with target,
  // or insert at cursor if source not found
  const selectAndReplace = (sourceTerm: string, targetTerm: string, mtTranslation?: string) => {
    const editor = editorRef.current
    const tiptap = editor?.getEditor()
    if (effectiveReadOnly || !editor || !tiptap) return
    const text = editor.getText()
    const doc = tiptap.state.doc
    const cursor = cursorPosRef.current
    const result = smartReplace(text, sourceTerm, targetTerm, cursor, mtTranslation)
    // Replace only the changed range, so tag chips elsewhere in the doc survive.
    const start = result.cursor.end - targetTerm.length
    const end = start + (text.length - result.newText.length) + targetTerm.length
    let from: number
    let to: number
    if (start === cursor.start && end === cursor.end) {
      // Insert at the cursor / replace the selection: use the editor's own
      // positions. Chips have no plain-text width, so a plain offset can't tell
      // "between <2> and </2>" from "after </2>".
      ({ from, to } = tiptap.state.selection)
    } else {
      // The end maps from the last replaced character, so a chip right after
      // the term stays outside the range.
      from = plainOffsetToPm(doc, start)
      to = end > start ? plainOffsetToPm(doc, end - 1) + 1 : from
    }
    editor.replaceRange(from, to, targetTerm)
    cursorPosRef.current = result.cursor
    tiptap.commands.setTextSelection(from + targetTerm.length)
  }

  const confirmAndAdvance = useCallback(() => {
    const afterSave = () => {
      // Confirm never locks. Locking is a separate manual action via the lock
      // toggle button.
      queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
      onNavigate('next')
    }

    // No QA check here: the server runs QA on the text being confirmed and
    // refuses with 422 (saveMut.onError shows its findings). The cached
    // `qaIssues` describe the text the last QA run saw, which is often not
    // the text in the editor, so they never decide a confirm.
    if (editText.trim() === '' && segment.source_text) {
      // Empty confirm → copy source to target, save, advance
      const src = plainText ? segment.source_text : segment.source_rich_text || segment.source_text
      setEditText(segment.source_text) // plain text
      editorRef.current?.setText(src)
      saveMut.mutate(undefined, { onSuccess: afterSave })
    } else if (editText.trim()) {
      // Always save on Cmd+Enter — confirms/approves the segment even if unedited
      saveMut.mutate(undefined, { onSuccess: afterSave })
    } else {
      onNavigate('next')
    }
  }, [editText, segment.source_text, segment.source_rich_text, plainText, saveMut, onNavigate, queryClient, jobId])

  // Copy source → target. Same logic as SegmentEditor's onCopySource
  // prop — extracted here so we can expose it via insertRef for the window-level
  // keyboard shortcut in CATEditor (works even when focus is outside the editor).
  const copySourceToTarget = useCallback(() => {
    if (effectiveReadOnly) return
    const src = plainText ? segment.source_text : segment.source_rich_text || segment.source_text
    setEditText(segment.source_text) // plain text for dirty detection
    editorRef.current?.setText(src)
  }, [segment.source_text, segment.source_rich_text, plainText, effectiveReadOnly])

  // Source tags missing from target. Show a small bar above the
  // editor with each missing tag as a clickable chip,
  // plus F8 / Alt+F8 keyboard shortcuts. Recomputed on every keystroke
  // (editText) so the bar disappears the moment all tags are back.
  let missingTags = NO_MISSING_TAGS
  if (isActive && segment.source_rich_text && hasXliffTags(segment.source_rich_text)) {
    const { text, richText } = serializeEditor(editorHandle, editText, plainText)
    missingTags = getMissingTags(segment.source_rich_text, richText || text)
  }

  const insertTagAtCursor = useCallback((tag: MissingTag) => {
    if (effectiveReadOnly) return
    const editor = editorRef.current?.getEditor()
    if (!editor) return
    editor.chain().focus().insertContent({
      type: 'xliffTag',
      attrs: {
        tagType: tag.tagType,
        tagId: tag.tagId,
        ctype: tag.ctype,
        tagNum: tag.tagNum,
        closing: tag.closing,
        rawXml: tag.rawXml,
      },
    }).run()
  }, [effectiveReadOnly])

  const insertNextMissingTag = useCallback(() => {
    if (missingTags.length > 0) insertTagAtCursor(missingTags[0])
  }, [missingTags, insertTagAtCursor])

  const insertAllMissingTags = useCallback(() => {
    // Iterate over the snapshot we computed at the time of the keystroke.
    // Each insert mutates the doc → editText updates → memo recomputes,
    // but we want to insert exactly what was missing when the user asked.
    for (const t of missingTags) insertTagAtCursor(t)
  }, [missingTags, insertTagAtCursor])

  // Imperative handlers for CATEditor's window-level shortcuts (Mod+Enter,
  // Mod+Shift+S, Ctrl+1..5) and the sidebar's inserts. On a read-only or locked
  // row every handler that writes is undefined or a no-op (parity with TipTap's
  // `if (!props.readOnly)` keymap checks); selectTerm, the hover highlight,
  // stays. No deps: re-runs every render so the ref holds the latest closures.
  useEffect(() => {
    if (insertRef) {
      insertRef.current = {
        selectAndReplace,
        selectTerm: selectTermInTextarea,
        replaceAll: effectiveReadOnly ? undefined : (text: string, insertToken?: string) => {
          setEditText(text)
          editorRef.current?.setText(text)
          // The editor's plain text, as typing sends it; the tags travel in the
          // rich text the save reads from the editor.
          if (insertToken) saveDraft({ text: editorRef.current?.getText() ?? text, insertToken }).catch(() => toast.error('Draft save failed'))
        },
        confirm: effectiveReadOnly ? undefined : confirmAndAdvance,
        copySource: effectiveReadOnly ? undefined : copySourceToTarget,
        clear: canClear ? requestClear : undefined,
      }
    }
  })

  const status = deriveDisplayStatus(translation, { predictedBeforeStep })
  const originVal = translation?.origin?.valid ? translation.origin.translation_origin : ''
  // Exactly one provenance chip per row (repetition > TM% > APE > MT > File).
  const matchChip = deriveMatchChip({ isRepetitionSource, isRepetition, origin: originVal })
  const qe = qeChipInfo(translation)
  // The extras column can be 0px wide (no row in the job has extras); an empty
  // cell then drops its padding so it doesn't spill past the row.
  const hasExtras = isLocked || qe != null || qaIssues.length > 0 || commentCount > 0

  return (
    <div
      className={`${SEGMENT_GRID_COLS} border-b border-gray-100 dark:border-gray-800 cursor-pointer transition-colors duration-150 ${isActive ? 'bg-blue-50 dark:bg-blue-950/30' : isSelected ? 'bg-primary/5 hover:bg-primary/10' : 'hover:bg-muted'}`}
      data-selected={isSelected || undefined}
      onClick={!isActive ? onActivate : undefined}
    >
      {onSelectClick && translation ? (
        // The whole cell is the checkbox's click target. Shift+click selects a
        // range; select-none + the mousedown guard keep Shift from also
        // selecting text.
        <div
          className="flex items-start gap-1.5 px-2 py-3 text-xs text-gray-400 dark:text-gray-500 select-none"
          onMouseDown={e => { if (e.shiftKey) e.preventDefault() }}
          onClick={e => {
            e.stopPropagation()
            onSelectClick(translation.id, e.shiftKey)
          }}
          data-testid="segment-select-cell"
        >
          <input
            type="checkbox"
            checked={!!isSelected}
            // The cell handles the click (bubbled from here too); keyboard
            // Space also arrives as a click.
            onChange={() => {}}
            className="mt-px size-3.5 shrink-0 cursor-pointer accent-primary"
            aria-label={`Select segment ${index}`}
            data-testid={`segment-select-${index}`}
          />
          {index}
          {heldByName && <SegmentHeldIcon name={heldByName} />}
        </div>
      ) : (
        <div className="flex items-start gap-1 px-3 py-3 text-xs text-gray-400 dark:text-gray-500">
          {index}
          {heldByName && <SegmentHeldIcon name={heldByName} />}
        </div>
      )}
      {/* min-w-0 lets the 1fr track stay at its fair share instead of growing to
          fit a long unbreakable token; break-words (inherited overflow-wrap)
          then wraps that token inside the cell. Without both, a token with no
          spaces (e.g. "Copyright_2026_Deloitte_...") expands the column and
          misaligns the row. */}
      <div className="px-3 py-3 text-sm text-gray-900 dark:text-gray-100 min-w-0 break-words">
        {/*
          When the segment is active, the source sits in a dashed grey box.
          The translation editor has the same border (index.css
          `.segment-editor .ProseMirror`), so the two read as one frame.
          Geometry comes from .segment-preview / .segment-source-box
          (index.css), shared with the translation cell, so the two columns
          line up and activating the row doesn't move the text.
        */}
        <div
          className={
            isActive
              ? 'segment-source-box border border-dashed border-gray-300 dark:border-gray-700 rounded'
              : 'segment-preview'
          }
          data-testid={isActive ? 'active-source-box' : undefined}
        >
          <div className={isActive ? undefined : SEGMENT_PREVIEW_CLAMP}>
            {/* SegmentText owns the full rendering-mode resolution (chips →
                whitespace → terms → find-highlight → plain), so term underlines
                survive the chip and whitespace paths. Term
                lookups only run for the active segment, hence the isActive
                gate. */}
            <SegmentText
              text={segment.source_text}
              richText={segment.source_rich_text}
              terms={isActive ? matchingTerms : undefined}
              target={isActive ? editText : undefined}
              diffAgainst={isActive ? tmDiffReference : undefined}
              highlightTerm={findHighlightTerm}
              showWhitespace={showWhitespace}
              resolveRef={resolveRefSource}
            />
          </div>
        </div>
      </div>
      <div className="px-3 py-3 min-w-0 break-words" data-translation-cell>
        {isActive ? (
          <div data-active-row>
            {conflictPayload && (
              <ConflictBanner
                fieldLabel="TRANSLATION"
                theirText={conflictPayload.active_text ?? ''}
                theirRichText={conflictPayload.active_rich_text}
                myEditText={editText}
                // serializeEditor reads the live ProseMirror doc and emits the
                // XLIFF rich-text representation. The active row re-renders on
                // every transaction (setDocTick), so this is up to date even
                // while the user keeps typing.
                myEditRichText={serializeEditor(editorHandle, editText, plainText).richText}
                editedByUserId={conflictPayload.edited_by}
                editedByName={conflictPayload.edited_by_name}
                currentUserId={currentUserId}
                onUseTheirs={() => {
                  // Discard local edits and load the version the banner shows;
                  // the refetch brings the same version to the cache.
                  dirtyRef.current = false
                  loadVersion(conflictPayload)
                  queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
                  setConflictPayload(null)
                }}
                onUseMine={() => {
                  if (!translation) return
                  // Re-issue the save with `expected_updated_at = null` to
                  // bypass the optimistic-lock check. The other user's version
                  // is already preserved in `translation_memory_history`
                  // so it isn't lost.
                  const { text, richText } = serializeEditor(editorRef.current, editText, plainText)
                  // This confirm reports the time the refused one handed back.
                  const delta = editingTimer.consume()
                  updateTranslationHuman(fileId, translation.id, text, delta, richText, null)
                    .then(({ translation: saved, updatedTmIds, taskReopened }) => {
                      // Same post-save sync helper as saveMut/saveDraft, so
                      // every save path clears dirty and advances the baseline.
                      applySegmentSaveSuccess({
                        queryClient, jobId, saved,
                        baselineUpdatedAtRef, dirtyRef, setOriginalText,
                      })
                      optimisticAdjustLeverage(queryClient, jobId, translation, saved, segment.word_count, segment.source_text.length)
                      if (projectId) invalidateJobContext(queryClient, projectId, jobId)
                      // Same post-save TM side effects as the main save path:
                      // the backend writes to the configured TMs identically.
                      applyTMUpdatesAfterSave(queryClient, updatedTmIds, toast.success)
                      if (taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
                      setConflictPayload(null)
                    })
                    .catch((err: Error) => {
                      editingTimer.restore(delta)
                      if (isTaskLockedError(err)) { handleTaskLocked(queryClient, projectId ?? 0, jobId, err); return }
                      if (isSegmentHeldError(err)) { toast.error(err.message); return }
                      // Surface QA-block issues here too, so a 422 on the
                      // use-mine retry shows the actionable issue list, not
                      // the generic "Save failed".
                      if (isQABlockError(err)) {
                        toast.error(formatQAErrorToast(getQAIssuesFromError(err)))
                        return
                      }
                      toast.error(`Save failed: ${err.message}`)
                    })
                }}
              />
            )}
            {heldByName && <SegmentHeldNotice name={heldByName} />}
            {!effectiveReadOnly && missingTags.length > 0 && (
              <MissingTagsBar
                missing={missingTags}
                onInsert={insertTagAtCursor}
                onInsertAll={insertAllMissingTags}
              />
            )}
            {/* segment-editor-frame (index.css): activating a row keeps its
                height and the text stays where the preview drew it. */}
            <div className="segment-editor-frame relative">
              {/* The action bar floats on the editor's top border; its background
                  matches the editor's, so it cuts the border like a legend. Half
                  sits in the cell padding, half in the editor's padding, clear
                  of the glyphs. */}
              <div
                className={`absolute right-1.5 top-0 -translate-y-1/2 z-10 flex items-center gap-2 whitespace-nowrap rounded px-1 ${editorChromeBg}`}
                data-testid="segment-editor-actions"
              >
                {!effectiveReadOnly && <SegmentEditorToolbar editor={editorHandle?.getEditor() ?? null} />}
                {translation && (
                  isLocked && readOnly ? (
                    // Locked, and the row is read-only for this user anyway
                    // (not their task): a status label, not a dead button.
                    <span
                      className="text-[9px] text-amber-500 cursor-not-allowed"
                      title="Locked"
                      data-testid="segment-locked-indicator"
                    >🔒 Locked</span>
                  ) : (isLocked || (canLock && deriveDisplayStatus(translation) !== 'untranslated')) ? (
                    // Unlock: anyone who may edit the row. Lock: managers only,
                    // and only a translated segment — locking approves it.
                    // eslint-disable-next-line no-restricted-syntax -- compact inline lock/unlock control in the segment row, not a standard button
                    <button
                      onClick={() => {
                        // Pending-guard + error toast: a silent failure would
                        // hide route-gate 403s, network errors and double-clicks.
                        if (lockToggleInFlightRef.current) return
                        lockToggleInFlightRef.current = true
                        const action = isLocked
                          ? unlockTranslation(fileId, translation.id)
                          : lockTranslation(fileId, translation.id)
                        action
                          // A lock approves the segment, so counts and the
                          // progress bar move with it.
                          .then(() => {
                            if (projectId) invalidateJobContext(queryClient, projectId, jobId)
                            else queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
                          })
                          .catch((err: Error) => toast.error(`${isLocked ? 'Unlock' : 'Lock'} failed: ${err.message}`))
                          .finally(() => { lockToggleInFlightRef.current = false })
                      }}
                      title={isLocked ? 'Unlock to edit' : 'Lock: approve the segment and make it read-only'}
                      className={`text-[9px] cursor-pointer ${isLocked ? 'text-amber-500 hover:text-amber-600' : 'text-gray-400 hover:text-amber-500'}`}
                    >{isLocked ? '🔒 Unlock' : '🔓 Lock'}</button>
                  ) : null
                )}
                {isActive && canClear && (
                  // eslint-disable-next-line no-restricted-syntax -- compact inline clear control in the segment row, not a standard button
                  <button
                    onClick={requestClear}
                    disabled={clearMut.isPending}
                    className="text-[9px] text-gray-400 hover:text-red-500 cursor-pointer disabled:opacity-50 disabled:cursor-wait"
                    title={`Clear translation (${modKey}⇧⌫)`}
                    data-testid="clear-translation-btn"
                  >🧹 Clear</button>
                )}
              </div>
              <SegmentEditor
                ref={attachEditor}
                initialText={editTextReady ? editText : editorInitialText}
                readOnly={effectiveReadOnly}
                placeholder="Click to translate"
                className={effectiveReadOnly ? 'segment-editor-readonly' : ''}
                language={targetLanguage}
                spellCheck={spellCheck}
                spellJobId={jobId}
                spellFixActions={spellFixActions}
                spellSourceText={segment.source_text}
                showWhitespace={showWhitespace}
                resolveRef={resolveRefTarget}
                plainText={plainText}
                onTextChange={(text) => {
                  setEditText(text)
                  // Force memo recomputation on every editor transaction —
                  // even ones that don't change plain text (atom-node moves).
                  setDocTick(t => t + 1)
                }}
                onSelectionChange={(from, to) => {
                  const ed = editorRef.current
                  if (!ed) return
                  cursorPosRef.current = { start: ed.posToOffset(from), end: ed.posToOffset(to) }
                }}
                onConfirmAndAdvance={confirmAndAdvance}
                onNavigate={onNavigate}
                onDeactivate={onDeactivate}
                onCopySource={copySourceToTarget}
                onInsertNextSourceTag={insertNextMissingTag}
                onInsertAllSourceTags={insertAllMissingTags}
                onClearTranslation={() => { if (canClear) requestClear() }}
              />
            </div>
          </div>
        ) : (
          <div
            onClick={onActivate}
            className="segment-preview text-sm cursor-pointer h-full text-gray-700 dark:text-gray-300"
          >
            <div className={SEGMENT_PREVIEW_CLAMP}>
              {translation?.active_text
                ? ((!plainText && translation.active_rich_text && hasXliffTags(translation.active_rich_text))
                      ? <RichSourceText richText={translation.active_rich_text} showWhitespace={showWhitespace} resolveRef={resolveRefTarget} />
                      : !plainText && hasXliffTags(translation.active_text)
                        ? <RichSourceText richText={translation.active_text} showWhitespace={showWhitespace} resolveRef={resolveRefTarget} />
                        : showWhitespace
                          ? <WhitespaceText text={translation.active_text} highlightTerm={findHighlightTerm} />
                          : !plainText && hasHTMLFormatting(translation.active_text)
                            ? <SafeHTML html={translation.active_text} />
                            : findHighlightTerm
                              ? highlightText(translation.active_text, findHighlightTerm)
                              : translation.active_text)
                : <span className="text-gray-300 dark:text-gray-600 italic">{effectiveReadOnly ? 'Empty' : 'Click to translate'}</span>
              }
            </div>
          </div>
        )}
      </div>
      <div className="px-2 py-3 flex items-center justify-center">
        {/* ONE provenance chip per row — deriveMatchChip picks it with
            priority repetition > TM% > APE > MT > File (mutually exclusive
            by design).

            Provenance is grey; only the TM percentage carries a colour,
            because only it says something about quality. Otherwise this
            column would light up on every row and stop meaning anything.

            The two repetition chips are told apart by their arrow glyph
            (→R solid = source, ⇢R dashed = derived), not by colour. A dashed
            outline is not available: Chip's edge is a ring, and rings are
            box-shadows, which CSS cannot dash. */}
        {matchChip === 'repetition_source' && (
          <Chip
            title="Source of repetitions — confirming this auto-propagates to identical-source segments"
            data-testid="repetition-source-badge"
            className="font-semibold"
          >
            →R
          </Chip>
        )}
        {matchChip === 'repetition' && (
          <Chip
            title="Repetition — same source appears earlier in this file"
            data-testid="repetition-badge"
            className="font-semibold"
          >
            ⇢R
          </Chip>
        )}
        {matchChip === 'tm' && (
          <Chip
            color={
              translation!.tm_match_percent != null && translation!.tm_match_percent < 100
                ? 'yellow'
                : 'green'
            }
            title={
              translation!.tm_match_percent === 101
                ? '101% — source and both surrounding segments match'
                : translation!.tm_match_percent != null && translation!.tm_match_percent < 100
                  ? 'Fuzzy TM match'
                  : 'Exact (100%) TM match'
            }
          >
            {translation!.tm_match_percent != null
              ? `${translation!.tm_match_percent}%`
              : '100%'}
          </Chip>
        )}
        {matchChip === 'ape' && (
          <Chip title="Machine translated, post-edited by APE">APE</Chip>
        )}
        {matchChip === 'mt' && <Chip title="Machine translated">MT</Chip>}
        {matchChip === 'file' && <Chip title="Imported from bilingual file">File</Chip>}
      </div>
      <div className="px-2 py-3 flex flex-wrap items-center gap-1">
        {/* Empty for untranslated / pre_translated (default states) — the
            Status column only flags progress: Draft / Auto-approved /
            Approved. Provenance for pretranslated rows shows in Match.
            "Confirmed by assistant" stacks under Approved; the column is as
            wide as its widest chip (lib/segment-columns). */}
        {showStatusChip(status) && (
          <Chip color={STATUS_CHIP_COLORS[status]}>{statusLabel(status)}</Chip>
        )}
        {isAssistantConfirmed(translation) && (
          <Chip color="grey">{ASSISTANT_CONFIRMED_LABEL}</Chip>
        )}
        {/* Grey: it is history, the chip above is the state of this step. */}
        {autoApprovedBeforeStep && (
          <Tooltip>
            <TooltipTrigger render={<span className="inline-flex" tabIndex={0} />}>
              <Chip color="grey" data-testid="previous-step-chip">{statusLabel('auto_approved')}</Chip>
            </TooltipTrigger>
            <TooltipContent side="top">{PREVIOUS_AUTO_APPROVED_TOOLTIP}</TooltipContent>
          </Tooltip>
        )}
      </div>
      <div className={hasExtras ? 'px-2 py-3 text-xs text-gray-500 dark:text-gray-400 flex items-center' : undefined}>
        {/* Everything that is not the match or the status, in one group that
            wraps to a second line when it runs out of room. */}
        <div className="flex-1 min-w-0 flex flex-wrap items-center gap-1">
        {isLocked && <span title="Locked">🔒</span>}
        {/* LLM QE+APE score chip (a segment can be MT *and* scored). */}
        {qe && (
          <Chip color={qe.color} data-testid="qe-note-badge" title={qe.title}>
            {qe.label}
          </Chip>
        )}
        <QASummaryDot issues={qaIssues} />
        {/* Comment indicator — non-interactive: the row's own click already
            activates the segment, which loads the sidebar's Comments section. */}
        {commentCount > 0 && (
          <Tooltip>
            <TooltipTrigger>
              <Chip
                // Same colour as the Approved status chip — read from the
                // map rather than hard-coded, so a theme change to one
                // moves the other with it.
                color={STATUS_CHIP_COLORS.approved}
                icon={<MessageSquare className="fill-current" />}
                data-testid="segment-comment-indicator"
                data-comment-count={commentCount}
                className="gap-0.5 px-1 tabular-nums"
              >
                {commentCount}
              </Chip>
            </TooltipTrigger>
            <TooltipContent side="top">
              {commentCount} comment{commentCount === 1 ? '' : 's'} — open the segment to read them
            </TooltipContent>
          </Tooltip>
        )}
        </div>
      </div>

      {translation && (
        <ConfirmDialog
          open={clearConfirmOpen}
          onOpenChange={setClearConfirmOpen}
          title="Clear translation?"
          description="This reverts the segment to untranslated and removes any MT, TM match, or human edit. Translation memory entries are not affected."
          actionLabel="Clear"
          onConfirm={() => clearMut.mutate(translation.id, { onSuccess: ({ translation: cleared }) => loadVersion(cleared) })}
        />
      )}
    </div>
  )
}, segmentRowEqual)
