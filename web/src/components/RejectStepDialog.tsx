// Reject: send the job back to an earlier step of its workflow. The whole
// workflow is shown so the user picks the step on it; steps that cannot be
// picked say why on hover. Reopening an AI step runs it again; a manager may
// run a hybrid step's AI again too.
import { useState } from 'react'
import type { JobStepAssignment, RejectStepInput } from '../api'
import { MAX_NOTE_LENGTH } from '../api'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { WorkflowStepChips, type ChipStep } from './workflow/WorkflowStepChips'
import { defaultRollbackTarget, rerunChoice, rollbackBlockReason } from '../lib/rollback'
import { TASK_STATE_DISPLAY, type TaskState } from '../lib/task-matrix'

function stepState(status: string): TaskState {
  if (status === 'completed') return 'completed'
  if (status === 'in_progress') return 'in_progress'
  return 'not_started'
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The job's step assignments in workflow order. */
  steps: JobStepAssignment[]
  currentIndex: number
  isManager: boolean
  pending: boolean
  onConfirm: (input: RejectStepInput) => void
}

export function RejectStepDialog(props: Props) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange} keepOpen={props.pending}>
      <DialogContent className="sm:max-w-lg">
        {/* Mounted per opening, so every opening starts from the default pick. */}
        <RejectStepBody {...props} />
      </DialogContent>
    </Dialog>
  )
}

function RejectStepBody({ onOpenChange, steps, currentIndex, isManager, pending, onConfirm }: Props) {
  const [target, setTarget] = useState<number | null>(() => defaultRollbackTarget(steps, currentIndex))
  const [rerun, setRerun] = useState(false)
  const [reason, setReason] = useState('')

  const chipSteps: ChipStep[] = steps.map(s => ({
    name: s.step_name, step_type: s.step_type, ai_router_id: s.ai_router_id, assignee_name: s.assignee_name,
  }))
  const picked = target != null ? steps[target] : undefined
  const choice = rerunChoice(picked, isManager)

  return (
    <>
      <DialogHeader>
        <DialogTitle>Reject step</DialogTitle>
        <DialogDescription>
          Pick the step to send the job back to. The steps after it start again, in order.
        </DialogDescription>
      </DialogHeader>

      <WorkflowStepChips
        steps={chipSteps}
        data-testid="reject-target"
        selection={{
          value: target,
          onChange: i => { setTarget(i); setRerun(false) },
          blockReason: i => rollbackBlockReason(steps, i, currentIndex, isManager),
          'aria-label': 'Step to send the job back to',
        }}
        trailing={i => {
          const d = TASK_STATE_DISPLAY[stepState(steps[i].status)]
          return <Chip color={d.color} className="ml-auto shrink-0">{d.label}</Chip>
        }}
      />

      {choice === 'optional' && (
        <CheckboxField
          checked={rerun}
          onChange={setRerun}
          label="Run the AI again"
          data-testid="reject-rerun-ai"
        />
      )}

      <div className="space-y-2">
        <label htmlFor="reject-reason" className="text-sm font-medium text-gray-700 dark:text-gray-300">
          Rejection reason <span className="text-gray-400 font-normal">(optional)</span>
        </label>
        <Textarea
          id="reject-reason"
          value={reason}
          onChange={e => setReason(e.target.value)}
          placeholder="e.g., Several terminology inconsistencies in segments 12-15…"
          rows={3}
          maxLength={MAX_NOTE_LENGTH}
          className="text-sm"
        />
      </div>

      <DialogFooter>
        <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>
          Cancel
        </Button>
        <Button
          size="sm"
          variant="destructive"
          disabled={pending || picked == null}
          onClick={() => picked && onConfirm({ reason, toStepId: picked.id, rerunAI: choice === 'optional' && rerun })}
          data-testid="reject-confirm"
        >
          {pending ? 'Rejecting...' : `Send back to ${picked?.step_name ?? '…'}`}
        </Button>
      </DialogFooter>
    </>
  )
}
