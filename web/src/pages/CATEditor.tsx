import { useState, useRef, useEffect, useLayoutEffect, useCallback, useMemo } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { ChevronLeft, FileDiff, Keyboard, Loader2Icon, Upload } from 'lucide-react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { defaultRangeExtractor, useVirtualizer, type Range } from '@tanstack/react-virtual'
import {
  completeJob,
  runQA,
  getQAReport,
  listJobSteps,
  getJobStepStatuses,
  advanceJobStep,
  confirmAllTranslations,
  rejectJobStep,
  type RejectStepInput,
  getCommentCounts,
  type EditorRow,
  type TermLookupResult,
  type QAReport,
  listJobTermBases,
} from '../api'
import { getMe } from '../api/auth'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext, invalidateProjectStatus, invalidateTMContext } from '../lib/query-invalidation'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { useStableArray } from '../hooks/useStableArray'
import { useJobPipelineActions } from '../hooks/useJobPipelineActions'
import { usePipelineEditorRefresh } from '../hooks/usePipelineEditorRefresh'
import { useJobDetail } from '../hooks/useJobDetail'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { inClearWindow, isPipelineRunning, isAIStepRunning, pipelineBadge, stoppedNote } from '../lib/project-logic'
import { canEditTask, canRerunTask, canSkipAIStep, currentTask } from '../lib/current-task'
import { handleTaskLocked, handleTaskReopened, isTaskLockedError } from '../lib/task-edit'
import { unapprovedCounts, blockedSegmentNumbers, type ApproveBlocked } from '../lib/step-completion'
import type { SegmentBulkResult } from '../lib/segment-bulk'
import type { SpellRunScope } from '../lib/spellcheck-run'
import { qaButtonLabel, qaRunToast, patchQAReport, groupFindingsByTranslation, type QaFinding, NO_QA_PROFILE_REASON } from '../lib/qa-findings'
import { DEFAULT_RUN_SCOPE } from '../lib/spellcheck-scope'
import SpellCheckDialog from '../components/SpellCheckDialog'
import ConcordanceModal from '../components/ConcordanceModal'
import { QuickAddTermDialog } from '../components/QuickAddTermDialog'
import { quickAddBlockedReason } from '../lib/term-quick-add'
import { DownloadMenu } from '../components/project/DownloadMenu'
import { ImportXLIFFAction } from '../components/project/ImportXLIFFAction'
import EditorSidebar, { type SegmentReference } from '../components/EditorSidebar'
import FindReplaceModal from '../components/FindReplaceModal'
import { SegmentSelectionBar } from '../components/SegmentSelectionBar'
import { SelectAllCheckbox } from '@/components/list/SelectAllCheckbox'
import { useSegmentSelection } from '../hooks/useSegmentSelection'
import { useSegmentHold } from '../hooks/useSegmentHold'
import { JobPipelineActions } from '../components/JobPipelineActions'
import { TaskActions, TaskDeadlineChip, TaskNotes, TaskStepChip } from '../components/TaskControls'
import { RejectStepDialog } from '../components/RejectStepDialog'
import { canRollback } from '../lib/rollback'
import CompleteStepDialog from '../components/CompleteStepDialog'
import { SegmentRow, SEGMENT_GRID_COLS, isMac, modKey, type SegmentRowHandle } from '../components/SegmentRow'
import { SegmentRowPlaceholder } from '../components/SegmentRowPlaceholder'
import { qeChipLabel } from '../lib/qe-chip'
import { extrasColumnWidth, isResizableBorder, segmentColumnMins, segmentColumnsStyle } from '../lib/segment-columns'
import { hasOverlayScrollbars } from '../lib/scrollbar'
import { autoApprovedBeforeStep } from '../components/step-status-labels'
import { useColumnBorderDrag } from '../hooks/useColumnBorderDrag'
import { ColumnResizeHandle } from '@/components/ui/column-resize-handle'
import { Progress } from '@/components/ui/progress'
import { useMediaQuery } from '../hooks/useMediaQuery'
import { useLastSelection } from '../hooks/useLastSelection'
import { useTMMatches } from '../hooks/useTMMatches'
import { usePanelResize } from '../hooks/usePanelResize'
import { usePageTitle } from '../hooks/usePageTitle'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import SegmentFilterBar from '../components/SegmentFilterBar'
import { countByDisplayStatus, filterSegments, isFilterActive, DEFAULT_FILTERS, type SegmentFilters } from '../lib/segment-filters'
import JobLeverageStatusBar, { LEVERAGE_BAR_CLEARANCE } from '../components/JobLeverageStatusBar'
import { writeStorage } from '../lib/safe-storage'
import { panelInsertApproves, panelInsertText, type PanelInsert } from '../lib/panel-insert'
import { segmentRefKeys, type RefTerms } from '../lib/ref-terms'
import { REPETITION_REPEAT, REPETITION_SOURCE, rowForNumber, segmentNumber, translatedRows } from '../lib/editor-index'
import { joinRefusal, joinsWithNext } from '../lib/segment-edit'
import type { LineCaret } from '../lib/line-caret'
import type { EntryCaret } from '../lib/entry-caret'
import { useSegmentSplitJoin } from '../hooks/useSegmentSplitJoin'
import { EDITOR_PAGE_SIZE, useEditorIndex, useEditorPages, useEditorRefs, useEditorSearch } from '../hooks/useEditorRows'
import { useSpellFixes } from '../hooks/useSpellFixes'
import { tableHeaderRowClass } from '@/components/list/list-row'

/** Width the Resources panel opens at on a click, and its starting width. */
const SIDEBAR_CLICK_WIDTH = 320

/** A row without QA findings. One shared array keeps SegmentRow's memo quiet. */
const NO_ISSUES: QaFinding[] = []

/** Rows within which a programmatic scroll is smooth; further ones jump. */
const SMOOTH_SCROLL_ROWS = 50

