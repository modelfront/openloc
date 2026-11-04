import { Upload } from 'lucide-react'
import { modKey } from '@/components/SegmentRow'
import { DownloadMenu } from '@/components/project/DownloadMenu'
import { ImportXLIFFAction } from '@/components/project/ImportXLIFFAction'
import { JobPipelineActions } from '@/components/JobPipelineActions'
import { TaskActions, TaskDeadlineChip, TaskNotes, TaskStepChip } from '@/components/TaskControls'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useJobPipelineActions } from '@/hooks/useJobPipelineActions'
import { canRerunTask, canSkipAIStep } from '@/lib/current-task'
import { canRollback } from '@/lib/rollback'
import { RunQAButton, SpellCheckButton } from './EditorToolButtons'
import { DEFAULT_RUN_SCOPE } from '@/lib/spellcheck-scope'
import type { EditorData } from './useEditorData'
import type { StepCompletion } from './useStepCompletion'
import type { EditorDialogs } from './useEditorDialogs'

interface EditorHeaderProps {
  projectId: number
  jobId: number
  data: EditorData
  completion: StepCompletion
  dialogs: Pick<EditorDialogs, 'openConcordance' | 'openSpellCheck'>
  pipeline: ReturnType<typeof useJobPipelineActions>
}

/**
 * The editor's top: breadcrumbs with the task's chips and controls, the
 * toolbar with the job's tools, and the task's notes.
 */
export function EditorHeader({ projectId: pid, jobId: jid, data, completion, dialogs, pipeline }: EditorHeaderProps) {
  const {
    job, jobSteps, task, isManager, isFinalStep, currentStepIndex, inFirstStep, taskInClearWindow, canEdit,
    runningBadge, jobStoppedNote, qaReport, runQAMut,
  } = data
  const { advanceMut, rejectMut, setApproveBlocked, setCompleteDialogOpen, setRejectDialogOpen } = completion
  const { rerun, skipAIStep } = pipeline

  return (
    <>
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
                onClick={() => dialogs.openConcordance()}
                className="h-7 text-xs"
              />
            }>
              Concordance
            </TooltipTrigger>
            <TooltipContent>{modKey}+K — search TM/terminology for the selected phrase</TooltipContent>
          </Tooltip>
          <SpellCheckButton disabled={!job || job.counts.total === 0} onClick={() => dialogs.openSpellCheck(DEFAULT_RUN_SCOPE)} />
          <RunQAButton
            report={qaReport}
            pending={runQAMut.isPending}
            disabled={!job || job.counts.total === 0}
            onRun={() => runQAMut.mutate()}
          />
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
    </>
  )
}
