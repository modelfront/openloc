import { useState, useRef, useEffect, useEffectEvent, useCallback, useMemo, useSyncExternalStore, memo, type MouseEvent as ReactMouseEvent } from 'react'
import { useLatestRef } from '@/hooks/useLatestRef'
import { cn } from '@/lib/utils'
import { useOnChange } from '@/hooks/useOnChange'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  updateTranslationHuman,
  saveTranslationDraft,
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
import { searchHighlightRegex } from '../lib/text-search'
import { qaIssuesEqual, type QaFinding } from '../lib/qa-findings'
import QASummaryDot from './QASummaryDot'
import { toast } from 'sonner'
import type { EditorId } from '../lib/editor-scope'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext, invalidateQAReports, invalidateSegmentSaveContext, applyTMUpdatesAfterSave } from '../lib/query-invalidation'
import { applySegmentSaveSuccess, patchPropagatedSiblings } from '../lib/segment-save'
import { deriveDisplayStatus, deriveMatchChip } from '../lib/segment-status'
import { useInFlightGuard } from '../hooks/useInFlightGuard'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { trackPropagation, reconcilePropagationBaseline } from '../lib/propagation-guard'
import { smartReplace, getTermSelection } from '../lib/term-insert'
import { plainOffsetToPm } from '../lib/pm-offset'
import SegmentEditor, { type SegmentEditorHandle } from './SegmentEditor'
import type { SpellFixActions } from '../lib/spellcheck-scope'
import SegmentEditorToolbar from './SegmentEditorToolbar'
import MissingTagsBar from './MissingTagsBar'
import { getMissingTags, type MissingTag } from '../lib/missing-tags'
import type { Placeable } from '../lib/placeables'
import { useSourceInserts } from '../hooks/useSourceInserts'
import { flattenRichText } from '../lib/xliff-tiptap'
import { MessageSquare } from 'lucide-react'
import { Chip } from '@/components/ui/chip'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { SafeHTML } from './SafeHTML'
import ConflictBanner from './ConflictBanner'
import { useSegmentEditingTimer } from '../hooks/useSegmentEditingTimer'
import { useSourceCaret } from '../hooks/useSourceCaret'
import { useSourceSelection } from '../hooks/useSourceSelection'
import { altKey } from '../lib/platform'
import type { LineCaret } from '../lib/line-caret'
import { isLineCaret, type EntryCaret, type TextCaret } from '../lib/entry-caret'
import { textCaretAt } from '../lib/source-caret'
import { SegmentRowAction, SegmentRowActionBar } from './SegmentRowAction'

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
  plainTargetText,
  termLookupCache,
  subscribeTermLookups,
  getTermLookupVersion,
  hasXliffTags,
  hasHTMLFormatting,
  STATUS_CHIP_COLORS,
  qeChipInfo,
  tmMatchChipInfo,
  modKey,
  SEGMENT_PREVIEW_CLAMP,
} from './SegmentRow.shared'
import { PREVIOUS_AUTO_APPROVED_TOOLTIP } from './step-status-labels'
import { SegmentStatusIcon, LockIcon, statusIconFor } from './SegmentStatusIcon'

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

const REPETITION_SOURCE_TITLE = 'Source of repetitions — confirming this auto-propagates to identical-source segments'

// XLIFF tag wrappers, dropped when the stored text is compared with the
// editor's plain text for dirty detection.
const XLIFF_WRAPPER_RE = /<(bpt|ept|ph|it|x|g|hi|sub)(\s[^>]*)?>(.+?)<\/\1>|<(bpt|ept|ph|it|x|g|hi|sub)(\s[^>]*)?\/>/g
const withoutXliffTags = (text: string) => (hasXliffTags(text) ? text.replace(XLIFF_WRAPPER_RE, '') : text)

// The parts of a row version the editor loads.
type RowVersion = { active_text?: string | null; active_rich_text?: string | null; updated_at?: string | null }

/** The open row's handlers, for CATEditor's window-level shortcuts and the
 *  sidebar's inserts. A handler that writes is undefined when the row is
 *  read-only. */
export interface SegmentRowHandle {
  selectAndReplace: (src: string, tgt: string, mt?: string) => void
  selectTerm: (src: string, tgt: string, mt?: string) => void
  /** Replaces the target. With an insert token (a Resources-panel insert)
   *  it saves the draft at once, recording where the text came from; with
   *  `approve` it then confirms the segment, staying on it. */
  replaceAll?: (text: string, insertToken?: string, approve?: boolean) => void
  confirm?: () => void
  copySource?: () => void
  clear?: () => void
  split?: () => void
  joinNext?: () => void
  /** The text selected in the source (the last selection there) and in the
   *  translation, for the term quick-add. Empty when nothing is selected. */
  getTermSelection?: () => { source: string; target: string }
  /** Forgets the remembered source selection and removes its mark, once the
   *  quick-add dialog closes. */
  clearSourceSelection?: () => void
  /** Tab/Shift+Tab or Down/Up out of a read-only row, whose editor takes no
   *  keys: to the next or previous segment, by line keeping the caret's place
   *  across. */
  leaveReadOnly?: (dir: 'next' | 'prev', byLine: boolean) => void
}