export default function CATEditor() {
  const { projectId, jobId } = useParams<{ projectId: string; jobId: string }>()
  const pid = Number(projectId)
  const jid = Number(jobId)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [activeSegmentIdx, setActiveSegmentIdx] = useState<number | null>(null)
  // Where the open row's caret starts when an arrow key entered it from the
  // row above or below, or a click on its translation opened it; every other
  // switch clears it.
  const [entryCaret, setEntryCaret] = useState<{ idx: number; caret: EntryCaret } | null>(null)
  const activeSegmentIdxRef = useRef<number | null>(null)
  activeSegmentIdxRef.current = activeSegmentIdx
  // The active segment's tagged source, for re-anchoring TM match tags on
  // insert. Same ref pattern as above: the keyboard
  // insert path runs inside a keydown handler and must not close over state.
  const activeSourceRichRef = useRef<string>('')
  const activeRowDirtyRef = useRef(false) // tracks if active segment has unsaved edits

  // Step completion / rejection dialog state
  const [completeDialogOpen, setCompleteDialogOpen] = useState(false)
  // Segments "Approve all and complete" could not approve; the step stays
  // open until they are approved.
  const [approveBlocked, setApproveBlocked] = useState<ApproveBlocked | null>(null)
  const [rejectDialogOpen, setRejectDialogOpen] = useState(false)
  const [activeTerms, setActiveTerms] = useState<TermLookupResult[]>([])
  const [activeEditText, setActiveEditText] = useState('')
  // The deactivating row can't clear these itself — it only
  // receives `onTermsLoaded`/`onEditTextChange` WHILE it's the active row (see
  // the row props below), so its deactivate cleanup sees them `undefined` and
  // its `?.([])` is a no-op. Clear at the owning layer when nothing is active.
  useEffect(() => {
    if (activeSegmentIdx === null) {
      setActiveTerms([])
      setActiveEditText('')
    }
  }, [activeSegmentIdx])
  const [sidebarOpen, setSidebarOpen] = useState(() => { try { return localStorage.getItem('cat-sidebar-open') !== 'false' } catch { return true } })
  const tmMatchesRef = useRef<import('../api').TMMatch[]>([])

  // Ref populated by the active SegmentRow with its imperative handlers.
  // CATEditor's window-level keyboard shortcuts (Ctrl+1..5, Mod+Enter,
  // Mod+Shift+S) route into these handlers so shortcuts work regardless of
  // whether focus is currently inside the ProseMirror editor or on a
  // sidebar/panel button. The editor's own TipTap keymap still
  // handles these when focus IS inside the editor — the window handler
  // skips via a `.ProseMirror` closest() check to avoid double-firing.
  const activeInsertRef = useRef<SegmentRowHandle>({ selectAndReplace: () => {}, selectTerm: () => {} })
  const flushDraftRef = useRef<() => Promise<void>>(async () => {})

  // Modals
  const [concordanceOpen, setConcordanceOpen] = useState(false)
  const [findReplaceOpen, setFindReplaceOpen] = useState(false)
  // Whether Find & replace opens limited to the segment selection (its bar button).
  const [findInSelection, setFindInSelection] = useState(false)
  const [findHighlightTerm, setFindHighlightTerm] = useState<string | null>(null)
  const [showWhitespace, setShowWhitespace] = useState(() => { try { return localStorage.getItem('cat-show-whitespace') === 'true' } catch { return false } })
  const [trackChanges, setTrackChanges] = useState(() => { try { return localStorage.getItem('cat-track-changes') === 'true' } catch { return false } })
  const [segmentFilters, setSegmentFilters] = useState<SegmentFilters>(DEFAULT_FILTERS)
  const segmentSearchRef = useRef<HTMLInputElement>(null)

  // Current-user identity for the conflict banner. Drives "Saved by NAME"
  // vs "Saved from another tab/session" messaging — same-user 409s happen
  // routinely with two tabs open, and "Saved by Olivia while you were
  // editing" reads as nonsense when YOU are Olivia. Cached for the session.
  const { data: me, isPending: mePending } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe, staleTime: Infinity })
  const currentUserId = me?.id ?? null
  // PMs (owner/admin) can unlock segments; members can only lock. The route
  // is also gated server-side — this is the UI mirror so non-admins don't
  // see a button that 403s on click. Tri-state via me?.role: undefined
  // (loading) → assume member (safe default), 'admin'/'owner' → admin,
  // anything else → member.
  const isAdmin = isAdminRole(me?.role)
  const isManager = isManagerRole(me?.role)

  // Project context (id, name, source language) comes off the job response.
  // `GET /api/projects` is manager-only, so linguists can't read it.
  const { data: job, isPending: jobPending } = useJobDetail(jid)
  usePageTitle(job?.file_name, job?.project_name)

  const pipelineRunning = !!job && isPipelineRunning(job)
  // While this job's QE+APE step runs, the editor is
  // hard-locked — we show a waiting screen instead of the segment grid, and the
  // backend also rejects edits with 409. usePipelineEditorRefresh reloads the
  // rows when the step ends, so the grid loads automatically once it completes.
  const aiRunning = !!job && isAIStepRunning(job)
  const runningBadge = job ? pipelineBadge(job) : null
  const jobStoppedNote = job ? stoppedNote(job) : null


  // Every segment without its text: counts, filters, selection and navigation
  // run over this. The rows on screen load their text by page, further down.
  const editorIndex = useEditorIndex(jid, !!job)
  const indexRows = editorIndex.rows
  const indexLookup = editorIndex.lookup
  // Follows a running pipeline: counts and rows on screen reload as segments
  // are filled, and once more when the run ends.
  usePipelineEditorRefresh(jid, job, indexRows?.length ?? 0)

  // Batched comment counts for the whole file — one request feeds every row's
  // comment indicator and the "Commented" filter (no per-row N+1).
  const { data: commentCounts, isLoadingError: commentCountsFailed } = useQuery({
    queryKey: queryKeys.comments.countsByFile(job?.file_id ?? 0, job?.target_language ?? ''),
    queryFn: () => getCommentCounts(job!.file_id, job!.target_language),
    enabled: !!job,
  })

  // A failed counts fetch is indistinguishable from "nothing has comments":
  // indicators disappear and the Commented filter matches zero rows, which
  // reads as fact rather than as a broken request. Drop the filter and let the
  // bar disable the pill so the absence is legible.
  useEffect(() => {
    if (commentCountsFailed) {
      setSegmentFilters(f => (f.hasComments ? { ...f, hasComments: false } : f))
    }
  }, [commentCountsFailed])

  const commentCountByTranslation = useMemo(() => {
    const map = new Map<number, number>()
    commentCounts?.forEach(c => map.set(c.translation_id, c.comment_count))
    return map
  }, [commentCounts])

  const completeMut = useMutationWithToast({
    mutationFn: () => completeJob(jid),
    successMessage: data => (data.tm_saved_count > 0
      ? `Job completed. Saved ${data.tm_saved_count} segment(s) to TM.`
      : 'Job completed'),
    errorMessage: 'Complete failed',
    onSuccess: (data) => {
      invalidateJobContext(queryClient, pid, jid)
      // This job may have been the project's last unfinished one, in which
      // case the backend just auto-completed the project.
      invalidateProjectStatus(queryClient, pid)
      if (data.tm_saved_count > 0) invalidateTMContext(queryClient)
    },
  })

  // QA findings. The stored report loads with the page so dots and the count
  // reflect the last run (or the last autosave) before anyone clicks Run QA.
  // Per-segment refreshes and ignore/un-ignore patch the cached report in
  // place; a job-wide run replaces it.
  const qaReportKey = queryKeys.job.qaReport(jid)
  const { data: qaReport, isLoadingError: qaReportFailed } = useQuery({
    queryKey: qaReportKey,
    queryFn: () => getQAReport(jid),
    enabled: !!job,
  })

  // A failed report fetch is indistinguishable from "this job is clean": the
  // dots disappear and the QA filters match either every row (No issues) or
  // none, which reads as fact rather than as a broken request. Drop the filter
  // and let the bar disable the pills so the absence is legible.
  //
  // isLoadingError, not isError: a refetch that fails while cached findings are
  // still on screen leaves the dots rendering, so disabling the pills there
  // would contradict them — and the filter reset is one-way, the user's choice
  // does not come back when the next refetch succeeds.
  useEffect(() => {
    if (qaReportFailed) {
      setSegmentFilters(f => (f.qaFilters.length === 0 ? f : { ...f, qaFilters: [] }))
    }
  }, [qaReportFailed])

  // Spell check review dialog, and the scope it opens on.
  const [spellCheckOpen, setSpellCheckOpen] = useState(false)
  const [spellCheckScope, setSpellCheckScope] = useState<SpellRunScope>(DEFAULT_RUN_SCOPE)

  // Resolves to the fresh stored report, which replaces the cached one.
  const runQAMut = useMutationWithToast({
    mutationFn: async () => {
      // Save any unsaved edits in the active segment before running QA
      await flushDraftRef.current()
      await runQA(jid)
      return getQAReport(jid)
    },
    successMessage: report => qaRunToast(report.summary),
    errorMessage: 'QA failed',
    invalidateKeys: [queryKeys.job.analysis(jid)],
    onSuccess: report => queryClient.setQueryData<QAReport>(qaReportKey, report),
  })

  // Workflow step data
  const { data: jobSteps, isPending: jobStepsPending } = useQuery({
    queryKey: queryKeys.job.steps(jid),
    queryFn: () => listJobSteps(jid),
    enabled: !!job,
  })
  // The task the editor works on, and the ONE flag every write path checks.
  // The keyboard and sidebar handlers read it through a ref.
  const task = currentTask(jobSteps, job, isManager)
  // Which segments the previous workflow step auto-approved, and which AI
  // verdicts predate the current step.
  const { data: stepStatuses } = useQuery({
    queryKey: queryKeys.job.stepStatuses(jid),
    queryFn: () => getJobStepStatuses(jid),
    enabled: !!job,
  })
  const autoApprovedBefore = useMemo(() => autoApprovedBeforeStep(stepStatuses?.previous ?? []), [stepStatuses])
  const predictedBeforeStep = useMemo(() => new Set(stepStatuses?.predicted_before_step ?? []), [stepStatuses])
  // Track changes: each loaded row carries its text when the previous step
  // ended. A first step has nothing to compare with.
  const hasPreviousStep = (stepStatuses?.previous.length ?? 0) > 0
  const showTrackChanges = trackChanges && hasPreviousStep
  const previousStepName = stepStatuses?.previous[0]?.step_name
  const canEdit = canEditTask(task.mode)
  const canEditRef = useRef(canEdit)
  canEditRef.current = canEdit
  // "Strings contain HTML" off for this file: every insert goes in as plain text.
  const plainTextFile = job?.file_inline_html === false
  const plainTextFileRef = useRef(plainTextFile)
  plainTextFileRef.current = plainTextFile

  // The page stays on one loading screen until everything its first paint
  // depends on is in: the job, its steps, the segment index and the user.
  // Each piece rendering as it arrives reshuffles the page several times on a
  // slow connection. A failed query stops pending, so an error never hangs here.
  const editorLoading = mePending || jobPending
    || (!!job && (jobStepsPending || editorIndex.isPending))
  const gridShown = !editorLoading && !aiRunning
  const { rerun, skipAIStep } = useJobPipelineActions(jid, pid)

  const advanceMut = useMutationWithToast({
    mutationFn: (comment: string) => advanceJobStep(jid, comment),
    // The final advance's toast comes from completeMut.
    successMessage: (data) => {
      if (data.status === 'workflow_completed') return null
      // The next step's assignee name for the toast message
      const nextStep = jobSteps?.find(s => s.id === data.next_step_id)
      const assigneeMsg = nextStep?.assignee_name ? ` Assigned to ${nextStep.assignee_name}.` : ''
      return `Step completed — advanced to ${nextStep?.step_name ?? 'next step'}.${assigneeMsg}`
    },
    // The dialog stays open on failure, keeping the typed comment.
    errorMessage: 'Advance failed',
    onSuccess: (data) => {
      invalidateJobContext(queryClient, pid, jid)
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })
      setCompleteDialogOpen(false)
      if (data.status === 'workflow_completed') {
        // The final advance chains into /complete — that call is what closes
        // the job (status='completed', approved segments saved to TM). It's the
        // editor's only completion path. completeMut owns the toast (success
        // and error); this callback only leaves the editor, and only on
        // success — on failure the user stays put and can retry.
        completeMut.mutate(undefined, {
          onSuccess: () => navigate(`/projects/${pid}`),
        })
        return
      }
      // Navigate back to project detail (leave the editor after completing your step)
      navigate(`/projects/${pid}`)
    },
  })

  // "Approve all and complete": approve every translated segment, then complete
  // the step — unless a QA error, a change made meanwhile or someone else
  // editing it kept a segment unapproved, in which case the dialog stays open
  // and lists them.
  // The variable is the hand-off note the advance sends once all is approved.
  const approveAllMut = useMutation<SegmentBulkResult, Error, string>({
    mutationFn: () => confirmAllTranslations(jid),
    onSuccess: (res, comment) => {
      invalidateJobContext(queryClient, pid, jid)
      if (res.task_reopened) handleTaskReopened(queryClient, pid, jid)
      const blocked = blockedSegmentNumbers(res.skipped, id => indexLookup.byTranslation.get(id), idx => segmentNumber(rows[idx]))
      if (blocked) {
        setApproveBlocked(blocked)
        return
      }
      advanceMut.mutate(comment)
    },
    onError: (err: Error) => {
      if (isTaskLockedError(err)) {
        handleTaskLocked(queryClient, pid, jid, err)
        setCompleteDialogOpen(false)
        return
      }
      toast.error(`Approve all failed: ${err.message}`)
    },
  })

  const rejectMut = useMutationWithToast({
    mutationFn: (input: RejectStepInput) => rejectJobStep(jid, input),
    successMessage: (data) => {
      const to = jobSteps?.find(s => s.id === data.reopened_step_id)?.step_name ?? 'an earlier step'
      return `Sent back to ${to}.${data.rerun_ai ? ' The AI runs again.' : ''}`
    },
    // The dialog stays open on failure, keeping the typed reason.
    errorMessage: 'Reject failed',
    invalidateKeys: [queryKeys.job.steps(jid), queryKeys.job.detail(jid)],
    onSuccess: () => {
      // The reopened step gets its own statuses back, so the segments change.
      invalidateJobContext(queryClient, pid, jid)
      setRejectDialogOpen(false)
    },
  })

  // Jump to segment dialog
  const [jumpOpen, setJumpOpen] = useState(false)
  const [jumpInput, setJumpInput] = useState('')

  // Segment list scroll container. Declared here rather than beside the other
  // layout refs because the selection hook below needs it as its scope.
  const segmentScrollRef = useRef<HTMLDivElement>(null)
  // Concordance/terminology lookups start from whatever the linguist highlighted
  // in the segment list — source cell or target editor, either side is valid
  // (the query is matched against both columns of the TM).
  const { getSelectionText } = useLastSelection(segmentScrollRef)

  const [concordanceQuery, setConcordanceQuery] = useState('')

  // Both entry points (Mod+K, toolbar button) route through here, so the query is
  // always the phrase highlighted in the segment list — source cell or target
  // editor, both inside `segmentScrollRef`. Read via useLastSelection rather than
  // window.getSelection(): a button's mousedown collapses the selection before
  // its onClick runs. No selection → empty query, and the modal focuses its input.
  const openConcordance = useCallback(() => {
    setConcordanceQuery(getSelectionText())
    setConcordanceOpen(true)
  }, [getSelectionText])

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
  }, [refetchJobTermBases, isManager])

  // A Resources-panel insert: the click on a TM match or MT suggestion, and
  // Ctrl+1..5. An exact TM match is approved too. Refs are stable, so no
  // dependencies.
  const handleSidebarInsert = useCallback((insert: PanelInsert) => {
    if (!canEditRef.current) return
    activeInsertRef.current.replaceAll?.(
      panelInsertText(insert, activeSourceRichRef.current, plainTextFileRef.current),
      insert.insertToken,
      panelInsertApproves(insert),
    )
  }, [])

  // Global keyboard shortcuts (editor level)
  //
  // Segment-level shortcuts (Mod+Enter confirm, Mod+Shift+S
  // copy-source) must work when focus is on a sidebar button, not only inside
  // ProseMirror. The active SegmentRow populates `activeInsertRef` with
  // `confirm`/`copySource` handlers and this window-level listener routes to
  // them regardless of DOM focus. Skipped inside form inputs (F&R search,
  // etc.) and when TipTap has already handled the event, to avoid
  // double-firing.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey
      if (mod && e.key === 'k') {
        e.preventDefault()
        openConcordance()
      }
      // The browser's find sees only the rows on screen; the editor's search
      // covers every segment of the file.
      if (mod && !e.shiftKey && !e.altKey && e.key === 'f') {
        e.preventDefault()
        segmentSearchRef.current?.focus()
        segmentSearchRef.current?.select()
      }
      if (mod && e.key === 'h') {
        e.preventDefault()
        setFindInSelection(false)
        setFindReplaceOpen(true)
      }
      if (mod && e.key === 'g') {
        e.preventDefault()
        setJumpOpen(true)
        setJumpInput('')
      }
      // Ctrl+1..5 — insert TM match from sidebar, the same insert as a click.
      if (mod && e.key >= '1' && e.key <= '5' && canEditRef.current) {
        const idx = parseInt(e.key) - 1
        const m = tmMatchesRef.current[idx]
        if (m) {
          e.preventDefault()
          handleSidebarInsert({ kind: 'tm', match: m, insertToken: m.insert_token })
        }
      }

      // ── Segment-level shortcuts (Mod+Enter confirm, Mod+Shift+S copy-source) ──
      // Only route to activeInsertRef if:
      //   1. An active segment exists
      //   2. Target is NOT inside ProseMirror (let TipTap's keymap handle that
      //      case via its own addKeyboardShortcuts — avoids double-fire)
      //   3. Target is NOT a form control (INPUT/TEXTAREA/SELECT) — those
      //      handle their own Enter key (e.g. F&R search field, jump-to
      //      segment input, any <select> dropdown)
      const target = e.target as HTMLElement | null
      const inProseMirror = !!target?.closest('.ProseMirror')
      const tag = target?.tagName
      const inFormInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
      const canRouteToActive = activeSegmentIdxRef.current !== null && !inProseMirror && !inFormInput

      // Escape clears the segment selection before anything else closes —
      // unless it belongs to the open editor, a text field or a dialog. A
      // focused checkbox (the selection's own) does not count as a text field.
      const inTextField = inFormInput && !(target instanceof HTMLInputElement && target.type === 'checkbox')
      if (e.key === 'Escape' && !inProseMirror && !inTextField && !target?.closest('[role="dialog"],[role="alertdialog"]')
        && selectionRef.current.selected.size > 0) {
        e.preventDefault()
        selectionRef.current.clear()
        return
      }

      if (canRouteToActive && mod && e.key === 'Enter') {
        if (activeInsertRef.current.confirm) {
          e.preventDefault()
          activeInsertRef.current.confirm()
        }
      }
      if (canRouteToActive && mod && e.shiftKey && (e.key === 'S' || e.key === 's')) {
        if (activeInsertRef.current.copySource) {
          e.preventDefault()
          activeInsertRef.current.copySource()
        }
      }
      if (canRouteToActive && mod && e.shiftKey && (e.key === 'Backspace' || e.key === 'Delete')) {
        if (activeInsertRef.current.clear) {
          e.preventDefault()
          activeInsertRef.current.clear()
        }
      }
      // Tab/Shift+Tab and Down/Up from a read-only row: its editor takes no
      // focus or keys, so they land here on the page itself (a writable
      // editor handles them in its keymap). Focus on a control keeps them.
      const leave = activeInsertRef.current.leaveReadOnly
      if (leave && (!target || target === document.body) && !e.altKey && !mod) {
        if (e.key === 'Tab') {
          e.preventDefault()
          leave(e.shiftKey ? 'prev' : 'next', false)
        } else if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !e.shiftKey) {
          e.preventDefault()
          leave(e.key === 'ArrowDown' ? 'next' : 'prev', true)
        }
      }
      // Alt+Shift+T split, Alt+Shift+J join with next: TipTap has no binding
      // for them, so they also run from inside the editor. Matched on the
      // physical key: on a Mac, Alt+Shift turns e.key into another character.
      if (activeSegmentIdxRef.current !== null && !inFormInput && e.altKey && e.shiftKey && !mod
        && (e.code === 'KeyT' || e.code === 'KeyJ')) {
        const run = e.code === 'KeyT' ? activeInsertRef.current.split : activeInsertRef.current.joinNext
        if (run) {
          e.preventDefault()
          run()
        }
      }
      // Alt+Shift+G adds a term, from inside the editor too.
      if (activeSegmentIdxRef.current !== null && !inFormInput && e.altKey && e.shiftKey && !mod && e.code === 'KeyG') {
        e.preventDefault()
        void openQuickAddTerm()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [openConcordance, handleSidebarInsert, openQuickAddTerm])

  // Workflow-aware toolbar state, from the current task. Per-button gating
  // (TM / MT / Predict) lives in JobPipelineActions, which is told the
  // first-step answer because the steps list is loaded here.
  const isFinalStep = !!task.step && task.step.step_order === (jobSteps?.length ?? 1) - 1
  const currentStepIndex = task.step ? (jobSteps?.findIndex(s => s.id === task.step!.id) ?? -1) : -1
  const inFirstStep = !task.step || task.step.step_order === 0
  // Clear (toolbar, selection bar, segment) runs up to the first human or
  // hybrid step; Pre-translate stays first-step only.
  const taskInClearWindow = inClearWindow(task.step, job?.last_clear_step_order ?? 0)

  // Job-wide derived data, memoized on the index: a render that changes it
  // not (a column drag fires one per pointer move) must not walk every
  // segment again. A save replaces one index row, so these recompute then.
  const rows = useMemo(() => indexRows ?? [], [indexRows])
  // What Complete step would leave unapproved: drafts (any text) and untranslated.
  const unapproved = useMemo(() => unapprovedCounts(translatedRows(rows)), [rows])
  const statusCounts = useMemo(
    () => countByDisplayStatus(rows, predictedBeforeStep),
    [rows, predictedBeforeStep],
  )

  const qaResultsByTranslation = useMemo(() => groupFindingsByTranslation(qaReport?.results), [qaReport])

  // Width of the unnamed extras column: the widest row in the whole job, not
  // just the filtered or rendered rows, so filtering doesn't resize it.
  const extrasWidth = useMemo(() => extrasColumnWidth(translatedRows(rows).map(r => ({
    locked: r.locked,
    qeLabel: qeChipLabel(r.qeNoteHead),
    hasQA: (qaResultsByTranslation.get(r.translationId!)?.length ?? 0) > 0,
    commentCount: commentCountByTranslation.get(r.translationId!) ?? 0,
  }))), [rows, qaResultsByTranslation, commentCountByTranslation])
  // Measured once: an overlay scrollbar gets an empty gutter track at the
  // grid's right edge (OVERLAY_SCROLLBAR_GUTTER).
  const [overlayScrollbar] = useState(hasOverlayScrollbars)

  // The filter bar's text search runs on the server over the whole file; the
  // other filters run here over the index.
  const textSearch = useMemo(() => (segmentFilters.searchText.trim() === '' ? null : {
    text: segmentFilters.searchText,
    source: segmentFilters.searchSource,
    target: segmentFilters.searchTarget,
    caseSensitive: segmentFilters.caseSensitive,
    regex: segmentFilters.useRegex,
  }), [segmentFilters])
  const { matches: textMatches, state: searchState } = useEditorSearch(jid, textSearch)

  // Stable while the visible rows and their order stay the same: a save
  // replaces an index row, and a new array here would make the row
  // virtualizer recompute every row position (its getItemKey depends on it).
  const filteredIndices = useStableArray(useMemo(
    () => filterSegments(rows, qaResultsByTranslation, segmentFilters, textMatches, commentCountByTranslation, predictedBeforeStep),
    [rows, qaResultsByTranslation, segmentFilters, textMatches, commentCountByTranslation, predictedBeforeStep],
  ))

  const segmentCount = rows.length
  const filteredCount = filteredIndices.length

  // Multi-segment selection, by translation id, over the filtered rows.
  const orderedTranslationIds = useMemo(
    () => filteredIndices.flatMap(i => rows[i].translationId ?? []),
    [filteredIndices, rows],
  )
  const selection = useSegmentSelection(orderedTranslationIds)
  const selectionRef = useRef(selection)
  selectionRef.current = selection
  const indexRowByTranslation = useMemo(
    () => new Map(translatedRows(rows).map(r => [r.translationId!, r])),
    [rows],
  )
  const pickedSet = useMemo(() => new Set(selection.picked), [selection.picked])

  // Single chokepoint for changing the active segment. Every
  // transition (row click, Tab, Cmd+G, F&R navigate, click-outside) flushes
  // the previously-active row's draft FIRST, then sets the new index. The
  // discard option is reserved for Escape — the only "abandon edit" gesture.
  // Never call `setActiveSegmentIdx` directly: a bypass skips the flush and
  // loses the edit. The lint rule in .eslintrc pins direct setter
  // calls to this helper.
  const switchActiveSegment = useCallback(async (
    newIdx: number | null,
    opts: { discard?: boolean; caret?: EntryCaret } = {},
  ): Promise<void> => {
    if (
      !opts.discard &&
      activeSegmentIdxRef.current !== null &&
      activeRowDirtyRef.current
    ) {
      try {
        await flushDraftRef.current()
      } catch (err) {
        // saveDraft already toasts on conflict (409) and other errors; we just
        // don't want a flush failure to block the transition.
        console.warn('switchActiveSegment: flushDraft failed:', err)
      }
    }
    activeRowDirtyRef.current = false
    setEntryCaret(opts.caret && newIdx !== null ? { idx: newIdx, caret: opts.caret } : null)
    // The ONLY legitimate setActiveSegmentIdx call site, gated by the flush invariant above.
    // eslint-disable-next-line no-restricted-syntax
    setActiveSegmentIdx(newIdx)
  }, [])

  // Navigate to next/previous within filtered results
  const goToSegment = useCallback((idx: number, caret?: LineCaret) => {
    if (idx >= 0 && idx < filteredCount) {
      void switchActiveSegment(filteredIndices[idx], { caret })
    }
  }, [filteredCount, filteredIndices, switchActiveSegment])

  // Stable sidebar callbacks (refs are stable, so no dependencies needed)
  const handleTermClick = useCallback((src: string, tgt: string, mt?: string) => activeInsertRef.current.selectAndReplace(src, tgt, mt), [])
  const handleTermHover = useCallback((src: string, tgt: string, mt?: string) => activeInsertRef.current.selectTerm?.(src, tgt, mt), [])
  const handleSidebarClose = useCallback(() => {
    setSidebarOpen(false)
    writeStorage('cat-sidebar-open', 'false')
  }, [])
  const handleSidebarOpen = useCallback(() => {
    setSidebarOpen(true)
    writeStorage('cat-sidebar-open', 'true')
  }, [])
  // Resources panel width. Dragging it all the way in closes the panel rather
  // than leaving a strip nothing fits in; past the maximum the segment grid is
  // the one that suffers.
  const sidebarResize = usePanelResize({
    storageKey: 'cat-sidebar-width',
    min: 30,
    max: 640,
    fallback: SIDEBAR_CLICK_WIDTH,
    label: 'Resize resources panel',
    onCollapse: handleSidebarClose,
    onExpand: handleSidebarOpen,
  })
  // Clicking the closed panel's tab opens it at SIDEBAR_CLICK_WIDTH, whatever
  // the last dragged width was. Dragging that tab instead chooses its own width.
  const openSidebarByClick = useCallback(() => {
    handleSidebarOpen()
    if (!sidebarResize.wasDragged()) sidebarResize.setWidth(SIDEBAR_CLICK_WIDTH)
  }, [handleSidebarOpen, sidebarResize])

  // Auto-collapse the Resources sidebar on narrow viewports (< 1200px) so the
  // segment grid keeps enough room. This is a display override, not a
  // preference change: we never write localStorage here, so widening the window
  // restores whatever the user last chose. The reopen handle still works as a
  // temporary manual override while narrow (it does persist, via handleSidebarOpen).
  const isNarrow = useMediaQuery('(max-width: 1199px)')
  useEffect(() => {
    if (isNarrow) {
      setSidebarOpen(false)
    } else {
      try { setSidebarOpen(localStorage.getItem('cat-sidebar-open') !== 'false') } catch { setSidebarOpen(true) }
    }
  }, [isNarrow])

  // The column header lives INSIDE the scroll container as a sticky row so
  // it shares the exact same (scrollbar-reduced) content width as the body
  // rows — otherwise the Source/Translation columns drift out of alignment when
  // the vertical scrollbar appears. Because the header occupies flow space at
  // the top of the scroll content, the virtualizer needs its height as
  // `scrollMargin` (content-before-the-list), and each row's transform
  // subtracts it. We measure the header rather than hardcoding so font/zoom
  // changes stay correct.
  const headerRef = useRef<HTMLDivElement>(null)
  const columnMins = useMemo(() => segmentColumnMins(extrasWidth), [extrasWidth])
  const columnDrag = useColumnBorderDrag(headerRef, columnMins)
  // The header draws no border after its last visible column.
  const lastHeader = extrasWidth > 0 ? SEGMENT_HEADER_CELLS.length - 1 : LAST_LABELLED_HEADER
  const [headerHeight, setHeaderHeight] = useState(0)
  useLayoutEffect(() => {
    const el = headerRef.current
    if (!el) return
    const measure = () => setHeaderHeight(el.getBoundingClientRect().height)
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [gridShown])
  // Position of the active segment in the filtered list. The
  // virtualizer's `rangeExtractor` (below) appends this index to the visible
  // range so the active SegmentRow stays mounted regardless of scroll position.
  // Unmounting it would destroy the in-memory edit not yet auto-saved by the
  // 3s draft-save debounce, and on scroll-back the editor would re-initialize
  // from the stale React Query cache. Keeping the row mounted preserves the
  // ProseMirror doc and the React state holding `editText` / `originalText` /
  // baselineRef.
  const activeRowKeeperRef = useRef<number>(-1)
  activeRowKeeperRef.current = activeSegmentIdx == null ? -1 : filteredIndices.indexOf(activeSegmentIdx)
  const rowVirtualizer = useVirtualizer({
    count: filteredCount,
    getScrollElement: () => segmentScrollRef.current,
    // A one-line row (index.css --seg-row-min-h), the same for every row.
    // TanStack caches a measured size only when it differs from the estimate,
    // so an estimate that changed with the active row would replace the real
    // height of every row that measured exactly 33px.
    estimateSize: () => 33,
    // Measured heights are cached per segment, not per list position: a filter
    // or search moves segments to new positions, and a row that stays mounted
    // through that is never measured again.
    getItemKey: useCallback(
      (idx: number) => rows[filteredIndices[idx]]?.segmentId ?? idx,
      [rows, filteredIndices],
    ),
    overscan: 10,
    // Sticky header occupies this many px at the top of the scroll content.
    scrollMargin: headerHeight,
    rangeExtractor: useCallback((range: Range) => {
      const base = defaultRangeExtractor(range)
      const keeper = activeRowKeeperRef.current
      if (keeper < 0 || base.includes(keeper)) return base
      // Insert the active index in sorted order so virtual items stay ordered.
      const merged = [...base, keeper].sort((a, b) => a - b)
      return merged
    }, []),
  })

  // Stabilise the clicked row's visual position across activation.
  // Anchor via the row element's `getBoundingClientRect()`, not TanStack's
  // `item.start`: the previously-active row's height collapse only resolves
  // after its `measureElement` ref fires in a later frame, so `start` values
  // are stale in the first rAF. Capture the clicked row's screen-relative top
  // before changing activeSegmentIdx, then restore it over a settle loop
  // (multiple rAFs) by adjusting scrollTop.
  const prevActiveRef = useRef<number | null>(null)
  const programmaticScrollRef = useRef(false)
  const pendingAnchorRef = useRef<{ idx: number; clientTop: number } | null>(null)

  // Wrap onActivate from each row so we can capture the visual top of the
  // row that's about to become active, BEFORE React renders the new state.
  const captureAnchorBeforeActivate = useCallback((idx: number) => {
    const row = document.querySelector<HTMLElement>(`[data-segment-idx="${idx}"]`)
    if (row) {
      pendingAnchorRef.current = { idx, clientTop: row.getBoundingClientRect().top }
    }
  }, [])

  useEffect(() => {
    if (activeSegmentIdx === prevActiveRef.current) return
    const scrollEl = segmentScrollRef.current

    // Programmatic scroll path (F&R) handles its own positioning — skip.
    if (programmaticScrollRef.current) {
      programmaticScrollRef.current = false
      prevActiveRef.current = activeSegmentIdx
      return
    }

    prevActiveRef.current = activeSegmentIdx
    if (activeSegmentIdx == null || !scrollEl) return

    const anchor = pendingAnchorRef.current
    pendingAnchorRef.current = null

    // Settle loop: adjust on each rAF for a bounded number of frames so
    // late measureElement flushes (previously-active row collapsing back
    // to compact, newly-active editor rendering) can be absorbed. Stop
    // EARLY once the row's position is stable across two consecutive
    // checks — prevents the loop from chasing oscillating measurements
    // when the layout never quite settles.
    let frames = 0
    const MAX_FRAMES = 20 // ~330ms at 60fps, covers measurement + 150ms transition
    let lastTop: number | null = null
    let lastHeight = -1
    let stableFrames = 0
    const settle = () => {
      const row = document.querySelector<HTMLElement>(`[data-segment-idx="${activeSegmentIdx}"]`)
      if (!row) {
        if (frames++ < MAX_FRAMES) requestAnimationFrame(settle)
        return
      }
      const rect = row.getBoundingClientRect()
      // The virtualizer's total size, which drops when the row that just
      // deactivated collapses back to compact.
      const contentHeight = scrollEl.scrollHeight

      // Stability check: the row hasn't moved AND the list has stopped
      // resizing — stop adjusting before more late measurements arrive and we
      // start oscillating. Both halves are needed: on a slow frame the row can
      // hold still for two frames while the deactivated row's collapse is
      // still pending, and exiting there leaves the anchor to be undone by a
      // shift of the collapsed row's full height.
      if (lastTop !== null && Math.abs(rect.top - lastTop) < 1 && contentHeight === lastHeight) {
        stableFrames++
        if (stableFrames >= 2) return // settled for 2 consecutive frames
      } else {
        stableFrames = 0
      }
      lastTop = rect.top
      lastHeight = contentHeight

      if (anchor && anchor.idx === activeSegmentIdx) {
        const delta = rect.top - anchor.clientTop
        if (Math.abs(delta) > 1) {
          scrollEl.scrollTop += delta
          lastTop = anchor.clientTop // expected post-adjustment position
        }
      } else {
        const containerRect = scrollEl.getBoundingClientRect()
        const rowTopInContainer = rect.top - containerRect.top
        const rowBottomInContainer = rect.bottom - containerRect.top
        if (rowTopInContainer < 0 || rowBottomInContainer > scrollEl.clientHeight) {
          row.scrollIntoView({ block: 'center', behavior: 'auto' })
        }
      }

      if (frames++ < MAX_FRAMES) requestAnimationFrame(settle)
    }
    requestAnimationFrame(settle)
  }, [activeSegmentIdx])

  // Scroll to a row by its index position (Find & Replace, spell check,
  // cross-references, Jump to segment, a reload). Explicitly scrolls the
  // virtualizer — does NOT rely on the activeSegmentIdx effect, which has
  // "restore position" logic that fights programmatic navigation. Only scrolls
  // when the row is off-screen or clipped; smooth only over a short distance,
  // since a smooth scroll across thousands of rows of estimated height misses.
  // Returns false when the filter hides the row.
  const scrollToRow = useCallback(async (
    segIdx: number,
    opts: { activate?: boolean } = {},
  ): Promise<boolean> => {
    const activate = opts.activate ?? true
    const filteredPos = filteredIndices.indexOf(segIdx)
    if (filteredPos === -1) return false
    programmaticScrollRef.current = true // tell the activeSegmentIdx effect to skip its scroll
    // Route through the chokepoint so the previously-active row's draft
    // is flushed before we navigate to the F&R match.
    //
    // `activate: false` scrolls without opening an editor. The spell check
    // dialog needs that: it writes corrections through the bulk draft endpoint,
    // and an open editor on a row being written to would race its own autosave
    // against those writes.
    if (activate) await switchActiveSegment(segIdx)
    requestAnimationFrame(() => {
      const scrollEl = segmentScrollRef.current
      const items = rowVirtualizer.getVirtualItems()
      const behavior = Math.abs(filteredPos - (items[0]?.index ?? 0)) < SMOOTH_SCROLL_ROWS ? 'smooth' : 'auto'
      if (!scrollEl) {
        rowVirtualizer.scrollToIndex(filteredPos, { align: 'center', behavior })
        return
      }
      // Check if the target row is already fully visible
      const item = items.find(v => v.index === filteredPos)
      if (item) {
        const rowTop = item.start - scrollEl.scrollTop
        const rowBottom = rowTop + item.size
        const viewportHeight = scrollEl.clientHeight
        // Fully visible with some padding (active row expands, so add margin)
        const pad = 40
        if (rowTop >= pad && rowBottom <= viewportHeight - pad) {
          return // already in view — no scroll needed
        }
      }
      // 'auto' = minimal scroll to bring item into view (TanStack's equivalent of 'nearest')
      rowVirtualizer.scrollToIndex(filteredPos, { align: 'auto', behavior })
    })
    return true
  }, [filteredIndices, rowVirtualizer, switchActiveSegment])

  const scrollToTranslation = useCallback(async (translationId: number, opts: { activate?: boolean } = {}) => {
    const segIdx = indexLookup.byTranslation.get(translationId)
    if (segIdx !== undefined) await scrollToRow(segIdx, opts)
  }, [indexLookup, scrollToRow])

  // Split and join, while the task takes edits: Split on the open row's
  // source, Alt+Shift+J before a row it can join, Join on a selection.
  const openTranslation = useCallback((id: number) => void scrollToTranslation(id, { activate: true }), [scrollToTranslation])
  const splitJoin = useSegmentSplitJoin({ jobId: jid, projectId: pid, lookup: indexLookup, openTranslation })
  const pickedRows = useMemo(
    () => selection.picked.flatMap(id => indexLookup.byTranslation.get(id) ?? []).sort((a, b) => a - b),
    [selection.picked, indexLookup],
  )
  const joinSelection = async () => {
    await switchActiveSegment(null)
    const ids = pickedRows.flatMap(i => rows[i].translationId ?? [])
    selection.clear()
    splitJoin.joinTranslations(ids)
  }

  // The last opened segment stays in the URL (?seg=<segment id>), so a reload,
  // Back or a shared link returns to it. Replaced, not pushed: moving between
  // segments adds no history entries.
  const [searchParams, setSearchParams] = useSearchParams()
  const activeSegmentId = activeSegmentIdx != null ? rows[activeSegmentIdx]?.segmentId : undefined
  useEffect(() => {
    if (activeSegmentId === undefined || searchParams.get('seg') === String(activeSegmentId)) return
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      next.set('seg', String(activeSegmentId))
      return next
    }, { replace: true })
  }, [activeSegmentId, searchParams, setSearchParams])

  // Once the grid is up, open the segment the URL names.
  const restoredRef = useRef(false)
  useEffect(() => {
    if (restoredRef.current || !gridShown || !indexRows) return
    restoredRef.current = true
    const idx = indexLookup.bySegment.get(Number(searchParams.get('seg')))
    if (idx !== undefined) void scrollToRow(idx)
  }, [gridShown, indexRows, indexLookup, searchParams, scrollToRow])

  // Text of the rows on screen, plus the open row wherever it is, by segment
  // id. A row whose page has not arrived yet renders as a placeholder.
  const virtualItems = rowVirtualizer.getVirtualItems()
  const shownIndices = useMemo(() => {
    const out = virtualItems.map(v => filteredIndices[v.index])
    // Unfiltered, the page past each edge of the view loads too, so a short
    // scroll finds its rows ready.
    if (out.length > 0 && filteredIndices.length === rows.length) {
      out.push(Math.max(0, out[0] - EDITOR_PAGE_SIZE / 2), Math.min(rows.length - 1, out[out.length - 1] + EDITOR_PAGE_SIZE / 2))
    }
    if (activeSegmentIdx != null) out.push(activeSegmentIdx)
    return out
  }, [virtualItems, filteredIndices, activeSegmentIdx, rows.length])
  const loadedRows = useEditorPages(jid, indexRows, shownIndices)
  const rowAt = (idx: number): EditorRow | undefined => {
    const row = rows[idx]
    return row ? loadedRows.get(row.segmentId) : undefined
  }
  const activeRow = activeSegmentIdx != null ? rowAt(activeSegmentIdx) : undefined

  // --- Spell check review dialog ---
  //
  // Every correction goes through the bulk draft endpoint, including the segment
  // the user last had open: `beforeRun` deactivates it first, so there's one
  // write path instead of an editor branch racing its own autosave against the
  // batch writes.
  const spellSegmentIds = useMemo(() => rows.map(r => r.segmentId), [rows])
  const spellRowsBySegment = useMemo(() => new Map(translatedRows(rows).map(r => [
    r.segmentId, { id: r.translationId!, status: r.status, locked: r.locked },
  ])), [rows])

  // Cross-reference terms, from the server's list of referenced segments. The
  // map keeps its identity until a referenced target changes (a save patches
  // the cached list): SegmentRow's memo compares it by reference, and a map
  // that churned per keystroke would re-render every visible row.
  const { data: refs } = useEditorRefs(jid, !!job)
  const refTerms = useMemo<RefTerms>(
    () => new Map((refs ?? []).map(r => [r.key, { source: r.source, target: r.target }])),
    [refs],
  )
  // Where each referenced segment sits in the index, by key.
  const refRowByKey = useMemo(() => {
    const out = new Map<string, number>()
    for (const r of refs ?? []) {
      const idx = indexLookup.bySegment.get(r.segment_id)
      if (idx !== undefined) out.set(r.key, idx)
    }
    return out
  }, [refs, indexLookup])

  // Jump to the segment a cross-reference points at, via the same helper F&R
  // uses. Called from the sidebar's References section — the chips themselves
  // are deliberately not clickable (a jump on plain click teleported linguists
  // away when they merely meant to activate the row).
  const jumpToReference = (refKey: string) => {
    const idx = refRowByKey.get(refKey)
    const translationId = idx !== undefined ? rows[idx].translationId : null
    if (translationId != null) void scrollToTranslation(translationId)
  }

  // The active segment's cross-references for the sidebar's References section:
  // the term each chip renders + the row that defines it. Identity is stable
  // across unrelated edits (deps change only on segment switch or when a
  // referenced target changes), which is what the sidebar's memo comparator
  // relies on.
  const activeSourceRich = activeRow?.segment.source_rich_text
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
  // Mirror the active segment's tagged source into a ref so the TM insert
  // paths (sidebar click + Mod+1..5) can re-anchor tags without re-subscribing.
  activeSourceRichRef.current = activeSourceRich ?? ''

  const activeSourceText = activeRow?.segment.source_text ?? ''

  // Same hook + query key the sidebar's TM section uses, so this shares its
  // request rather than issuing a second one.
  const activeTranslationId = activeSegmentIdx != null ? rows[activeSegmentIdx]?.translationId ?? undefined : undefined

  const { usable: usableTMMatches } = useTMMatches(jid, activeSourceText, activeTranslationId ?? null)

  // Mod+1..5 reads the hits through a ref so the window keydown listener never
  // closes over a stale array.
  useEffect(() => {
    tmMatchesRef.current = usableTMMatches
  }, [usableTMMatches])

  // The hit the segment's source is diffed against: whichever TM card the user
  // is pointing at, else the best one. Cleared on segment switch so a hover
  // never carries over to the next segment's source.
  const [hoveredTMSource, setHoveredTMSource] = useState<string | null>(null)
  useEffect(() => { setHoveredTMSource(null) }, [activeSegmentIdx])
  const tmDiffReference = hoveredTMSource ?? usableTMMatches[0]?.source_text ?? null

  const { applySpellFixes, spellFixActions } = useSpellFixes({ jid, pid, job, isAdmin, activeTranslationId })

  // Segment holds: the open row is held for this user while they may edit it;
  // a row someone else holds is read-only here and names them. It follows
  // activeTranslationId, which changes only after switchActiveSegment's flush.
  const activeRowLocked = activeSegmentIdx != null && rows[activeSegmentIdx]?.locked === true
  const { heldByOthers, streamLive: holdStreamLive } = useSegmentHold({
    jobId: jid,
    translationId: activeTranslationId ?? null,
    canHold: canEdit && !activeRowLocked,
    currentUserId,
    onIdle: () => { void switchActiveSegment(null) },
  })
  const activeRowHeld = activeTranslationId != null && heldByOthers.has(activeTranslationId)


  // Click outside active segment → deactivate.
  // Route through `switchActiveSegment(null)` so the flush-on-transition
  // behavior matches every other exit path (row click, Tab, F&R navigate, etc.).
  // The dirty check + flush + setter live in the chokepoint helper.
  useEffect(() => {
    const handleMouseDown = (e: MouseEvent) => {
      const idx = activeSegmentIdxRef.current
      if (idx === null) return
      const target = e.target as HTMLElement
      // Don't deactivate if clicking inside the editor, the sidebar, or a button
      if (target.closest('[data-testid="segment-editor"]') || target.closest('[data-term-panel]') || target.closest('[data-testid="editor-sidebar"]') || target.closest('button')) return
      // Don't deactivate if clicking inside any portaled dialog (Jump, Step Completion,
      // Step Rejection, Keyboard shortcuts, Analysis, conflict — anything Radix portals
      // to <body>). Without this, mousedown bubbles past the portal subtree to document
      // and trips the deactivation path.
      if (target.closest('[data-slot$="-portal"]')) return
      // Popovers portal to <body> without a "-portal" slot; their popup carries
      // data-slot="popover-content". The QA finding popover lives inside a
      // segment row, and a mousedown on its text would otherwise deactivate the
      // row here while the React click, bubbling through the portal to the row,
      // re-activates it — a needless editor unmount/remount on every click.
      if (target.closest('[data-slot="popover-content"]')) return
      // Same for the inline spell fix panel — portaled to <body> too, and its
      // non-<button> parts (source text, the "Change to" input) must not
      // deactivate the segment the panel is fixing.
      if (target.closest('[data-spellcheck-menu]')) return
      // Don't deactivate if clicking inside the segment list (gaps between rows, row borders, etc.)
      // Row clicks will fire onActivate to switch segments; only deactivate for clicks fully outside.
      if (segmentScrollRef.current?.contains(target)) return
      void switchActiveSegment(null)
    }
    document.addEventListener('mousedown', handleMouseDown)
    return () => document.removeEventListener('mousedown', handleMouseDown)
  }, [switchActiveSegment])

  // Same message as the route's Suspense fallback, so loading the editor code
  // and loading its data read as one screen.
  if (editorLoading) return <LoadingPlaceholder continues message="Loading editor…" />

  return (
    <TooltipProvider delay={200}>
      {/* data-hold-stream says whether other editors' holds arrive live or by poll. */}
      <PageContainer width="full" className={cn('max-md:min-w-3xl', LEVERAGE_BAR_CLEARANCE)} data-hold-stream={holdStreamLive ? 'live' : 'polling'}>
        <Breadcrumbs
          items={[
            { label: 'Projects', to: '/projects' },
            {
              label: <EditableTitle name={job?.project_name ?? String(pid)} canEdit={false} />,
              to: `/projects/${pid}`,
              shrink: true,
            },
            { label: <EditableTitle name={job?.file_name ?? `Job ${jid}`} canEdit={false} /> },
          ]}
          trailing={job && (
            <>
              <Chip data-testid="cat-target-language">{job.target_language.toUpperCase()}</Chip>
              {jobSteps && <TaskStepChip task={task} steps={jobSteps} job={job} isFinalStep={isFinalStep} />}
              <TaskDeadlineChip task={task} />
            </>
          )}
          actions={
            <>
              {jobSteps && jobSteps.length > 0 && (
                <TaskActions
                  task={task}
                  isFinalStep={isFinalStep}
                  canRerun={!!job && canRerunTask(job, isManager)}
                  rerunPending={rerun.isPending}
                  completePending={advanceMut.isPending}
                  rejectPending={rejectMut.isPending}
                  onRerun={() => rerun.mutate()}
                  onComplete={() => {
                    // Always through the dialog: it takes the hand-off note,
                    // and offers Approve all when drafts are left.
                    setApproveBlocked(null)
                    setCompleteDialogOpen(true)
                  }}
                  canReject={currentStepIndex >= 0 && canRollback(jobSteps, currentStepIndex, isManager)}
                  onReject={() => setRejectDialogOpen(true)}
                />
              )}
              {job && (
                <DownloadMenu
                  projectId={pid}
                  jobs={[job]}
                  fileIds={[job.file_id]}
                  size="sm"
                  testidPrefix="editor-download"
                />
              )}
              {job && (
                <ImportXLIFFAction projectId={pid} job={job}>
                  {(pick, pending) => (
                    <Button variant="outline" size="sm" onClick={pick} disabled={pending} data-testid="editor-reimport-btn">
                      <Upload />
                      {pending ? 'Importing…' : 'Import XLIFF'}
                    </Button>
                  )}
                </ImportXLIFFAction>
              )}
            </>
          }
        />

        {/* flex-wrap here too: at narrow widths the action row drops BELOW the
            status badges and gets the full container width, instead of being
            squeezed into a column of stacked buttons beside them. */}
        <div className="flex items-center justify-between gap-y-2 flex-wrap mb-4">
          <div className="flex items-center gap-3">
            {runningBadge && (
              <Chip color={runningBadge.color} className="animate-pulse" title={runningBadge.detail}>
                {runningBadge.label}
              </Chip>
            )}
            {jobStoppedNote && (
              <span
                className="text-xs text-amber-600 dark:text-amber-400 truncate max-w-md"
                title={jobStoppedNote}
                data-testid="job-stopped-note"
              >
                {jobStoppedNote}
              </span>
            )}
          </div>
          {/* flex-wrap + justify-end: on a narrow screen this row's actions
              don't fit, and without wrapping the tail runs off the right edge —
              unreachable, not merely clipped. */}
          <div data-guide="editor.toolbar" className="flex flex-wrap justify-end gap-2">
            {/* hideComplete: "Complete step →" / "Complete final step ✓" on
                the breadcrumb line is this screen's completion control, so the
                per-job Complete would be a second button with the same meaning. */}
            {job && <JobPipelineActions job={job} projectId={pid} inFirstStep={inFirstStep} inClearWindow={taskInClearWindow} canEdit={canEdit} hideComplete />}
            <Tooltip>
              {/* preventDefault stops the browser collapsing the segment-list
                  selection, so the highlight stays visible while the modal opens.
                  Cosmetic only — useLastSelection is what carries the phrase. */}
              <TooltipTrigger render={
                <Button
                  variant="outline"
                  size="sm"
                  onMouseDown={e => e.preventDefault()}
                  onClick={() => openConcordance()}
                  className="h-7 text-xs"
                />
              }>
                Concordance
              </TooltipTrigger>
              <TooltipContent>{modKey}+K — search TM/terminology for the selected phrase</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger render={
                <Button
                  onClick={() => { setSpellCheckScope(DEFAULT_RUN_SCOPE); setSpellCheckOpen(true) }}
                  disabled={!job || job.counts.total === 0}
                  variant="outline"
                  size="sm"
                  data-testid="open-spellcheck"
                />
              }>
                Spell check
              </TooltipTrigger>
              <TooltipContent>
                Review spelling one error at a time, with replace all and ignore all
              </TooltipContent>
            </Tooltip>
            {qaReport?.profile_assigned === false ? (
              <Button
                data-testid="run-qa-btn"
                disabled
                disabledReason={NO_QA_PROFILE_REASON}
                variant="outline"
                size="sm"
              >
                {qaButtonLabel(false, qaReport.summary)}
              </Button>
            ) : (
              <Tooltip>
                <TooltipTrigger render={
                  <Button
                    data-testid="run-qa-btn"
                    onClick={() => runQAMut.mutate()}
                    disabled={runQAMut.isPending || !job || job.counts.total === 0}
                    variant="outline"
                    size="sm"
                  />
                }>
                  {qaButtonLabel(runQAMut.isPending, qaReport?.summary)}
                </TooltipTrigger>
                <TooltipContent>Run quality checks on all segments (tags, numbers, terminology, punctuation)</TooltipContent>
              </Tooltip>
            )}
          </div>
        </div>

        {/* The task's error and the reason a later step sent it back. */}
        {jobSteps && (
          <TaskNotes
            task={task}
            steps={jobSteps}
            errorMessage={job?.error_message}
            onSkipAIStep={job && canSkipAIStep(job, task.step, isManager) ? () => skipAIStep.mutate() : undefined}
            skipAIStepPending={skipAIStep.isPending}
          />
        )}

        {aiRunning ? (
          // QE+APE hard lock. The QE+APE engine is rewriting/approving rows;
          // human edits would race the predict writes, so the grid is hidden
          // until the step completes. The header keeps its live badge + the
          // job-detail poll (every 2s) trips the finish effect, which reloads
          // translations automatically.
          <div
            className="flex-1 flex flex-col items-center justify-center gap-3 py-24 text-center"
            data-testid="qeape-waiting"
          >
            <Loader2Icon aria-hidden="true" className="size-8 animate-spin text-gray-400 dark:text-gray-500" />
            <div className="text-base font-medium text-gray-900 dark:text-gray-100">
              {job?.current_step || 'QE+APE in progress'}
            </div>
            <div className="max-w-md text-sm text-gray-500 dark:text-gray-400">
              This task is locked while automated quality evaluation and post-editing run.
              It unlocks automatically as soon as the step completes.
            </div>
          </div>
        ) : editorIndex.error ? (
          // Without the index there is no grid to show; an empty one would
          // read as a file with no segments.
          <LoadFailed what="segments" error={editorIndex.error} onRetry={() => void editorIndex.refetch()} />
        ) : (
          <>
          {/* Progress bar */}
          {indexRowByTranslation.size > 0 && (() => {
            // Approved and auto-approved: every translation Complete step would not count.
            const total = indexRowByTranslation.size
            const approved = total - unapproved.drafts - unapproved.untranslated
            const pct = Math.round((approved / total) * 100)
            const summary = `${pct}% approved (${approved}/${total})`
            return (
              <Progress
                value={pct}
                size="sm"
                tone="green"
                title={summary}
                aria-label="Segments approved"
                aria-valuetext={summary}
                className="mb-2"
              />
            )
          })()}

          <SegmentFilterBar
            filters={segmentFilters}
            searchInputRef={segmentSearchRef}
            searchState={searchState}
            onChange={setSegmentFilters}
            totalCount={segmentCount}
            filteredCount={filteredCount}
            commentCountsUnavailable={commentCountsFailed}
            qaReportUnavailable={qaReportFailed}
            onOpenFindReplace={() => { setFindInSelection(false); setFindReplaceOpen(true) }}
            statusCounts={statusCounts}
            actions={
              <>
                {hasPreviousStep ? (
                  <Tooltip>
                    <TooltipTrigger render={
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 text-xs"
                        aria-label="Track changes"
                        aria-pressed={showTrackChanges}
                        onClick={() => {
                          setTrackChanges(v => {
                            const next = !v
                            try { localStorage.setItem('cat-track-changes', String(next)) } catch { /* storage blocked */ }
                            return next
                          })
                        }}
                      />
                    }>
                      <FileDiff className="size-3.5" />
                    </TooltipTrigger>
                    <TooltipContent>{`Track changes: mark what changed since ${previousStepName ?? 'the previous step'}`}</TooltipContent>
                  </Tooltip>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 text-xs"
                    aria-label="Track changes"
                    disabled
                    disabledReason="Track changes: available once a workflow step is finished"
                  >
                    <FileDiff className="size-3.5" />
                  </Button>
                )}
                <Tooltip>
                  <TooltipTrigger render={
                    <Button
                      variant="outline"
                      aria-pressed={showWhitespace}
                      size="sm"
                      className="h-7 text-xs font-mono"
                      onClick={() => {
                        setShowWhitespace(v => {
                          const next = !v
                          try { localStorage.setItem('cat-show-whitespace', String(next)) } catch { /* storage blocked */ }
                          return next
                        })
                      }}
                    />
                  }>
                    ¶
                  </TooltipTrigger>
                  <TooltipContent>Toggle hidden characters and full tag names (spaces, tabs, newlines, &lt;sub&gt;)</TooltipContent>
                </Tooltip>
                <KeyboardShortcutsButton />
              </>
            }
          />

          {selection.selected.size > 0 && job && (
            <SegmentSelectionBar
              jobId={jid}
              projectId={pid}
              picked={selection.picked}
              selectedCount={selection.selected.size}
              translationsById={indexRowByTranslation}
              canConfirm={canEdit}
              canLock={canEdit && isManager}
              canUnlock={canEdit}
              canClear={canEdit && taskInClearWindow}
              canPretranslate={canEdit && inFirstStep && job.pipeline_status !== 'failed' && !isPipelineRunning(job)}
              qaDisabledReason={qaReport?.profile_assigned === false ? NO_QA_PROFILE_REASON : undefined}
              onJoin={canEdit ? () => void joinSelection() : undefined}
              joinRefusal={joinRefusal(rows, pickedRows)}
              beforeRun={() => switchActiveSegment(null)}
              onSpellCheck={() => { setSpellCheckScope('selected'); setSpellCheckOpen(true) }}
              onFindReplace={() => { setFindInSelection(true); setFindReplaceOpen(true) }}
              onClearSelection={selection.clear}
            />
          )}

          <div className="flex-1 flex min-h-0 gap-0">
            <div data-guide="editor.segments" className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 overflow-hidden flex flex-col min-h-0 flex-1 min-w-0">
              {/* Find & Replace bar — sits above segment grid, no backdrop/blur */}
              <FindReplaceModal
                open={findReplaceOpen}
                onClose={() => { setFindReplaceOpen(false); setFindHighlightTerm(null) }}
                jobId={jid}
                onHighlight={setFindHighlightTerm}
                onScrollToTranslation={scrollToTranslation}
                onDeactivate={() => void switchActiveSegment(null)}
                onComplete={({ taskReopened }) => {
                  queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jid) })
                  queryClient.invalidateQueries({ queryKey: queryKeys.job.detail(jid) })
                  if (taskReopened) handleTaskReopened(queryClient, pid, jid)
                }}
                onTaskLocked={err => handleTaskLocked(queryClient, pid, jid, err)}
                readOnly={!canEdit}
                selectedIds={selection.picked}
                initialInSelection={findInSelection}
                canReplaceLocked={isAdmin}
              />
              <div ref={segmentScrollRef} data-testid="segment-grid" className="flex-1 overflow-y-auto overflow-x-hidden" style={segmentColumnsStyle({ extrasWidth, widths: columnDrag.widths, overlayScrollbar })}>
              {/* Header is a sticky row INSIDE the scroll container so it shares
                  the same scrollbar-reduced width as the body rows (see
                  headerHeight/scrollMargin above). z-20 keeps it above the
                  active row (zIndex 10); its background is opaque so rows scroll
                  cleanly beneath it. */}
              <div ref={headerRef} className={`${SEGMENT_GRID_COLS} sticky top-0 z-20 ${tableHeaderRowClass}`}>
                {SEGMENT_HEADER_CELLS.map((cell, i) => (
                  <div
                    key={i}
                    className={cn(
                      'relative',
                      cell.className,
                      // A visible line on every draggable border, like the
                      // project Tasks tab's header.
                      i < lastHeader && 'border-r border-gray-200 dark:border-gray-700',
                    )}
                  >
                    {i === 0 ? (
                      <SelectAllHeader state={selection.header} onToggle={selection.toggleAll} disabled={orderedTranslationIds.length === 0} />
                    ) : cell.label}
                    {/* The border after every visible column but the last: drag
                        to move it, double-click to restore the default widths. */}
                    {i < lastHeader && isResizableBorder(i, extrasWidth > 0) && (
                      <ColumnResizeHandle
                        active={columnDrag.dragging === i}
                        onPointerDown={columnDrag.startDrag(i)}
                        onDoubleClick={columnDrag.reset}
                        title="Drag to resize. Double-click to reset."
                        data-testid={`segment-column-resize-${i}`}
                      />
                    )}
                  </div>
                ))}
              </div>
              <div style={{ height: `${rowVirtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }}>
                {rowVirtualizer.getVirtualItems().map(virtualRow => {
                  const idx = filteredIndices[virtualRow.index]
                  const indexRow = rows[idx]
                  const loaded = rowAt(idx)
                  const seg = loaded?.segment
                  const translation = loaded?.translation ?? undefined
                  const tid = indexRow.translationId ?? -1
                  return (
                    <div
                      key={indexRow.segmentId}
                      // Dynamic measurement. `estimateSize` is only a
                      // first guess; multi-line rows grow past it, so ref +
                      // data-index opt the row into TanStack Virtual's
                      // auto-measurement loop to keep row N+1 from overlapping.
                      ref={rowVirtualizer.measureElement}
                      data-index={virtualRow.index}
                      // Stable anchor for the scroll-restoration
                      // settle loop in the activeSegmentIdx effect — uses
                      // boundingClientRect of the row's DOM node, which
                      // reflects the current layout even while measureElement
                      // refs are still flushing.
                      data-segment-idx={idx}
                      // Capture position on mousedown (the FIRST
                      // event in the click sequence — fires before blur on
                      // the previously-active editor, which would otherwise
                      // trigger saveDraft and shift layout BEFORE the React
                      // synthetic click reaches our handler).
                      onMouseDownCapture={activeSegmentIdx === idx ? undefined : (e) => {
                        if (!isSelectToggleClick(e)) captureAnchorBeforeActivate(idx)
                      }}
                      // Cmd/Ctrl+click adds the row to the selection (or takes
                      // it out) instead of opening it.
                      onClickCapture={(e) => {
                        if (indexRow.translationId == null || !isSelectToggleClick(e)) return
                        e.preventDefault()
                        e.stopPropagation()
                        selection.toggle(indexRow.translationId)
                      }}
                      style={{
                        position: 'absolute',
                        top: 0,
                        left: 0,
                        width: '100%',
                        // virtualRow.start includes scrollMargin (the sticky
                        // header height); the spacer already sits below the
                        // header in flow, so subtract it to avoid double-counting.
                        transform: `translateY(${virtualRow.start - rowVirtualizer.options.scrollMargin}px)`,
                        transition: 'transform 150ms ease-out',
                        zIndex: activeSegmentIdx === idx ? 10 : 0,
                      }}
                    >
                      {!seg ? <SegmentRowPlaceholder number={segmentNumber(rows[idx])} /> : (
                      <SegmentRow
                        segment={seg}
                        translation={translation}
                        number={segmentNumber(rows[idx])}
                        onSplit={activeSegmentIdx === idx && canEdit && tid > 0
                          ? (offset, version) => splitJoin.split.mutate({ translation_id: tid, offset, expected_updated_at: version })
                          : undefined}
                        onJoinNext={activeSegmentIdx === idx && canEdit && tid > 0 && joinsWithNext(rows, idx)
                          ? version => splitJoin.joinTranslations([tid, rows[idx + 1].translationId ?? 0], { [tid]: version })
                          : undefined}
                        jobId={jid}
                        fileId={job?.file_id ?? 0}
                        projectId={job?.project_id ?? null}
                        targetLanguage={job?.target_language ?? ''}
                        sourceLanguage={job?.source_language ?? ''}
                        isActive={activeSegmentIdx === idx}
                        // virtualRow.start counts from the top of the scroll
                        // content, header included: the row's top below the
                        // header's bottom edge.
                        actionsBelow={activeSegmentIdx === idx && virtualRow.start - (rowVirtualizer.scrollOffset ?? 0) - headerHeight < ACTION_BAR_REACH}
                        onActivate={caret => void switchActiveSegment(idx, { caret })}
                        onNavigate={(dir, caret) => {
                          // Navigate within filtered results
                          const pos = filteredIndices.indexOf(idx)
                          if (pos === -1) return
                          goToSegment(dir === 'next' ? pos + 1 : pos - 1, caret)
                        }}
                        entryCaret={entryCaret?.idx === idx ? entryCaret.caret : undefined}
                        // Escape is the ONLY transition that discards (no flush).
                        // Every other path routes through switchActiveSegment(...) without `discard`.
                        onDeactivate={() => void switchActiveSegment(null, { discard: true })}
                        dirtyRef={activeRowDirtyRef}
                        onTermsLoaded={activeSegmentIdx === idx ? setActiveTerms : undefined}
                        onEditTextChange={activeSegmentIdx === idx ? setActiveEditText : undefined}
                        insertRef={activeSegmentIdx === idx ? activeInsertRef : undefined}
                        flushDraftRef={activeSegmentIdx === idx ? flushDraftRef : undefined}
                        readOnly={!canEdit}
                        pretranslating={pipelineRunning}
                        qaIssues={qaResultsByTranslation.get(tid) ?? NO_ISSUES}
                        showWhitespace={showWhitespace}
                        refTerms={refTerms}
                        spellCheck
                        spellFixActions={spellFixActions}
                        findHighlightTerm={findHighlightTerm}
                        tmDiffReference={activeSegmentIdx === idx ? tmDiffReference : undefined}
                        currentUserId={currentUserId}
                        inClearWindow={taskInClearWindow}
                        isRepetition={indexRow.repetition === REPETITION_REPEAT}
                        isRepetitionSource={indexRow.repetition === REPETITION_SOURCE}
                        commentCount={commentCountByTranslation.get(tid) ?? 0}
                        isSelected={selection.selected.has(tid)}
                        onSelectClick={selection.onCheckboxClick}
                        heldByName={heldByOthers.get(tid) ?? null}
                        autoApprovedBeforeStep={autoApprovedBefore.has(tid)}
                        predictedBeforeStep={predictedBeforeStep.has(tid)}
                        trackChanges={showTrackChanges}
                        trackChangesFrom={showTrackChanges ? loaded?.previous_step_text ?? null : null}
                        placeables={loaded?.placeables}
                        plainText={plainTextFile}
                        onQARefresh={(translationId, issues) => {
                          void patchQAReport(queryClient, qaReportKey, translationId, issues)
                        }}
                      />
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          </div>
          {/* Drag handle for the panel's width. Kept in flow (not absolute) so
              the grid and the panel simply share what's left of the row. */}
          {sidebarOpen && (
            <div
              {...sidebarResize.separatorProps}
              data-testid="resize-sidebar"
              title="Drag to resize — double-click to reset"
              className="group w-2 shrink-0 flex justify-center cursor-col-resize focus-visible:outline-none"
            >
              {/* 2px at full primary: a 1px half-transparent hairline reads as
                  nothing at all on either theme. */}
              <span
                className={cn(
                  'w-0.5 h-full rounded-full transition-colors group-hover:bg-primary group-focus-visible:bg-primary',
                  sidebarResize.isResizing && 'bg-primary',
                )}
              />
            </div>
          )}
          <div
            className={cn('overflow-hidden shrink-0', !sidebarResize.isResizing && 'transition-all duration-200 ease-in-out')}
            style={{
              width: sidebarOpen ? `${sidebarResize.width}px` : '0px',
              opacity: sidebarOpen ? 1 : 0,
            }}
          >
            {sidebarOpen && (
              <EditorSidebar
                jobId={jid}
                sourceText={activeSourceText}
                editText={activeEditText}
                terms={activeTerms}
                references={activeReferences}
                onJumpToReference={refKey => void jumpToReference(refKey)}
                onInsertMatch={handleSidebarInsert}
                plainText={plainTextFile}
                onAddTerm={quickAddOpen ? openQuickAddTerm : undefined}
                onTermClick={handleTermClick}
                onTermHover={handleTermHover}
                onClose={handleSidebarClose}
                onMatchHover={setHoveredTMSource}
                translationId={activeTranslationId ?? null}
                qaIssues={activeTranslationId ? qaResultsByTranslation.get(activeTranslationId) ?? [] : []}
                onQARefresh={(translationId, issues) => {
                  void patchQAReport(queryClient, qaReportKey, translationId, issues)
                }}
              />
            )}
          </div>
          {/* Collapsed handle — the reopen affordance once the sidebar is
              closed, sitting right where the panel was. Click it, or drag it
              leftwards to pull the panel back out at the width you want. */}
          {!sidebarOpen && (
            // eslint-disable-next-line no-restricted-syntax -- open-sidebar launcher, not an action button
            <button
              onClick={openSidebarByClick}
              {...sidebarResize.reopenDragProps}
              data-testid="open-sidebar"
              title="Open TM & terminology sidebar — drag to set its width"
              // The open panel carries the same anchor; only one renders.
              data-guide="editor.sidebar"
              className="group relative ml-2 self-stretch flex flex-col items-center justify-center gap-2 px-1.5 rounded-lg border border-gray-200 dark:border-gray-800 bg-card text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors cursor-col-resize"
            >
              {/* Same grab line the open panel's handle shows, on the edge the
                  drag starts from — the gesture is the same one. */}
              <span
                aria-hidden
                className={cn(
                  '-left-1 absolute inset-y-0 w-0.5 rounded-full transition-colors group-hover:bg-primary group-focus-visible:bg-primary',
                  sidebarResize.isResizing && 'bg-primary',
                )}
              />
              <ChevronLeft className="size-4" />
              <span className="text-[10px] font-medium uppercase tracking-wider [writing-mode:vertical-rl] rotate-180">Resources</span>
            </button>
          )}
          </div>
          </>
        )}
      </PageContainer>
      {/* The dialogs below live at page level, NOT inside the loaded-segments
          branch: the toolbar buttons that open them render outside every
          loading/locked conditional, so a dialog mounted inside one would make
          the click a silent no-op while translations load or while a QE+APE
          step holds the editor locked. */}
      <SpellCheckDialog
        open={spellCheckOpen}
        onClose={() => setSpellCheckOpen(false)}
        jobId={jid}
        spellFixActions={spellFixActions}
        segmentIds={spellSegmentIds}
        filteredIndices={filteredIndices}
        translationBySegment={spellRowsBySegment}
        filterActive={isFilterActive(segmentFilters)}
        selectedIds={pickedSet}
        initialScope={spellCheckScope}
        // Flushes the open segment's draft AND deactivates it, so the batch
        // writes can't race an editor autosave.
        beforeRun={() => switchActiveSegment(null)}
        // activate: false — scroll the row into view behind the modal without
        // mounting an editor on it.
        onNavigate={id => void scrollToTranslation(id, { activate: false })}
        onApply={async (error, replacement, includeLocked) =>
          (await applySpellFixes([error], replacement, includeLocked)) > 0}
        onApplyAll={applySpellFixes}
        readOnly={!canEdit}
      />

      <ConcordanceModal
        open={concordanceOpen}
        onClose={() => setConcordanceOpen(false)}
        projectId={job?.project_id ?? null}
        sourceLanguage={job?.source_language ?? ''}
        targetLanguage={job?.target_language ?? ''}
        readOnly={!canEdit || activeRowLocked || activeRowHeld}
        plainText={plainTextFile}
        onInsert={(text) => {
          if (activeSegmentIdx === null) return
          activeInsertRef.current.replaceAll?.(text)
        }}
        initialQuery={concordanceQuery}
      />

      {job && (
        <QuickAddTermDialog
          open={quickAddTerm !== null}
          onClose={() => {
            setQuickAddTerm(null)
            activeInsertRef.current.clearSourceSelection?.()
          }}
          jobId={jid}
          projectId={job.project_id}
          isManager={isManager}
          initialSource={quickAddTerm?.source ?? ''}
          initialTarget={quickAddTerm?.target ?? ''}
        />
      )}

      {/* Jump-to-segment dialog (Ctrl+G) */}
      <Dialog open={jumpOpen} onOpenChange={setJumpOpen}>
        <DialogContent className="sm:max-w-xs">
          <DialogHeader>
            <DialogTitle>Jump to segment</DialogTitle>
          </DialogHeader>
          <form onSubmit={(e) => {
            e.preventDefault()
            // The number the grid shows ("12", or "12.1" for a piece of a
            // split), also with a filter on.
            const typed = jumpInput.trim()
            const idx = rowForNumber(rows, typed)
            if (idx === undefined) {
              toast.info(`There is no segment ${typed}.`)
              return
            }
            setJumpOpen(false)
            void scrollToRow(idx).then(shown => {
              if (!shown) toast.info(`Segment ${typed} is hidden by the filter.`)
            })
          }}>
            <Input
              inputMode="decimal"
              value={jumpInput}
              onChange={(e) => setJumpInput(e.target.value)}
              placeholder={rows.length > 0 ? `1–${segmentNumber(rows[rows.length - 1])}` : ''}
              autoFocus
              className="mb-3"
            />
            <div className="flex justify-end">
              <Button type="submit" size="sm" disabled={!jumpInput.trim()}>
                Go
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      {/* Step Completion Dialog — confirmation before advancing workflow */}
      <CompleteStepDialog
        open={completeDialogOpen}
        onOpenChange={setCompleteDialogOpen}
        stepName={task.step?.step_name ?? 'Step'}
        isFinalStep={isFinalStep}
        approvedCount={job ? job.counts.approved : null}
        unapproved={unapproved}
        canApproveAll={canEdit}
        approveBlocked={approveBlocked}
        pending={advanceMut.isPending || approveAllMut.isPending}
        onConfirm={comment => advanceMut.mutate(comment)}
        onApproveAll={comment => { setApproveBlocked(null); approveAllMut.mutate(comment) }}
      />

      {jobSteps && currentStepIndex >= 0 && (
        <RejectStepDialog
          open={rejectDialogOpen}
          onOpenChange={setRejectDialogOpen}
          steps={jobSteps}
          currentIndex={currentStepIndex}
          isManager={isManager}
          pending={rejectMut.isPending}
          onConfirm={input => rejectMut.mutate(input)}
        />
      )}

      {/* Always-visible live leverage breakdown, fixed to the viewport
          bottom, VSCode-style. The page's LEVERAGE_BAR_CLEARANCE keeps
          everything above it. */}
      <JobLeverageStatusBar jobId={jid} localRepetitionStats={editorIndex.repetitionStats} segmentCount={segmentCount} wordCount={job?.word_count ?? 0} />
    </TooltipProvider>
  )
}

// How far the open row's action bar reaches above the row: half its height
// (SegmentRowActionBar, h-4), as it sits centred on its box's top border.
const ACTION_BAR_REACH = 8

// Header cells, in SEGMENT_GRID_COLS order. Match, Status and the unlabelled
// extras column use px-2, like their body cells.
const SEGMENT_HEADER_CELLS = [
  { label: '#', className: 'px-2 py-2' },
  { label: 'Source', className: 'px-3 py-2' },
  { label: 'Translation', className: 'px-3 py-2' },
  { label: 'Match', className: 'px-2 py-2 text-center' },
  { label: <span className="sr-only">Status</span>, className: 'px-2 py-2' },
  { label: null, className: '' },
]

// The last header with text. The unlabelled extras column after it shows only
// when some row has a lock, QE, QA or comment.
const LAST_LABELLED_HEADER = SEGMENT_HEADER_CELLS.reduce((last, cell, i) => (cell.label ? i : last), 0)

/** Cmd+click (Mac) / Ctrl+click elsewhere, outside the open editor's text. */
function isSelectToggleClick(e: ReactMouseEvent): boolean {
  const mod = isMac ? e.metaKey : e.ctrlKey
  return mod && !(e.target as HTMLElement | null)?.closest('.ProseMirror')
}

/** The # header: select or unselect every row the filter shows. */
function SelectAllHeader({ state, onToggle, disabled }: { state: 'none' | 'some' | 'all'; onToggle: () => void; disabled: boolean }) {
  return (
    <label className="flex cursor-pointer items-center gap-1.5 select-none">
      <SelectAllCheckbox
        checked={state === 'all'}
        indeterminate={state === 'some'}
        onChange={onToggle}
        disabled={disabled}
        aria-label="Select all segments shown"
        data-testid="segment-select-all"
      />
      #
    </label>
  )
}

function KeyboardShortcutsButton() {
  const shortcuts = [
    { keys: `${modKey}+Enter`, desc: 'Confirm segment & go to next' },
    { keys: 'Tab', desc: 'Next segment (without confirming)' },
    { keys: 'Shift+Tab', desc: 'Previous segment' },
    { keys: '↓ / ↑', desc: 'Next / previous segment, from the last / first line' },
    { keys: 'Shift+Enter', desc: 'Line break inside the segment' },
    { keys: 'Escape', desc: 'Exit editing' },
    { keys: `${modKey}+Z`, desc: 'Undo' },
    { keys: `${modKey}+Shift+S`, desc: 'Copy source to target' },
    { keys: `${modKey}+G`, desc: 'Jump to segment #' },
    { keys: `${modKey}+K`, desc: 'Concordance search — looks up the selected phrase' },
    { keys: `${modKey}+F`, desc: 'Search all segments' },
    { keys: `${modKey}+H`, desc: 'Find and replace' },
    { keys: `${modKey}+B`, desc: 'Bold' },
    { keys: `${modKey}+I`, desc: 'Italic' },
    { keys: `${modKey}+1..5`, desc: 'Insert TM match #1–5' },
    { keys: 'F8', desc: 'Insert next missing tag, placeable (number, URL, variable…) or term' },
    { keys: 'Alt+F8', desc: 'Insert all missing tags, placeables and terms' },
    { keys: `${modKey}+Shift+Backspace`, desc: 'Clear translation' },
    { keys: 'Alt+Shift+T', desc: 'Split the segment where you clicked in the source' },
    { keys: 'Alt+Shift+J', desc: 'Join with the next sentence or piece' },
    { keys: 'Alt+Shift+G', desc: 'Add the selected words to the term base' },
    { keys: `${modKey}+Click`, desc: 'Add segment to selection, or remove it' },
    { keys: 'Shift+Click #', desc: 'Select a range of segments' },
    { keys: 'Escape', desc: 'Clear selection (when not editing)' },
  ]

  return (
    <Dialog>
      <Tooltip>
        <TooltipTrigger render={
          <DialogTrigger
            render={
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Keyboard shortcuts"
              />
            }
          />
        }>
          <Keyboard className="size-4" />
        </TooltipTrigger>
        <TooltipContent>Keyboard shortcuts</TooltipContent>
      </Tooltip>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="-mx-1 mt-2 min-h-0 flex-1 space-y-3 overflow-y-auto px-1" data-testid="keyboard-shortcuts-list">
          {shortcuts.map(s => (
            <div key={s.keys} className="flex items-center justify-between">
              <span className="text-sm text-gray-600 dark:text-gray-400">{s.desc}</span>
              <kbd className="px-2 py-1 text-xs font-mono bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded">{s.keys}</kbd>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
