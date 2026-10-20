// Project page → Tasks tab. On the page: a collapsible section per workflow
// step holding each job's current task (TaskStepSections). Expand opens the
// full matrix — every task — in a full-screen modal. Both share the toolbar
// state and the selection bar.
//
// Access: managers, like the rest of the project page (the route and GET
// /api/projects/{id}/tasks are both manager-gated).

import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'

import { listProjectTasks, type Job } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import {
  buildTaskMatrix,
  currentTaskMatrix,
  stepTones,
  parseTaskMatrixFilters,
  selectedTasks,
} from '@/lib/task-matrix'
import { BulkJobActionsBar, type StepTarget } from '@/components/project/BulkJobActionsBar'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { TaskMatrix } from './TaskMatrix'
import { TaskStepSections } from './TaskStepSections'
import { TaskMatrixToolbar, type TaskMatrixFile } from './TaskMatrixToolbar'

interface Props {
  projectId: number
  jobs: Job[]
  /** When the jobs list last loaded; the tasks refetch whenever it moves. */
  jobsUpdatedAt: number
  isManager: boolean
}

export function ProjectTasksTab({ projectId, jobs, jobsUpdatedAt, isManager }: Props) {
  const [params] = useSearchParams()
  const filters = useMemo(() => parseTaskMatrixFilters(params), [params])
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [fullScreen, setFullScreen] = useState(false)

  const tasksQuery = useQuery({
    queryKey: queryKeys.jobs.tasks(projectId),
    queryFn: () => listProjectTasks(projectId),
  })
  const { data: tasks, refetch, isFetching, dataUpdatedAt, errorUpdatedAt } = tasksQuery

  // Step states move with the pipeline — an AI step completes itself, a
  // failure lands on the job — and it is the jobs list that polls while a
  // pipeline runs. So the tasks are kept no older than the jobs.
  useEffect(() => {
    if (jobsUpdatedAt > Math.max(dataUpdatedAt, errorUpdatedAt) && !isFetching) void refetch()
  }, [jobsUpdatedAt, dataUpdatedAt, errorUpdatedAt, isFetching, refetch])

  const matrix = useMemo(
    () => buildTaskMatrix(jobs, tasks ?? [], filters),
    [jobs, tasks, filters],
  )
  // The page shows one task per job, so its legend, its rows and what the
  // bar acts on all come from this; the modal works on the whole matrix.
  const pageMatrix = useMemo(() => currentTaskMatrix(matrix), [matrix])
  // From every task, not the current ones: a step every job is past is done.
  const tones = useMemo(() => stepTones(matrix), [matrix])
  const shown = fullScreen ? matrix : pageMatrix
  const targets = useMemo(() => [...new Set(jobs.map(j => j.target_language))].sort(), [jobs])
  const files = useMemo<TaskMatrixFile[]>(
    () => [...new Map(jobs.map(j => [j.file_id, { id: j.file_id, name: j.file_name, uploadedAt: j.file_created_at }])).values()]
      .sort((a, b) => a.name.localeCompare(b.name) || b.uploadedAt.localeCompare(a.uploadedAt)),
    [jobs],
  )

  const picked = useMemo(() => selectedTasks(shown, selected), [shown, selected])
  const stepTargets = useMemo<StepTarget[]>(
    () => picked.map(t => ({ job: t.job, assignmentId: t.task.id, status: t.task.status, stepType: t.task.step_type, state: t.state })),
    [picked],
  )
  const pickedJobs = useMemo(() => [...new Map(picked.map(t => [t.job.id, t.job])).values()], [picked])
  const clearSelection = () => setSelected(new Set())

  if (tasksQuery.isLoading) return <LoadingPlaceholder />
  if (tasksQuery.error && !tasks) return <LoadFailed what="tasks" error={tasksQuery.error} onRetry={() => void refetch()} />

  const view = (inModal: boolean) => (
    <div className={inModal ? 'flex min-h-0 flex-1 flex-col gap-3' : 'flex flex-col gap-3'}>
      <TaskMatrixToolbar
        matrix={inModal ? matrix : pageMatrix}
        tasks={tasks ?? []}
        targets={targets}
        files={files}
        onExpand={inModal ? undefined : () => setFullScreen(true)}
        fullScreen={inModal}
      />
      {/* The bar of the view on screen only: the page stays mounted under
          the modal, and a second bar there would act on the same picks. */}
      {picked.length > 0 && inModal === fullScreen && (
        <BulkJobActionsBar
          projectId={projectId}
          selectedJobs={pickedJobs}
          stepTargets={stepTargets}
          isManager={isManager}
          onClear={clearSelection}
        />
      )}
      {(inModal ? matrix : pageMatrix).rows.length === 0 ? (
        <EmptyState
          message={jobs.length === 0
            ? 'No files yet. Upload files on the Files tab to create tasks.'
            : 'No tasks match the current filters.'}
        />
      ) : inModal ? (
        <TaskMatrix
          projectId={projectId}
          matrix={matrix}
          selected={selected}
          onSelectedChange={setSelected}
          className="min-h-0 flex-1"
        />
      ) : (
        <TaskStepSections
          projectId={projectId}
          matrix={pageMatrix}
          selected={selected}
          onSelectedChange={setSelected}
          tones={tones}
        />
      )}
    </div>
  )

  return (
    <>
      {view(false)}
      <Dialog
        open={fullScreen}
        onOpenChange={(open, details) => {
          // Escape clears a selection first, wherever focus is; it closes the
          // modal only once nothing is selected.
          if (!open && details.reason === 'escape-key' && picked.length > 0) {
            details.cancel()
            clearSelection()
            return
          }
          setFullScreen(open)
        }}
      >
        <DialogContent
          className="flex h-[calc(100vh-2rem)] flex-col sm:max-w-[calc(100vw-2rem)]"
          data-testid="task-matrix-modal"
        >
          <DialogHeader>
            <DialogTitle>Tasks</DialogTitle>
          </DialogHeader>
          {fullScreen && view(true)}
        </DialogContent>
      </Dialog>
    </>
  )
}
