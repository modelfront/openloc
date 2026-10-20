import { errorText } from '@/lib/error-text'
import { useState, useRef, useEffect, useLayoutEffect, useCallback, useMemo } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { ChevronLeft, Keyboard, Loader2Icon, Upload } from 'lucide-react'
import { useParams, useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { defaultRangeExtractor, useVirtualizer, type Range } from '@tanstack/react-virtual'
import {
  listSegments,
  listJobTranslations,
  completeJob,
  getJob,
  runQA,
  getQAReport,
  listJobSteps,
  getJobStepStatuses,
  advanceJobStep,
  confirmAllTranslations,
  rejectJobStep,
  type RejectStepInput,
  getCommentCounts,
  type Translation,
  type TermLookupResult,
  type QAReport,
} from '../api'
import { getMe } from '../api/auth'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext, invalidateProjectStatus, invalidateTMContext } from '../lib/query-invalidation'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { useStableArray } from '../hooks/useStableArray'
import { useJobPipelineActions } from '../hooks/useJobPipelineActions'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { isPipelineRunning, isAIStepRunning, pipelineBadge, stoppedNote } from '../lib/project-logic'
import { canEditTask, canRerunTask, canSkipAIStep, currentTask } from '../lib/current-task'
import { handleTaskLocked, handleTaskReopened, isTaskLockedError } from '../lib/task-edit'
import { unapprovedCounts, blockedSegmentNumbers, type ApproveBlocked } from '../lib/step-completion'
import type { SegmentBulkResult } from '../lib/segment-bulk'
import { computeRepetitionSegmentIds, computeRepetitionSourceIds, computeRepetitionStats } from '../lib/cat-editor-logic'
import { writeSpellFixes } from '../lib/spellcheck-write'
import type { SpellError, SpellRunScope } from '../lib/spellcheck-run'
import { qaButtonLabel, qaRunToast, patchQAReport, groupFindingsByTranslation } from '../lib/qa-findings'
import {
  countScope, selectScopeTargets, wordOffsets, scopeIncludesLocked, DEFAULT_RUN_SCOPE,
  type SpellFixActions, type SpellFixScope,
} from '../lib/spellcheck-scope'
import { addSpellcheckDictionaryWord } from '../api'
import SpellCheckDialog from '../components/SpellCheckDialog'
import ConcordanceModal from '../components/ConcordanceModal'
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
import { SegmentRow, SEGMENT_GRID_COLS, isMac, modKey } from '../components/SegmentRow'
import { qeChipLabel } from '../lib/qe-chip'
import { extrasColumnWidth, segmentColumnMins, segmentColumnsStyle, statusColumnWidth } from '../lib/segment-columns'
import { showStatusChip, isAssistantConfirmed, ASSISTANT_CONFIRMED_LABEL, type DisplayStatus } from '../lib/segment-status'
import { statusLabel } from '../components/SegmentRow.shared'
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
import JobLeverageStatusBar from '../components/JobLeverageStatusBar'
import { writeStorage } from '../lib/safe-storage'
import { panelInsertText, type PanelInsert } from '../lib/panel-insert'
import {
  collectRefKeys, segmentRefKeys, indexSegments, indexTargets, refTermsSignature, buildRefTerms,
} from '../lib/ref-terms'

/** Width the Resources panel opens at on a click, and its starting width. */
const SIDEBAR_CLICK_WIDTH = 200