export interface SegmentRowProps {
  segment: Segment
  translation?: Translation
  /** The segment number the grid shows ("3", or "3.1" for a piece of a split). */
  number: string
  jobId: number
  fileId: number
  /** The editor the row sits in, when it is a multi-file editor
   *  (lib/editor-scope): a confirm propagates across its open files. */
  editorId?: EditorId
  projectId: number | null
  targetLanguage: string
  sourceLanguage: string
  isActive: boolean
  /** The open row sits right under the sticky column header, which would cover
   *  action bars on its boxes' top border: they hang below the boxes instead. */
  actionsBelow?: boolean
  /** Opens the row; `caret` is where a click on its translation put the caret. */
  onActivate: (caret?: TextCaret) => void
  /** `caret`: where the next or previous segment's caret goes, when an arrow
   *  key leaves this one from its last or first line. */
  onNavigate: (dir: 'next' | 'prev', caret?: LineCaret) => void
  /** Where the open editor's caret starts: where an arrow key from the
   *  segment above or below, or a click on the translation, put it. */
  entryCaret?: EntryCaret
  onDeactivate: () => void
  dirtyRef: React.MutableRefObject<boolean>
  onTermsLoaded?: (terms: TermLookupResult[]) => void
  onEditTextChange?: (text: string) => void
  insertRef?: React.MutableRefObject<SegmentRowHandle>
  /** Splits the row at an offset in its plain source, with the version its
   *  draft save left. Absent when the row may not be split. */
  onSplit?: (offset: number, expectedUpdatedAt: string) => void
  /** Joins the row with the next one, a piece of the same split segment.
   *  Absent when there is none or the row may not be joined. */
  onJoinNext?: (expectedUpdatedAt: string) => void
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
  // Defaults true; gates the per-segment Clear button: the task is at or before
  // the workflow's first human or hybrid step.
  inClearWindow?: boolean
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
  /** The Track changes view is on. */
  trackChanges?: boolean
  /** Track changes: the text this segment held when the previous workflow step
   *  ended. The inactive target marks what changed since; null or undefined
   *  marks nothing. The active row never shows marks. */
  trackChangesFrom?: string | null
  /** The source's placeables (the editor rows endpoint), underlined in the
   *  source; the open row offers the missing ones for insertion. */
  placeables?: readonly Placeable[]
  /** The file's strings are plain text ("Strings contain HTML" off): a save
   *  sends the editor's text, never its HTML. */
  plainText?: boolean
  /** A TM or MT run is going on the job: an empty target says so, since text
   *  may still arrive. */
  pretranslating?: boolean
}

export function segmentRowEqual(prev: SegmentRowProps, next: SegmentRowProps): boolean {
  // Always re-render the active row (it has internal editor state)
  if (prev.isActive || next.isActive) return false

  // For inactive rows, only re-render when data changes
  return (
    prev.segment.id === next.segment.id &&
    prev.number === next.number &&
    prev.actionsBelow === next.actionsBelow &&
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
    !prev.pretranslating === !next.pretranslating &&
    prev.refTerms === next.refTerms &&
    prev.spellCheck === next.spellCheck &&
    prev.findHighlightTerm === next.findHighlightTerm &&
    prev.tmDiffReference === next.tmDiffReference &&
    qaIssuesEqual(prev.qaIssues, next.qaIssues) &&
    prev.inClearWindow === next.inClearWindow &&
    prev.isRepetition === next.isRepetition &&
    prev.isRepetitionSource === next.isRepetitionSource &&
    prev.commentCount === next.commentCount &&
    prev.isSelected === next.isSelected &&
    (prev.heldByName ?? null) === (next.heldByName ?? null) &&
    !prev.onSelectClick === !next.onSelectClick &&
    !prev.onSplit === !next.onSplit &&
    !prev.onJoinNext === !next.onJoinNext &&
    prev.autoApprovedBeforeStep === next.autoApprovedBeforeStep &&
    !prev.trackChanges === !next.trackChanges &&
    (prev.trackChangesFrom ?? null) === (next.trackChangesFrom ?? null) &&
    prev.placeables === next.placeables
  )
}

