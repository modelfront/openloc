import { useMemo, useState } from 'react'
import type { RefObject } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getCommentCounts, getJobStepStatuses, getQAReport, listJobSteps, runQA, type QAReport } from '@/api'
import { getMe } from '@/api/auth'
import { queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { usePipelineEditorRefresh } from '@/hooks/usePipelineEditorRefresh'
import { useJobDetail } from '@/hooks/useJobDetail'
import { usePageTitle } from '@/hooks/usePageTitle'
import { useEditorIndex } from '@/hooks/useEditorRows'
import { isAdminRole, isManagerRole } from '@/lib/roles'
import { inClearWindow, isPipelineRunning, isAIStepRunning, pipelineBadge, stoppedNote } from '@/lib/project-logic'
import { canEditTask, currentTask } from '@/lib/current-task'
import { unapprovedCounts } from '@/lib/step-completion'
import { qaRunToast, groupFindingsByTranslation } from '@/lib/qa-findings'
import { autoApprovedBeforeStep } from '@/components/step-status-labels'
import { qeChipLabel } from '@/lib/qe-chip'
import { extrasColumnWidth } from '@/lib/segment-columns'
import { hasOverlayScrollbars } from '@/lib/scrollbar'
import { countByDisplayStatus } from '@/lib/segment-filters'
import { translatedRows } from '@/lib/editor-index'

/**
 * Everything the editor loads for one job: the user, the job, its segment
 * index, comment counts, the QA report and the workflow steps, plus what the
 * page derives from them (the current task, job-wide counts, column widths).
 */
export function useEditorData(jid: number, flushDraftRef: RefObject<() => Promise<void>>) {
  const queryClient = useQueryClient()

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
  // run over this. The rows on screen load their text by page.
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

  const commentCountByTranslation = useMemo(() => {
    const map = new Map<number, number>()
    commentCounts?.forEach(c => map.set(c.translation_id, c.comment_count))
    return map
  }, [commentCounts])

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
  const previousStepName = stepStatuses?.previous[0]?.step_name
  const canEdit = canEditTask(task.mode)
  // "Strings contain HTML" off for this file: every insert goes in as plain text.
  const plainTextFile = job?.file_inline_html === false

  // The page stays on one loading screen until everything its first paint
  // depends on is in: the job, its steps, the segment index and the user.
  // Each piece rendering as it arrives reshuffles the page several times on a
  // slow connection. A failed query stops pending, so an error never hangs here.
  const editorLoading = mePending || jobPending
    || (!!job && (jobStepsPending || editorIndex.isPending))
  const gridShown = !editorLoading && !aiRunning

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

  return {
    currentUserId,
    isAdmin,
    isManager,
    job,
    pipelineRunning,
    aiRunning,
    runningBadge,
    jobStoppedNote,
    editorIndex,
    indexRows,
    indexLookup,
    commentCountsFailed,
    commentCountByTranslation,
    qaReport,
    qaReportKey,
    qaReportFailed,
    runQAMut,
    jobSteps,
    task,
    autoApprovedBefore,
    predictedBeforeStep,
    hasPreviousStep,
    previousStepName,
    canEdit,
    plainTextFile,
    editorLoading,
    gridShown,
    isFinalStep,
    currentStepIndex,
    inFirstStep,
    taskInClearWindow,
    rows,
    unapproved,
    statusCounts,
    qaResultsByTranslation,
    extrasWidth,
    overlayScrollbar,
  }
}

export type EditorData = ReturnType<typeof useEditorData>