export default function CATEditor() {
  const { projectId, jobId } = useParams<{ projectId: string; jobId: string }>()
  const pid = Number(projectId)
  const jid = Number(jobId)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [activeSegmentIdx, setActiveSegmentIdx] = useState<number | null>(null)
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
  const activeInsertRef = useRef<{
    selectAndReplace: (src: string, tgt: string, mt?: string) => void
    selectTerm: (src: string, tgt: string, mt?: string) => void
    replaceAll?: (text: string, insertToken?: string) => void
    confirm?: () => void
    copySource?: () => void
    clear?: () => void
  }>({ selectAndReplace: () => {}, selectTerm: () => {} })
  const flushDraftRef = useRef<() => Promise<void>>(async () => {})

  // Modals
  const [concordanceOpen, setConcordanceOpen] = useState(false)
  const [findReplaceOpen, setFindReplaceOpen] = useState(false)
  // Whether Find & replace opens limited to the segment selection (its bar button).
  const [findInSelection, setFindInSelection] = useState(false)
  const [findHighlightTerm, setFindHighlightTerm] = useState<string | null>(null)
  const [showWhitespace, setShowWhitespace] = useState(() => { try { return localStorage.getItem('cat-show-whitespace') === 'true' } catch { return false } })
  const [segmentFilters, setSegmentFilters] = useState<SegmentFilters>(DEFAULT_FILTERS)

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

  const [wasProcessing, setWasProcessing] = useState(false)

  // Project context (id, name, source language) comes off the job response.
  // `GET /api/projects` is manager-only, so linguists can't read it.
  const { data: job, isPending: jobPending } = useQuery({
    queryKey: queryKeys.job.detail(jid),
    queryFn: () => getJob(jid),
    refetchInterval: (query) => {
      const j = query.state.data
      if (!j) return false
      // Poll while pipeline is running in the background.
      // isPipelineRunning is current_step-aware, so polling stops once the
      // pipeline writes a "*  complete" label, even though pipeline_status
      // stays 'in_progress' until /complete fires.
      return isPipelineRunning(j) ? 2000 : false
    },
  })
  usePageTitle(job?.file_name, job?.project_name)

  const pipelineRunning = !!job && isPipelineRunning(job)
  // While this job's QE+APE step runs, the editor is
  // hard-locked — we show a waiting screen instead of the segment grid, and the
  // backend also rejects edits with 409. The existing finish effect below
  // invalidates translations when pipelineRunning flips false, so the grid
  // loads automatically once QE+APE completes.
  const aiRunning = !!job && isAIStepRunning(job)
  const runningBadge = job ? pipelineBadge(job) : null
  const jobStoppedNote = job ? stoppedNote(job) : null

  // When pipeline finishes, do a single atomic refetch of translations — and of
  // the steps, since an AI step that ran may have completed or moved the task.
  useEffect(() => {
    if (pipelineRunning) {
      setWasProcessing(true)
    } else if (wasProcessing) {
      setWasProcessing(false)
      queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jid) })
      queryClient.invalidateQueries({ queryKey: queryKeys.job.steps(jid) })
    }
  }, [pipelineRunning, wasProcessing, jid, queryClient])

  const { data: segments, isPending: segmentsPending } = useQuery({
    queryKey: queryKeys.segments.byFile(job?.file_id ?? 0),
    queryFn: () => listSegments(job!.file_id),
    enabled: !!job,
  })

  const { data: translations, isPending: translationsPending } = useQuery({
    queryKey: queryKeys.job.translations(jid),
    queryFn: () => listJobTranslations(jid),
    enabled: !!job,
  })

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
      setSegmentFilters(f => (f.qaFilter === 'all' ? f : { ...f, qaFilter: 'all' }))
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
  const canEdit = canEditTask(task.mode)
  const canEditRef = useRef(canEdit)
  canEditRef.current = canEdit
  // "Strings contain HTML" off for this file: every insert goes in as plain text.
  const plainTextFile = job?.file_inline_html === false
  const plainTextFileRef = useRef(plainTextFile)
  plainTextFileRef.current = plainTextFile

  // The page stays on one loading screen until everything its first paint
  // depends on is in: the job, its steps, segments, translations and the user.
  // Each piece rendering as it arrives reshuffles the page several times on a
  // slow connection. A failed query stops pending, so an error never hangs here.
  const editorLoading = mePending || jobPending
    || (!!job && (jobStepsPending || segmentsPending || translationsPending))
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
      const position = new Map<number, number>()
      segments?.forEach((s, i) => {
        const id = translationMap.get(s.id)?.id
        if (id !== undefined) position.set(id, i)
      })
      const blocked = blockedSegmentNumbers(res.skipped, id => position.get(id))
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

  // A Resources-panel insert: the click on a TM match or MT suggestion, and
  // Ctrl+1..5. Refs are stable, so no dependencies.
  const handleSidebarInsert = useCallback((insert: PanelInsert) => {
    if (!canEditRef.current) return
    activeInsertRef.current.replaceAll?.(panelInsertText(insert, activeSourceRichRef.current, plainTextFileRef.current), insert.insertToken)
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
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [openConcordance, handleSidebarInsert])

  // Workflow-aware toolbar state, from the current task. Per-button gating
  // (TM / MT / Predict) lives in JobPipelineActions, which is told the
  // first-step answer because the steps list is loaded here.
  const isFinalStep = !!task.step && task.step.step_order === (jobSteps?.length ?? 1) - 1
  const currentStepIndex = task.step ? (jobSteps?.findIndex(s => s.id === task.step!.id) ?? -1) : -1
  const inFirstStep = !task.step || task.step.step_order === 0

  // Job-wide derived data, memoized on the queries: a render that changes
  // neither (a column drag fires one per pointer move) must not walk every
  // segment again — and a stable translationMap keeps filteredIndices memoized.
  // What Complete step would leave unapproved: drafts (any text) and untranslated.
  const unapproved = useMemo(() => unapprovedCounts(translations ?? []), [translations])

  // Build segment→translation map
  const translationMap = useMemo(() => {
    const map = new Map<number, Translation>()
    translations?.forEach(t => map.set(t.segment_id, t))
    return map
  }, [translations])
  const statusCounts = useMemo(
    () => countByDisplayStatus(segments ?? [], translationMap, predictedBeforeStep),
    [segments, translationMap, predictedBeforeStep],
  )
  // Status column: wide enough for the widest status chip this job shows.
  const statusWidth = useMemo(() => {
    const labels = (Object.entries(statusCounts) as [DisplayStatus, number][])
      .filter(([s, n]) => n > 0 && showStatusChip(s))
      .map(([s]) => statusLabel(s))
    if (translations?.some(isAssistantConfirmed)) labels.push(ASSISTANT_CONFIRMED_LABEL)
    if (autoApprovedBefore.size > 0) labels.push(statusLabel('auto_approved'))
    return statusColumnWidth(labels)
  }, [statusCounts, translations, autoApprovedBefore])

  const qaResultsByTranslation = useMemo(() => groupFindingsByTranslation(qaReport?.results), [qaReport])

  // Width of the unnamed extras column: the widest row in the whole job, not
  // just the filtered or rendered rows, so filtering doesn't resize it.
  const extrasWidth = useMemo(() => extrasColumnWidth((translations ?? []).map(t => ({
    locked: t.locked === true,
    qeLabel: qeChipLabel(t.qe_note),
    hasQA: (qaResultsByTranslation.get(t.id)?.length ?? 0) > 0,
    commentCount: commentCountByTranslation.get(t.id) ?? 0,
  }))), [translations, qaResultsByTranslation, commentCountByTranslation])

  // Filtered segment indices (client-side filtering)
  // Stable while the visible rows and their order stay the same: a save
  // rebuilds translationMap, and a new array here would make the row
  // virtualizer recompute every row position (its getItemKey depends on it).
  const filteredIndices = useStableArray(useMemo(() => {
    if (!segments) return []
    return filterSegments(segments, translationMap, qaResultsByTranslation, segmentFilters, commentCountByTranslation, predictedBeforeStep)
  }, [segments, translationMap, qaResultsByTranslation, segmentFilters, commentCountByTranslation, predictedBeforeStep]))

  // Repetition detection — two parallel sets:
  // `repetitionSourceIds` = FIRST occurrences that have duplicates → solid
  // "→R" chip ("confirming this propagates to others")
  // `repetitionSegmentIds` = SUBSEQUENT occurrences → dashed "⇢R" chip
  // ("you were auto-propagated from earlier")
  // The two sets are disjoint by construction; segments with unique sources
  // belong to neither.
  const repetitionSegmentIds = useMemo(
    () => computeRepetitionSegmentIds(segments),
    [segments],
  )
  const repetitionSourceIds = useMemo(
    () => computeRepetitionSourceIds(segments),
    [segments],
  )
  // Aggregate repetition stats for the leverage status bar's "Repetitions"
  // band. Backend's `repetitions_*` columns are always 0 (deferred per
  // migration 042); the bar component takes this prop and overrides the
  // band when present.
  const repetitionStats = useMemo(
    () => computeRepetitionStats(segments),
    [segments],
  )

  const segmentCount = segments?.length ?? 0
  const filteredCount = filteredIndices.length

  // Multi-segment selection, by translation id, over the filtered rows.
  const orderedTranslationIds = useMemo(() => filteredIndices.flatMap(i => {
    const id = segments ? translationMap.get(segments[i].id)?.id : undefined
    return id == null ? [] : [id]
  }), [filteredIndices, segments, translationMap])
  const selection = useSegmentSelection(orderedTranslationIds)
  const selectionRef = useRef(selection)
  selectionRef.current = selection
  const translationsById = useMemo(() => new Map((translations ?? []).map(t => [t.id, t])), [translations])
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
    opts: { discard?: boolean } = {},
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
    // The ONLY legitimate setActiveSegmentIdx call site, gated by the flush invariant above.
    // eslint-disable-next-line no-restricted-syntax
    setActiveSegmentIdx(newIdx)
  }, [])

  // Navigate to next/previous within filtered results
  const goToSegment = useCallback((idx: number) => {
    if (idx >= 0 && idx < filteredCount) {
      void switchActiveSegment(filteredIndices[idx])
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
  // Clicking the closed panel's tab opens it narrow — enough for the TM and
  // terminology lines, leaving the grid the rest. Dragging that tab instead
  // chooses its own width.
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
  const columnMins = useMemo(() => segmentColumnMins(statusWidth), [statusWidth])
  const columnDrag = useColumnBorderDrag(headerRef, columnMins)
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
    estimateSize: (idx) => filteredIndices[idx] === activeSegmentIdx ? 120 : 65,
    // Measured heights are cached per segment, not per list position: a filter
    // or search moves segments to new positions, and a row that stays mounted
    // through that is never measured again.
    getItemKey: useCallback(
      (idx: number) => segments?.[filteredIndices[idx]]?.id ?? idx,
      [segments, filteredIndices],
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

  // Scroll to a specific translation by ID (used by Find & Replace navigation).
  // Explicitly scrolls the virtualizer — does NOT rely on the activeSegmentIdx effect,
  // which has "restore position" logic that fights programmatic navigation.
  // Only scrolls when the segment is off-screen or clipped; uses smooth behavior.
  const scrollToTranslation = useCallback(async (
    translationId: number,
    opts: { activate?: boolean } = {},
  ) => {
    const activate = opts.activate ?? true
    if (!segments) return
    const segIdx = segments.findIndex(s => translationMap.get(s.id)?.id === translationId)
    if (segIdx === -1) return
    const filteredPos = filteredIndices.indexOf(segIdx)
    if (filteredPos === -1) return
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
      if (!scrollEl) {
        rowVirtualizer.scrollToIndex(filteredPos, { align: 'center', behavior: 'smooth' })
        return
      }
      // Check if the target row is already fully visible
      const items = rowVirtualizer.getVirtualItems()
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
      rowVirtualizer.scrollToIndex(filteredPos, { align: 'auto', behavior: 'smooth' })
    })
  }, [segments, translationMap, filteredIndices, rowVirtualizer, switchActiveSegment])

  // --- Spell check review dialog ---
  //
  // Every correction goes through the bulk draft endpoint, including the segment
  // the user last had open: `beforeRun` deactivates it first, so there's one
  // write path instead of an editor branch racing its own autosave against the
  // batch writes.
  const translationById = useMemo(() => {
    const map = new Map<number, Translation>()
    translations?.forEach(t => map.set(t.id, t))
    return map
  }, [translations])

  const sourceTextBySegment = useMemo(() => {
    const map = new Map<number, string>()
    segments?.forEach(s => map.set(s.id, s.source_text))
    return map
  }, [segments])

  const spellSegmentIds = useMemo(() => segments?.map(s => s.id) ?? [], [segments])

  // Cross-reference terms. Built in two stages on purpose: the
  // signature depends only on the REFERENCED segments' targets, so the map
  // keeps its identity while unrelated segments are edited. SegmentRow's memo
  // compares it by reference, and the grid is virtualized — a map that churned
  // per keystroke would re-render every visible row. See lib/ref-terms.ts.
  const refKeys = useMemo(() => collectRefKeys(segments), [segments])
  const segIndex = useMemo(() => indexSegments(segments), [segments])
  const targetsBySegment = useMemo(() => indexTargets(translations), [translations])
  const refSignature = useMemo(
    () => refTermsSignature(refKeys, targetsBySegment, segIndex.idByKey),
    [refKeys, targetsBySegment, segIndex],
  )
  const refTerms = useMemo(
    () => buildRefTerms(refKeys, targetsBySegment, segIndex.idByKey, segIndex.sourceByKey),
    // Identity is deliberately keyed on the signature, not the raw
    // translations array.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [refSignature],
  )

  // Jump to the segment a cross-reference points at: resolve the
  // decoded ref key to the defining segment's translation and navigate via the
  // same helper F&R uses. Called from the sidebar's References section — the
  // chips themselves are deliberately not clickable (a jump on plain click
  // teleported linguists away when they merely meant to activate the row).
  // Plain function, not useCallback: the sidebar comparator ignores callbacks,
  // and it closes over translationMap, which is rebuilt per render anyway.
  const jumpToReference = (refKey: string) => {
    const segId = segIndex.idByKey.get(refKey)
    const trans = segId !== undefined ? translationMap.get(segId) : undefined
    if (trans) void scrollToTranslation(trans.id)
  }

  // The active segment's cross-references for the sidebar's References section:
  // the term each chip renders + the row that defines it. Identity is stable
  // across unrelated edits (deps change only on segment switch or when a
  // referenced target changes via refTerms' signature-keyed identity), which is
  // what the sidebar's memo comparator relies on.
  const activeReferences = useMemo<SegmentReference[]>(() => {
    if (activeSegmentIdx === null || !segments) return []
    const keys = segmentRefKeys(segments[activeSegmentIdx]?.source_rich_text)
    return keys.map(key => {
      const segId = segIndex.idByKey.get(key)
      const idx = segId !== undefined ? segments.findIndex(s => s.id === segId) : -1
      const term = refTerms.get(key)
      return {
        refKey: key,
        source: term?.source ?? '',
        target: term?.target ?? '',
        segmentNumber: idx >= 0 ? idx + 1 : null,
      }
    })
  }, [activeSegmentIdx, segments, segIndex, refTerms])
  // Mirror the active segment's tagged source into a ref so the TM insert
  // paths (sidebar click + Mod+1..5) can re-anchor tags without re-subscribing.
  activeSourceRichRef.current =
    activeSegmentIdx !== null && segments ? segments[activeSegmentIdx]?.source_rich_text ?? '' : ''

  const activeSourceText =
    activeSegmentIdx !== null && segments ? segments[activeSegmentIdx]?.source_text ?? '' : ''

  // Same hook + query key the sidebar's TM section uses, so this shares its
  // request rather than issuing a second one.
  const activeTranslationId = useMemo(() => {
    if (activeSegmentIdx == null || !segments) return undefined
    return translationMap.get(segments[activeSegmentIdx]?.id ?? -1)?.id
  }, [activeSegmentIdx, segments, translationMap])

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

  /**
   * Apply spell corrections through the bulk draft endpoint.
   *
   * Routed through `useMutationWithToast` per the project convention; the success
   * message is a function of the result because the count isn't known until the
   * write returns. Conflict / unfixable warnings are raised inside the mutation
   * because they're partial-success notes, not failures. Resolves null when
   * the task refused the write, which handleTaskLocked has already reported.
   */
  const spellFixMut = useMutationWithToast<number | null, {
    errors: SpellError[]
    replacement: string
    includeLocked: boolean
  }>({
    mutationFn: ({ errors, replacement, includeLocked }) => writeSpellFixes({
      fileId: job?.file_id ?? 0,
      errors,
      replacement,
      includeLocked,
      lookup: id => translationById.get(id),
      onTaskLocked: err => handleTaskLocked(queryClient, pid, jid, err),
      onTaskReopened: () => handleTaskReopened(queryClient, pid, jid),
    }),
    successMessage: applied =>
      applied === null ? null : `Corrected ${applied} segment${applied === 1 ? '' : 's'}`,
    errorMessage: 'Could not apply the correction',
    // One call covers the job's translations, detail and analysis caches.
    onSuccess: () => invalidateJobContext(queryClient, pid, jid),
  })

  const applySpellFixes = useCallback(async (
    errors: SpellError[], replacement: string, includeLocked: boolean,
  ): Promise<number> => {
    if (errors.length === 0) return 0
    return (await spellFixMut.mutateAsync({ errors, replacement, includeLocked })) ?? 0
  }, [spellFixMut])

  // --- Word-level fixes from the inline right-click menu ---
  //
  // The open segment is fixed through the editor itself (see RichTextCellEditor),
  // so these only cover the OTHER segments — writing the open row underneath a
  // mounted editor would race its autosave.
  // Segment holds: the open row is held for this user while they may edit it;
  // a row someone else holds is read-only here and names them. It follows
  // activeTranslationId, which changes only after switchActiveSegment's flush.
  const activeRowLocked = activeSegmentIdx != null && segments
    ? translationMap.get(segments[activeSegmentIdx]?.id ?? -1)?.locked === true
    : false
  const { heldByOthers, streamLive: holdStreamLive } = useSegmentHold({
    jobId: jid,
    translationId: activeTranslationId ?? null,
    canHold: canEdit && !activeRowLocked,
    currentUserId,
    onIdle: () => { void switchActiveSegment(null) },
  })
  const activeRowHeld = activeTranslationId != null && heldByOthers.has(activeTranslationId)

  const spellFixActions = useMemo<SpellFixActions>(() => ({
    // The server writes through a lock for owners and admins only — the same
    // tier that may unlock — so both spell check surfaces offer the all scope
    // to them alone.
    canWriteLocked: isAdmin,
    counts: word => countScope(translations ?? [], word),

    applyScope: async (word, replacement, scope: SpellFixScope) => {
      const targets = selectScopeTargets(translations ?? [], word, scope, activeTranslationId)
      if (targets.length === 0) return 0

      // Synthesize one finding per whole-word occurrence and reuse the same
      // tag-safe builder the review flow uses, rather than hand-rolling string
      // surgery over rich text here.
      const errors: SpellError[] = []
      for (const t of targets) {
        for (const offset of wordOffsets(t.active_text ?? '', word)) {
          errors.push({
            translationId: t.id,
            segmentId: 0,
            segmentIndex: -1,
            offset,
            length: word.length,
            word,
            message: '',
            suggestions: [replacement],
            ruleCategory: '',
          })
        }
      }

      return applySpellFixes(errors, replacement, scopeIncludesLocked(scope))
    },

    ignoreInProject: async word => {
      if (!job?.project_id || !job?.target_language) return false
      try {
        await addSpellcheckDictionaryWord(job.project_id, job.target_language, word)
        toast.success(`"${word}" added to the project dictionary`)
        return true
      } catch (err) {
        toast.error(errorText(err, `Could not save "${word}"`))
        return false
      }
    },
  }), [isAdmin, translations, activeTranslationId, applySpellFixes, job?.project_id, job?.target_language])

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
      <PageContainer width="full" className="max-md:min-w-3xl" data-hold-stream={holdStreamLive ? 'live' : 'polling'}>
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
              <Chip color={runningBadge.color} className="animate-pulse">
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
            {job && <JobPipelineActions job={job} projectId={pid} inFirstStep={inFirstStep} canEdit={canEdit} hideComplete />}
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
        ) : (
          <>
          {/* Progress bar */}
          {translations && translations.length > 0 && (() => {
            const approved = translations.filter(t => t.status === 'approved' || t.status === 'auto_approved').length
            const pct = Math.round((approved / translations.length) * 100)
            const summary = `${pct}% approved (${approved}/${translations.length})`
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
            onChange={setSegmentFilters}
            totalCount={segmentCount}
            filteredCount={filteredCount}
            commentCountsUnavailable={commentCountsFailed}
            qaReportUnavailable={qaReportFailed}
            onOpenFindReplace={() => { setFindInSelection(false); setFindReplaceOpen(true) }}
            statusCounts={statusCounts}
            actions={
              <>
                <Tooltip>
                  <TooltipTrigger render={
                    <Button
                      variant={showWhitespace ? 'default' : 'outline'}
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
              translationsById={translationsById}
              canConfirm={canEdit}
              canLock={canEdit && isManager}
              canUnlock={canEdit}
              canClear={canEdit && isManager && inFirstStep}
              canPretranslate={canEdit && inFirstStep && job.pipeline_status !== 'failed' && !isPipelineRunning(job)}
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
              {/* pb-10 reserves ~40px so the last segment row can scroll past
                  the fixed JobLeverageStatusBar (~24-28px tall) at the
                  viewport bottom. Without this, the user can't see the last
                  segment fully — the bar overlays its bottom half even at
                  max scroll. */}
              <div ref={segmentScrollRef} className="flex-1 overflow-y-auto overflow-x-hidden pb-10" style={segmentColumnsStyle({ statusWidth, extrasWidth, widths: columnDrag.widths })}>
              {/* Header is a sticky row INSIDE the scroll container so it shares
                  the same scrollbar-reduced width as the body rows (see
                  headerHeight/scrollMargin above). z-20 keeps it above the
                  active row (zIndex 10); its background is opaque so rows scroll
                  cleanly beneath it. */}
              <div ref={headerRef} className={`${SEGMENT_GRID_COLS} sticky top-0 z-20 bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider`}>
                {SEGMENT_HEADER_CELLS.map((cell, i) => (
                  <div
                    key={i}
                    className={cn(
                      'relative',
                      cell.className,
                      // A visible line on every draggable border, like the
                      // project Tasks tab's header.
                      i < SEGMENT_HEADER_CELLS.length - 1 && 'border-r border-gray-200 dark:border-gray-700',
                    )}
                  >
                    {i === 0 ? (
                      <SelectAllHeader state={selection.header} onToggle={selection.toggleAll} disabled={orderedTranslationIds.length === 0} />
                    ) : cell.label}
                    {/* The border after every column but the last: drag to
                        move it, double-click to restore the default widths. */}
                    {i < SEGMENT_HEADER_CELLS.length - 1 && (
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
                  const actualIdx = filteredIndices[virtualRow.index]
                  const seg = segments![actualIdx]
                  const idx = actualIdx
                  return (
                    <div
                      key={seg.id}
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
                        const tid = translationMap.get(seg.id)?.id
                        if (tid == null || !isSelectToggleClick(e)) return
                        e.preventDefault()
                        e.stopPropagation()
                        selection.toggle(tid)
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
                      <SegmentRow
                        segment={seg}
                        translation={translationMap.get(seg.id)}
                        index={idx + 1}
                        jobId={jid}
                        fileId={job?.file_id ?? 0}
                        projectId={job?.project_id ?? null}
                        targetLanguage={job?.target_language ?? ''}
                        sourceLanguage={job?.source_language ?? ''}
                        isActive={activeSegmentIdx === idx}
                        onActivate={() => void switchActiveSegment(idx)}
                        onNavigate={(dir) => {
                          // Navigate within filtered results
                          const pos = filteredIndices.indexOf(idx)
                          if (pos === -1) return
                          if (dir === 'next') goToSegment(pos + 1)
                          else goToSegment(pos - 1)
                        }}
                        // Escape is the ONLY transition that discards (no flush).
                        // Every other path routes through switchActiveSegment(...) without `discard`.
                        onDeactivate={() => void switchActiveSegment(null, { discard: true })}
                        dirtyRef={activeRowDirtyRef}
                        onTermsLoaded={activeSegmentIdx === idx ? setActiveTerms : undefined}
                        onEditTextChange={activeSegmentIdx === idx ? setActiveEditText : undefined}
                        insertRef={activeSegmentIdx === idx ? activeInsertRef : undefined}
                        flushDraftRef={activeSegmentIdx === idx ? flushDraftRef : undefined}
                        readOnly={!canEdit}
                        qaIssues={translationMap.get(seg.id)?.id ? qaResultsByTranslation.get(translationMap.get(seg.id)!.id) ?? [] : []}
                        showWhitespace={showWhitespace}
                        refTerms={refTerms}
                        spellCheck
                        spellFixActions={spellFixActions}
                        findHighlightTerm={findHighlightTerm}
                        tmDiffReference={activeSegmentIdx === idx ? tmDiffReference : undefined}
                        currentUserId={currentUserId}
                        isAdmin={isAdmin}
                        canLock={canEdit && isManager}
                        inFirstStep={inFirstStep}
                        isRepetition={repetitionSegmentIds.has(seg.id)}
                        isRepetitionSource={repetitionSourceIds.has(seg.id)}
                        commentCount={commentCountByTranslation.get(translationMap.get(seg.id)?.id ?? -1) ?? 0}
                        isSelected={selection.selected.has(translationMap.get(seg.id)?.id ?? -1)}
                        onSelectClick={selection.onCheckboxClick}
                        heldByName={heldByOthers.get(translationMap.get(seg.id)?.id ?? -1) ?? null}
                        autoApprovedBeforeStep={autoApprovedBefore.has(translationMap.get(seg.id)?.id ?? -1)}
                        predictedBeforeStep={predictedBeforeStep.has(translationMap.get(seg.id)?.id ?? -1)}
                        plainText={plainTextFile}
                        onQARefresh={(translationId, issues) => {
                          void patchQAReport(queryClient, qaReportKey, translationId, issues)
                        }}
                      />
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
        translationBySegment={translationMap}
        textFor={id => translationById.get(id)?.active_text ?? ''}
        sourceTextFor={id => sourceTextBySegment.get(id) ?? ''}
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

      {/* Jump-to-segment dialog (Ctrl+G) */}
      <Dialog open={jumpOpen} onOpenChange={setJumpOpen}>
        <DialogContent className="sm:max-w-xs">
          <DialogHeader>
            <DialogTitle>Jump to segment</DialogTitle>
          </DialogHeader>
          <form onSubmit={(e) => {
            e.preventDefault()
            const num = parseInt(jumpInput)
            if (num >= 1 && num <= segmentCount) {
              goToSegment(num - 1)
              setJumpOpen(false)
            }
          }}>
            <Input
              type="number"
              min={1}
              max={segmentCount}
              value={jumpInput}
              onChange={(e) => setJumpInput(e.target.value)}
              placeholder={`1–${segmentCount}`}
              autoFocus
              className="mb-3"
            />
            <div className="flex justify-end">
              <Button type="submit" size="sm" disabled={!jumpInput || parseInt(jumpInput) < 1 || parseInt(jumpInput) > segmentCount}>
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

      {/* Always-visible live leverage breakdown. Fixed to the viewport
          bottom, VSCode-style;
          the outer editor div doesn't need extra padding because the bar
          is below the segment scroll container. */}
      <JobLeverageStatusBar jobId={jid} localRepetitionStats={repetitionStats} segmentCount={segmentCount} wordCount={job?.word_count ?? 0} />
    </TooltipProvider>
  )
}

// Header cells, in SEGMENT_GRID_COLS order. Match, Status and the unlabelled
// extras column use px-2, like their body cells.
const SEGMENT_HEADER_CELLS = [
  { label: '#', className: 'px-2 py-2' },
  { label: 'Source', className: 'px-3 py-2' },
  { label: 'Translation', className: 'px-3 py-2' },
  { label: 'Match', className: 'px-2 py-2 text-center' },
  { label: 'Status', className: 'px-2 py-2' },
  { label: null, className: '' },
]

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
    { keys: 'Shift+Enter', desc: 'Line break inside the segment' },
    { keys: 'Escape', desc: 'Exit editing' },
    { keys: `${modKey}+Z`, desc: 'Undo' },
    { keys: `${modKey}+Shift+S`, desc: 'Copy source to target' },
    { keys: `${modKey}+G`, desc: 'Jump to segment #' },
    { keys: `${modKey}+K`, desc: 'Concordance search — looks up the selected phrase' },
    { keys: `${modKey}+H`, desc: 'Find and replace' },
    { keys: `${modKey}+B`, desc: 'Bold' },
    { keys: `${modKey}+I`, desc: 'Italic' },
    { keys: `${modKey}+1..5`, desc: 'Insert TM match #1–5' },
    { keys: 'F8', desc: 'Insert next missing source tag' },
    { keys: 'Alt+F8', desc: 'Insert all missing source tags' },
    { keys: `${modKey}+Shift+Backspace`, desc: 'Clear translation' },
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
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="space-y-3 mt-2">
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