// One empty list for every row without missing tags, so callbacks keyed on it
// keep their identity.
const NO_MISSING_TAGS: MissingTag[] = []
const NO_TERMS: TermLookupResult[] = []
const NO_SOURCE_PLACEABLES: readonly Placeable[] = []

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
  number,
  jobId,
  fileId,
  editorId,
  projectId,
  targetLanguage,
  sourceLanguage,
  isActive,
  actionsBelow = false,
  onActivate,
  onNavigate,
  entryCaret,
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
  inClearWindow = true,
  isRepetition = false,
  isRepetitionSource = false,
  commentCount = 0,
  isSelected = false,
  onSelectClick,
  heldByName = null,
  autoApprovedBeforeStep,
  predictedBeforeStep,
  plainText = false,
  pretranslating = false,
  trackChanges = false,
  trackChangesFrom = null,
  placeables = NO_SOURCE_PLACEABLES,
  onSplit,
  onJoinNext,
}: SegmentRowProps) {
  // Source column mirrors the source document, target the deliverable — so a
  // reference reads "Wet" on the left and "Feucht" on the right.
  const resolveRefSource = useMemo(
    () => (refTerms ? refResolver(refTerms, 'source') : undefined), [refTerms])
  const resolveRefTarget = useMemo(
    () => (refTerms ? refResolver(refTerms, 'target') : undefined), [refTerms])
  // Track changes: the rich text an inactive row's chips render from, and
  // whether the row is drawn by SegmentText. A row with bold or italic
  // formatting keeps its formatted rendering and shows no marks, closed or
  // open; with ¶ on a closed row shows its tags as text and is marked, but the
  // editor still loads them as formatting, so the open row is not. While the
  // view is on, Find hits take the quiet search tint on every drawn row, so
  // they never read as added words.
  const activeText = translation?.active_text ?? ''
  const trackRich = plainText ? null
    : translation?.active_rich_text && hasXliffTags(translation.active_rich_text) ? translation.active_rich_text
    : hasXliffTags(activeText) ? activeText : null
  const unformatted = plainText || trackRich !== null || !hasHTMLFormatting(activeText)
  const trackable = trackChanges && activeText !== ''
  const trackView = trackable && !isActive && (unformatted || showWhitespace)
  const showTrackChanges = trackView && trackChangesFrom != null && trackChangesFrom !== activeText
  const trackSearch = useMemo(
    () => (findHighlightTerm ? searchHighlightRegex(findHighlightTerm, { searchSource: false, searchTarget: true, caseSensitive: false, useRegex: false }) : null),
    [findHighlightTerm])

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
  // The closed row's text boxes, for where a click lands (activateAt).
  const sourceBoxRef = useRef<HTMLDivElement>(null)
  const targetBoxRef = useRef<HTMLDivElement>(null)
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

  // Optimistic locking: the updated_at we saw when the user
  // started editing this row. Sent with every save — if the server's current
  // updated_at differs, another user has edited the row and we get 409.
  // Updated on every successful save so back-to-back saves by the same user
  // keep working. Cleared when the row is deactivated.
  const baselineUpdatedAtRef = useRef<string | null>(null)
  // The shared post-save sync of every save path (confirm, draft, "Use mine")
  // and of both Clear paths, with the cleared row.
  const syncSaved = useCallback((saved: Translation | undefined) => applySegmentSaveSuccess({
    queryClient, jobId, saved, prior: translation,
    wordCount: segment.word_count, charCount: segment.source_text.length,
    baselineUpdatedAtRef, dirtyRef, setOriginalText,
  }), [queryClient, jobId, translation, segment.word_count, segment.source_text.length, dirtyRef])
  const canSplit = isActive && !effectiveReadOnly && !!onSplit
  const {
    caretRef: sourceCaretRef, hasCaret: sourceHasCaret, fieldProps: sourceFieldProps, fieldKey: sourceFieldKey,
  } = useSourceCaret(canSplit)
  const {
    fieldRef: sourceSelectionRef, getText: getSourceSelection, clear: clearSourceSelection,
  } = useSourceSelection(isActive)
  // The source element carries both hooks' refs: the caret's only while
  // Split is on, the selection's whenever the row is open.
  const caretFieldRef = sourceFieldProps.ref
  const attachSourceField = useCallback((el: HTMLDivElement | null) => {
    sourceSelectionRef.current = el
    return typeof caretFieldRef === 'function' ? caretFieldRef(el) : undefined
  }, [caretFieldRef, sourceSelectionRef])

  // Unlock from the padlock in the last column. An unlock can leave the row
  // approved, so counts and the progress bar refresh with it.
  const unlockMut = useMutationWithToast({
    mutationFn: (translationId: number) => unlockTranslation(fileId, translationId),
    successMessage: () => null,
    errorMessage: 'Unlock failed',
    onSuccess: () => {
      if (projectId) invalidateJobContext(queryClient, projectId, jobId)
      else queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
    },
  })

  const [clearConfirmOpen, setClearConfirmOpen] = useState(false)
  const clearMut = useClearSegment({ fileId, jobId, projectId: projectId ?? 0 })
  // Clear shows on every row the user may clear, translated or not. A saved
  // translation is cleared on the server after a confirm; with nothing saved,
  // Clear only empties the editor.
  const canClear = inClearWindow && !!translation && !effectiveReadOnly
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

  // `vars` is the text a panel insert put in the editor, serialized when the
  // insert was saved; editText in this closure still holds the previous text.
  const saveMut = useMutation({
    mutationFn: (vars?: { text: string; richText: string | undefined }) => guardSave(async () => {
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
      await reconcilePropagationBaseline(editorId ?? jobId, segment.source_text, translation?.id, baselineUpdatedAtRef)
      const delta = editingTimer.consume()
      const { text, richText } = vars ?? serializeEditor(editorRef.current, editText, plainText)
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
      syncSaved(saved)
      // Invalidate the TM browser caches that just received the new entry +
      // show the throttled "TM updated" toast. One helper, shared with the
      // use-mine conflict-resolution path so the two can't drift.
      applyTMUpdatesAfterSave(queryClient, updatedTmIds, toast.success)
      if (taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
      return saved
    }),
    onSuccess: (saved, vars) => {
      // The 4 invariants (baseline / cache / originalText / dirty) are
      // applied inside mutationFn BEFORE the sentinel resolves — see comment
      // there. Side effects below don't affect this row's baseline state and
      // can run after the sentinel resolves without exposing queued callers
      // to stale data.
      if (projectId) invalidateSegmentSaveContext(queryClient, projectId, jobId)
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
          vars?.text ?? editText,
          translation.id,
          saved?.active_rich_text ?? undefined,
          editorId,
        )
          .then(r => {
            if (r.taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
            if (r.propagated > 0) {
              // Patch the cache for each sibling synchronously BEFORE
              // invalidation refetches, so a sibling re-activated within the
              // refetch window doesn't capture a stale `baselineUpdatedAtRef`
              // and false-409 on its next save.
              patchPropagatedSiblings({ queryClient, jobId, siblings: r.siblings, wordCount: segment.word_count, charCount: segment.source_text.length })
              toast.success(`Propagated to ${r.propagated} segment${r.propagated > 1 ? 's' : ''}`)
              if (projectId) invalidateSegmentSaveContext(queryClient, projectId, jobId)
              // The siblings' stored QA findings describe their old text.
              invalidateQAReports(queryClient, jobId)
            }
            warnHeldSkipped(r.held, 'repetition')
            if (r.not_editable) {
              toast.warning(`${r.not_editable} repetition${r.not_editable > 1 ? 's' : ''} in files you cannot edit now ${r.not_editable > 1 ? 'were' : 'was'} left as ${r.not_editable > 1 ? 'they were' : 'it was'}.`)
            }
            return new Map<number, string>(
              r.siblings.filter(s => s.updated_at).map(s => [s.id, s.updated_at!]),
            )
          })
          .catch((err) => { console.warn('QA/propagation failed:', err); return new Map<number, string>() })
        trackPropagation(editorId ?? jobId, segment.source_text, propagated)
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
  // Resolves true when the text was written.
  // The parent passes an inline arrow, new every render: read it through a ref
  // so saveDraft keeps its identity and the autosave timer below isn't reset.
  const onQARefreshRef = useLatestRef(onQARefresh)
  const saveDraft = useCallback(async (insert?: { text: string; insertToken: string }): Promise<boolean> => {
    if (!translation || effectiveReadOnly) return false
    return guardSave(async () => {
      // Re-check INSIDE the guarded section: after awaiting any prior
      // in-flight save, the dirty/baseline state may have changed, and a PUT
      // /draft with no pending edit 400s.
      if (!dirtyRef.current && !insert) return false
      if (emptiedRef.current && !insert) {
        const { translation: cleared } = await clearMut.mutateAsync(translation.id)
        loadVersion(cleared)
        syncSaved(cleared)
        emptiedRef.current = false
        dirtyRef.current = false
        return true
      }
      // Same in-flight propagation reconcile as saveMut — an autosave/flush on
      // a propagated sibling must not 409 against the user's own propagation.
      await reconcilePropagationBaseline(editorId ?? jobId, segment.source_text, translation.id, baselineUpdatedAtRef)
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
        // Post-save sync (advance baseline, patch cache and leverage bar, sync
        // originalText, clear dirty) — single helper, used by every save path and Clear.
        syncSaved(saved)
        // A displayed-status change (an empty segment's first draft, an
        // approval taken away) moves the job's counts, which gate the toolbar
        // Clear. An empty row is `needs_review` on the server too, so the raw
        // status alone misses the first draft.
        if (saved && deriveDisplayStatus(saved) !== deriveDisplayStatus(translation)) {
          void queryClient.invalidateQueries({ queryKey: queryKeys.job.detail(jobId) })
          if (projectId) void queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
        }
        if (taskReopened) handleTaskReopened(queryClient, projectId ?? 0, jobId)
        // Re-run QA for THIS segment after every autosave.
        // POST recomputes + persists so the badges track the draft as the user
        // types; scoped to the edited segment, not the whole job.
        const refreshQA = onQARefreshRef.current
        if (refreshQA) {
          runTranslationQA(translation.id).then(issues => refreshQA(translation.id, issues)).catch((err) => console.warn('QA refresh failed:', err))
        }
        return true
      } catch (err) {
        if (isTaskLockedError(err)) { handleTaskLocked(queryClient, projectId ?? 0, jobId, err); return false }
        if (isSegmentHeldError(err)) { toast.error((err as Error).message); return false }
        if (isSuggestionGoneError(err)) {
          // The text stays dirty, so the autosave keeps it as an ordinary edit.
          toast.error('That match is no longer in the translation memory, so it is kept as your own edit.')
          return false
        }
        if (isSaveConflictError(err)) {
          setConflictPayload(getConflictPayload(err))
          return false
        }
        throw err
      }
    })
  }, [translation, effectiveReadOnly, editText, fileId, projectId, dirtyRef, queryClient, jobId, editorId, guardSave, clearMut, loadVersion, onQARefreshRef, segment.source_text, plainText, syncSaved])

  // Split and join replace this row, so its draft is saved first and the
  // write carries the version that save left.
  const splitAtCaret = useCallback(async () => {
    if (!onSplit) return
    const offset = sourceCaretRef.current
    if (offset === null) {
      toast.info('Click in the source text where it should split.')
      return
    }
    await saveDraft()
    const version = baselineUpdatedAtRef.current ?? translation?.updated_at
    if (version) onSplit(offset, version)
  }, [onSplit, saveDraft, sourceCaretRef, translation?.updated_at])
  const joinWithNext = useCallback(async () => {
    if (!onJoinNext) return
    await saveDraft()
    const version = baselineUpdatedAtRef.current ?? translation?.updated_at
    if (version) onJoinNext(version)
  }, [onJoinNext, saveDraft, translation?.updated_at])

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
    flushDraftRef.current = async () => { await saveDraft() }
    return () => { if (flushDraftRef) flushDraftRef.current = async () => {} }
  }, [flushDraftRef, isActive, saveDraft])

  // Look up terminology when segment becomes active (cached by sourceText),
  // and again when a term added from the editor drops the cache.
  const termLookupVersion = useSyncExternalStore(subscribeTermLookups, getTermLookupVersion)
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
  }, [isActive, projectId, sourceLanguage, targetLanguage, segment.source_text, termLookupVersion]) // eslint-disable-line react-hooks/exhaustive-deps

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

  // A confirm of this row is saving. Cmd+Enter pressed again meanwhile is
  // dropped: queued, each press would save the row once more (and, on a
  // repetition source, wait for its own previous propagation) before the
  // editor moves on. A ref, not saveMut.isPending: key repeat outruns a render.
  // Settled through this save's own promise: mutate's per-call callbacks run
  // only for the latest call, so an insert-and-approve save started meanwhile
  // would drop them and leave the ref set.
  const confirmingRef = useRef(false)
  // A save that lands after the user left the row (Cmd+G, a find result) must
  // not pull them back to the row after it.
  const isActiveRef = useLatestRef(isActive)
  const confirmAndAdvance = useCallback(() => {
    if (confirmingRef.current) return
    // The saved row is already in the translations cache (applySegmentSaveSuccess).
    const afterSave = () => { if (isActiveRef.current) onNavigate('next') }
    const confirm = () => {
      confirmingRef.current = true
      // A failure is shown by saveMut's onError.
      saveMut.mutateAsync(undefined)
        .then(afterSave, () => {})
        .finally(() => { confirmingRef.current = false })
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
      confirm()
    } else if (editText.trim()) {
      // Always save on Cmd+Enter — confirms/approves the segment even if unedited
      confirm()
    } else {
      onNavigate('next')
    }
  }, [editText, segment.source_text, segment.source_rich_text, plainText, saveMut, onNavigate, isActiveRef])

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
  // editor with each missing tag as a clickable chip; F8 / Alt+F8 insert
  // them together with the missing placeables and term targets, in source
  // order. Recomputed on every keystroke (editText) so the bar disappears the
  // moment all tags are back.
  let missingTags = NO_MISSING_TAGS
  if (isActive && segment.source_rich_text && hasXliffTags(segment.source_rich_text)) {
    const { text, richText } = serializeEditor(editorHandle, editText, plainText)
    missingTags = getMissingTags(segment.source_rich_text, richText || text)
  }

  const hasSourceTags = !!segment.source_rich_text && hasXliffTags(segment.source_rich_text)
  const { insertTag, insertText, insertNext, insertAll } = useSourceInserts({
    sourceRich: hasSourceTags ? segment.source_rich_text : null,
    flatSource: isActive && hasSourceTags ? flattenRichText(segment.source_rich_text!) : segment.source_text,
    placeables, terms: matchingTerms, missingTags, isActive, readOnly: effectiveReadOnly, editorRef,
    target: isActive ? plainTargetText(editorHandle, editText) : '',
  })

  // A click on a placeable in the open row's source inserts it at the
  // target's cursor (renderRanges marks it with data-placeable). Only the first
  // click of a double click inserts, and a drag that selects text inserts
  // nothing. The Split caret goes anywhere but inside a placeable, which is
  // never cut.
  const insertClickedPlaceable = (e: ReactMouseEvent) => {
    if (e.detail !== 1 || !(document.getSelection()?.isCollapsed ?? true)) return
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-placeable]')
    if (!el?.dataset.placeable) return
    e.preventDefault()
    insertText(el.dataset.placeable)
  }

  // Imperative handlers for CATEditor's window-level shortcuts (Mod+Enter,
  // Mod+Shift+S, Ctrl+1..5) and the sidebar's inserts. On a read-only or locked
  // row every handler that writes is undefined or a no-op (parity with TipTap's
  // `if (!props.readOnly)` keymap checks); selectTerm, the hover highlight,
  // stays. No deps: re-runs every render so the ref holds the latest closures.
  // The cleanup hands the ref back empty when the row stops being the open one
  // or unmounts, so a shortcut never reaches a row the user has left.
  useEffect(() => {
    if (!insertRef) return
    insertRef.current = {
      selectAndReplace,
      selectTerm: selectTermInTextarea,
      replaceAll: effectiveReadOnly ? undefined : (text: string, insertToken?: string, approve?: boolean) => {
        setEditText(text)
        editorRef.current?.setText(text)
        if (!insertToken) return
        // The editor's plain text, as typing sends it; the tags travel in the
        // rich text the save reads from the editor. The draft records the
        // match first, so a confirm the QA gate refuses leaves it a draft
        // that still names its TM match. Only the match itself, still in an
        // open editor, is approved: a later insert or typing has its own
        // save, and a closed row has no editor to read the tags from.
        const inserted = editorRef.current?.getText() ?? text
        saveDraft({ text: inserted, insertToken })
          .then(saved => {
            const editor = editorRef.current
            if (!saved || !approve || !editor || editor.getText() !== inserted) return
            saveMut.mutate(serializeEditor(editor, inserted, plainText))
          })
          .catch(() => toast.error('Draft save failed'))
      },
      confirm: effectiveReadOnly ? undefined : confirmAndAdvance,
      copySource: effectiveReadOnly ? undefined : copySourceToTarget,
      clear: canClear ? requestClear : undefined,
      split: !effectiveReadOnly && onSplit ? () => void splitAtCaret() : undefined,
      joinNext: !effectiveReadOnly && onJoinNext ? () => void joinWithNext() : undefined,
      getTermSelection: () => {
        const { start, end } = cursorPosRef.current
        const text = editorRef.current?.getText() ?? ''
        return {
          source: getSourceSelection(),
          target: text.slice(Math.min(start, end), Math.max(start, end)).trim(),
        }
      },
      clearSourceSelection,
      leaveReadOnly: effectiveReadOnly
        ? (dir, byLine) => onNavigate(dir, byLine ? { line: dir === 'next' ? 'first' : 'last', x: entryCaret && isLineCaret(entryCaret) ? entryCaret.x : 0 } : undefined)
        : undefined,
    }
    return () => { insertRef.current = { selectAndReplace: () => {}, selectTerm: () => {} } }
  })

  const status = deriveDisplayStatus(translation, { predictedBeforeStep })
  const statusIcon = statusIconFor(status, translation)
  const originVal = translation?.origin?.valid ? translation.origin.translation_origin : ''
  // Exactly one provenance chip per row (repetition > TM% > APE > MT > File).
  const matchChip = deriveMatchChip({ isRepetitionSource, isRepetition, origin: originVal })
  const tmChip = tmMatchChipInfo(translation?.tm_match_percent)
  const qe = qeChipInfo(translation)
  // The extras column can be 0px wide (no row in the job has extras); an empty
  // cell then drops its padding so it doesn't spill past the row.
  const hasExtras = isLocked || qe != null || qaIssues.length > 0 || commentCount > 0

  // A click opens the row with the caret where it landed in the translation,
  // in one click. A selection dragged in the source stays, to be copied.
  const activateAt = (e: React.MouseEvent) => {
    const sel = document.getSelection()
    const source = sourceBoxRef.current
    if (sel && !sel.isCollapsed && source?.contains(sel.anchorNode) && source.contains(sel.focusNode)) return
    const target = targetBoxRef.current
    onActivate((activeText !== '' && target && textCaretAt(target, e.clientX, e.clientY)) || undefined)
  }

  return (
    <div
      className={`${SEGMENT_GRID_COLS} border-b border-gray-100 dark:border-gray-800 cursor-pointer transition-colors duration-150 ${isActive ? 'bg-blue-50 dark:bg-blue-950/30 shadow-[inset_1px_0_0_var(--color-blue-500)] dark:shadow-[inset_1px_0_0_var(--color-blue-400)]' : isSelected ? 'bg-primary/5 hover:bg-primary/10' : 'hover:bg-muted/40'}`}
      data-selected={isSelected || undefined}
      data-active={isActive || undefined}
      onClick={!isActive ? activateAt : undefined}
    >
      {onSelectClick && translation ? (
        // The whole cell is the checkbox's click target. Shift+click selects a
        // range; select-none + the mousedown guard keep Shift from also
        // selecting text.
        <div
          className="segment-cell px-2 text-xs text-gray-400 dark:text-gray-500 select-none"
          onMouseDown={e => { if (e.shiftKey) e.preventDefault() }}
          onClick={e => {
            e.stopPropagation()
            onSelectClick(translation.id, e.shiftKey)
          }}
          data-testid="segment-select-cell"
        >
          <div className="segment-number flex items-center gap-1.5 leading-5">
            <input
              type="checkbox"
              checked={!!isSelected}
              // The cell handles the click (bubbled from here too); keyboard
              // Space also arrives as a click.
              onChange={() => {}}
              className="size-3.5 shrink-0 cursor-pointer accent-primary"
              aria-label={`Select segment ${number}`}
              data-testid={`segment-select-${number}`}
            />
            {number}
            {heldByName && <SegmentHeldIcon name={heldByName} />}
          </div>
        </div>
      ) : (
        <div className="segment-cell px-3 text-xs text-gray-400 dark:text-gray-500">
          <div className="segment-number flex items-center gap-1 leading-5">
            {number}
            {heldByName && <SegmentHeldIcon name={heldByName} />}
          </div>
        </div>
      )}
      {/* min-w-0 lets the 1fr track stay at its fair share instead of growing to
          fit a long unbreakable token; break-words (inherited overflow-wrap)
          then wraps that token inside the cell. Without both, a token with no
          spaces (e.g. "Copyright_2026_Deloitte_...") expands the column and
          misaligns the row. */}
      <div className={`segment-cell px-3 text-sm min-w-0 break-words ${isActive ? 'text-gray-900 dark:text-gray-100' : 'text-gray-600 dark:text-gray-400'}`}>
        {/*
          Open or closed, the source sits in a box with an invisible border
          and no fill, like the translation (index.css
          `.segment-editor .ProseMirror`, `.segment-target-box`). Geometry comes from
          .segment-source-box / .segment-editor-frame (index.css), so the two
          columns line up and activating the row doesn't move the text.
        */}
        <div
          ref={sourceBoxRef}
          className={`segment-source-box relative border border-transparent rounded ${isActive ? '' : 'cursor-text'}`}
          data-testid={isActive ? 'active-source-box' : undefined}
        >
          {/* Split's mousedown keeps the caret where the user put it. */}
          {canSplit && (
            <SegmentRowActionBar placement={actionsBelow ? 'bottom' : 'top'}>
              <SegmentRowAction
                onMouseDown={e => e.preventDefault()}
                onClick={() => void splitAtCaret()}
                disabled={!sourceHasCaret}
                disabledReason="First click in the source text where you want to cut the segment."
                title={`Split at the cursor (${altKey}⇧T)`}
                data-testid="split-segment-btn"
              >✂ Split</SegmentRowAction>
            </SegmentRowActionBar>
          )}
          <div
            key={sourceFieldKey}
            className={isActive ? cn(canSplit && 'outline-none cursor-text', !effectiveReadOnly && '[&_[data-placeable]]:cursor-pointer') : SEGMENT_PREVIEW_CLAMP}
            data-testid={isActive ? 'active-source-text' : undefined}
            {...sourceFieldProps}
            ref={attachSourceField}
            onClick={isActive && !effectiveReadOnly ? insertClickedPlaceable : undefined}
          >
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
              placeables={placeables}
            />
          </div>
        </div>
      </div>
      <div className="segment-cell px-3 min-w-0 break-words" data-translation-cell>
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
                      syncSaved(saved)
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
                onInsert={insertTag}
                onInsertAll={() => { for (const t of missingTags) insertTag(t) }}
              />
            )}
            {/* segment-editor-frame (index.css): activating a row keeps its
                height and the text stays where the preview drew it. */}
            <div className="segment-editor-frame relative">
              {/* The toolbar and Clear are for an editable box only; a
                  read-only box draws no bar, rather than an empty one. */}
              {!effectiveReadOnly && (
                <SegmentRowActionBar placement={actionsBelow ? 'bottom' : 'top'} data-testid="segment-editor-actions">
                  <SegmentEditorToolbar editor={editorHandle?.getEditor() ?? null} />
                  {isActive && canClear && (
                    <SegmentRowAction
                      tone="danger"
                      onClick={requestClear}
                      disabled={clearMut.isPending}
                      title={`Clear translation (${modKey}⇧⌫)`}
                      data-testid="clear-translation-btn"
                    >🧹 Clear</SegmentRowAction>
                  )}
                </SegmentRowActionBar>
              )}
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
                trackChangesFrom={trackable && unformatted ? trackChangesFrom : null}
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
                initialCaret={entryCaret}
                onDeactivate={onDeactivate}
                onCopySource={copySourceToTarget}
                onInsertNextFromSource={insertNext}
                onInsertAllFromSource={insertAll}
                onClearTranslation={() => { if (canClear) requestClear() }}
              />
            </div>
          </div>
        ) : (
          // Drawn as the editor box that replaces it, so the click that opens
          // the row lands where the user aimed (activateAt).
          <div className="segment-editor-frame">
            <div
              ref={targetBoxRef}
              className={`segment-target-box cursor-text ${effectiveReadOnly ? 'segment-target-box-readonly' : ''}`}
              data-testid="segment-target-box"
            >
              <div className={SEGMENT_PREVIEW_CLAMP}>
                {trackView
                  ? <span data-testid={showTrackChanges ? 'track-changes' : undefined}>
                      <SegmentText
                        text={activeText}
                        richText={trackRich}
                        changesFrom={showTrackChanges ? trackChangesFrom : null}
                        search={trackSearch}
                        showWhitespace={showWhitespace}
                        resolveRef={resolveRefTarget}
                      />
                    </span>
                  : translation?.active_text
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
                  : <span className="text-gray-300 dark:text-gray-600" data-testid="empty-target">
                      {pretranslating ? 'Pre-translating…' : effectiveReadOnly ? 'Empty' : 'Click to translate'}
                    </span>
                }
              </div>
            </div>
          </div>
        )}
      </div>
      <div className="segment-cell px-2 flex items-center justify-center">
        {/* ONE provenance chip per row — deriveMatchChip picks it with
            priority repetition > TM% > APE > MT > File (mutually exclusive
            by design). A repetition source from TM keeps its percentage and
            adds the arrow: 98%→.

            Provenance is grey; only the TM percentage carries a colour,
            because only it says something about quality. Otherwise this
            column would light up on every row and stop meaning anything.

            The two repetition chips are told apart by their arrow glyph
            (→R solid = source, ⇢R dashed = derived), not by colour. A dashed
            outline is not available: Chip's edge is a ring, and rings are
            box-shadows, which CSS cannot dash. */}
        {matchChip === 'tm_repetition_source' && (
          <Chip
            color={tmChip.color}
            title={`${tmChip.title}. ${REPETITION_SOURCE_TITLE}`}
            data-testid="repetition-source-badge"
          >
            {tmChip.label}→
          </Chip>
        )}
        {matchChip === 'repetition_source' && (
          <Chip
            title={REPETITION_SOURCE_TITLE}
            data-testid="repetition-source-badge"
            className="font-semibold"
          >
            →R
          </Chip>
        )}
        {matchChip === 'repetition' && (
          <Chip
            title="Repetition — the same source appears in an earlier segment"
            data-testid="repetition-badge"
            className="font-semibold"
          >
            ⇢R
          </Chip>
        )}
        {matchChip === 'tm' && (
          <Chip color={tmChip.color} title={tmChip.title}>{tmChip.label}</Chip>
        )}
        {matchChip === 'ape' && (
          <Chip title="Machine translated, post-edited by APE">APE</Chip>
        )}
        {matchChip === 'mt' && <Chip title="Machine translated">MT</Chip>}
        {matchChip === 'file' && <Chip title="Imported from bilingual file">File</Chip>}
      </div>
      <div className="segment-cell px-2 flex flex-wrap items-center gap-1">
        {/* At most one icon (statusIconFor). The previous step's auto-approval
            shows, grey, only until this step marks the segment itself. */}
        {statusIcon ? (
          <SegmentStatusIcon kind={statusIcon.kind} label={statusIcon.label} />
        ) : autoApprovedBeforeStep && (
          <SegmentStatusIcon kind="machine_approved" label={PREVIOUS_AUTO_APPROVED_TOOLTIP} history data-testid="previous-step-status" />
        )}
      </div>
      <div className={hasExtras ? 'segment-cell px-2 text-xs text-gray-500 dark:text-gray-400 flex items-center' : undefined}>
        {/* Everything that is not the match or the status, in one group that
            wraps to a second line when it runs out of room. */}
        <div className="flex-1 min-w-0 flex flex-wrap items-center gap-1">
        {isLocked && (
          <LockIcon
            onUnlock={readOnly || !translation ? undefined : () => unlockMut.mutate(translation.id)}
            pending={unlockMut.isPending}
          />
        )}
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
                // Same colour as the Approved chip in the History section —
                // read from the map rather than hard-coded, so a theme change
                // to one moves the other with it.
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
          onConfirm={() => clearMut.mutate(translation.id, { onSuccess: ({ translation: cleared }) => { loadVersion(cleared); syncSaved(cleared) } })}
        />
      )}
    </div>
  )
}, segmentRowEqual)
