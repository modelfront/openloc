// The CAT editor's confirmation before it completes the current step. It says
// what is left unapproved and offers to approve every draft first. Past the
// final step it lists what completing the workflow does; before it, it takes
// the comment for whoever works the next step (the hand-off note, shown to
// them in the editor). The final step has no next assignee, so no comment.

import { useState } from 'react'
import { TriangleAlert } from 'lucide-react'
import { MAX_NOTE_LENGTH } from '@/api/analytics'
import { finalStepAutomations } from '../lib/completion-copy'
import { unapprovedSentence, type ApproveBlocked, type UnapprovedCounts } from '../lib/step-completion'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Alert } from '@/components/ui/alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

export interface CompleteStepDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  stepName: string
  isFinalStep: boolean
  /** The job's approved segments, for the final step's list; null while unknown. */
  approvedCount: number | null
  /** What completing would leave unapproved. */
  unapproved: UnapprovedCounts
  /** The user may approve the drafts (the task takes their edits). */
  canApproveAll: boolean
  /** Segments "Approve all and complete" could not approve; null when none. */
  approveBlocked: ApproveBlocked | null
  pending: boolean
  /** Called with the trimmed comment for the next assignee ('' for none). */
  onConfirm: (comment: string) => void
  /** Approve every draft, then complete with the comment. */
  onApproveAll: (comment: string) => void
}

export default function CompleteStepDialog({ open, onOpenChange, ...props }: CompleteStepDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} keepOpen={props.pending}>
      {/* Only the final step has a description; the others opt out explicitly. */}
      <DialogContent className="sm:max-w-md" {...(props.isFinalStep ? {} : { 'aria-describedby': undefined })}>
        {/* Mounted with the popup, so every opening starts with an empty comment. */}
        <CompleteStepForm {...props} />
      </DialogContent>
    </Dialog>
  )
}

function CompleteStepForm({
  stepName, isFinalStep, approvedCount, unapproved, canApproveAll, approveBlocked, pending, onConfirm, onApproveAll,
}: Omit<CompleteStepDialogProps, 'open' | 'onOpenChange'>) {
  const [comment, setComment] = useState('')
  const note = isFinalStep ? '' : comment.trim()
  const offerApproveAll = canApproveAll && unapproved.drafts > 0
  const tone = isFinalStep ? 'final' : 'success'
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {isFinalStep ? 'Complete final step' : `Complete "${stepName}"`}
        </DialogTitle>
        {isFinalStep && <DialogDescription>Completing this final step will:</DialogDescription>}
      </DialogHeader>

      {/* The final step triggers automations — list them explicitly. */}
      {isFinalStep && approvedCount !== null && (
        <ul className="list-disc pl-5 space-y-1 text-sm text-gray-600 dark:text-gray-400">
          {finalStepAutomations({ approvedCount }).map(line => (
            <li key={line.key}>{line.text}</li>
          ))}
        </ul>
      )}

      {unapproved.drafts + unapproved.untranslated > 0 && (
        <Alert tone="warning" icon={<TriangleAlert />} data-testid="complete-unapproved">
          {unapprovedSentence(unapproved)}
        </Alert>
      )}

      {approveBlocked && (
        <Alert tone="error" icon={<TriangleAlert />} data-testid="complete-approve-blocked">
          The step was not completed.
          {approveBlocked.toFix.length > 0 && (
            <>
              {' '}<SegmentList numbers={approveBlocked.toFix} />{' '}
              could not be approved because of a QA error or a change made meanwhile.
              Fix {approveBlocked.toFix.length === 1 ? 'it' : 'them'}, then try again.
            </>
          )}
          {approveBlocked.held.length > 0 && (
            <>
              {' '}<SegmentList numbers={approveBlocked.held} />{' '}
              {approveBlocked.held.length === 1 ? 'is' : 'are'} being edited by someone else.
              Try again when {approveBlocked.held.length === 1 ? 'it is' : 'they are'} free.
            </>
          )}
        </Alert>
      )}

      {!isFinalStep && (
        <div className="space-y-2">
          <label htmlFor="complete-comment" className="text-sm font-medium text-gray-700 dark:text-gray-300">
            Comment for next assignee <span className="text-gray-400 font-normal">(optional)</span>
          </label>
          <Textarea
            id="complete-comment"
            value={comment}
            onChange={e => setComment(e.target.value)}
            placeholder="e.g., Pay attention to terminology in section 3…"
            rows={3}
            maxLength={MAX_NOTE_LENGTH}
            className="text-sm"
          />
        </div>
      )}

      <DialogFooter>
        <Button
          size="sm"
          variant={offerApproveAll ? 'outline' : tone}
          onClick={() => onConfirm(note)}
          disabled={pending}
        >
          {pending && !offerApproveAll ? 'Completing...' : isFinalStep ? 'Complete final step' : 'Complete step'}
        </Button>
        {offerApproveAll && (
          <Button
            size="sm"
            onClick={() => onApproveAll(note)}
            disabled={pending}
            variant={tone}
            data-testid="complete-approve-all"
          >
            {pending ? 'Completing...' : 'Approve all and complete'}
          </Button>
        )}
      </DialogFooter>
    </>
  )
}

/** "Segment <b>4</b>" or "Segments <b>3, 8</b>". */
function SegmentList({ numbers }: { numbers: number[] }) {
  return <>{numbers.length === 1 ? 'Segment' : 'Segments'} <strong>{numbers.join(', ')}</strong></>
}
