// The multi-file editor's Complete step: every open file the caller may
// complete, what each would leave unapproved, one hand-off note, and
// "Approve all and complete". The server completes each file on its own and
// answers per file; the dialog then lists the files that did not complete
// and why, and keeps them open for another try.

import { useState } from 'react'
import { TriangleAlert } from 'lucide-react'
import type { ScopeCompleteResult, ScopeFile } from '@/api'
import { OutcomeBlocked, OutcomeFailed } from '@/types/scopecomplete'
import { unapprovedSentence, type UnapprovedCounts } from '@/lib/step-completion'
import { Alert } from '@/components/ui/alert'
import { CompleteStepButtons, HandOffComment } from '@/components/CompleteStepDialog'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

export interface ScopeCompleteDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The files to complete: those whose step the caller may act on. */
  files: ScopeFile[]
  /** What completing would leave unapproved, by file (same order as files). */
  unapproved: UnapprovedCounts[]
  pending: boolean
  /** The results of the last try that left some file open; null when none. */
  failures: ScopeCompleteResult[] | null
  onConfirm: (comment: string, approveAll: boolean) => void
}

export function ScopeCompleteDialog({ open, onOpenChange, ...props }: ScopeCompleteDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} keepOpen={props.pending}>
      <DialogContent className="sm:max-w-lg">
        {/* Mounted with the popup, so every opening starts with an empty comment. */}
        <ScopeCompleteForm {...props} />
      </DialogContent>
    </Dialog>
  )
}

function ScopeCompleteForm({ files, unapproved, pending, failures, onConfirm }: Omit<ScopeCompleteDialogProps, 'open' | 'onOpenChange'>) {
  const [comment, setComment] = useState('')
  const total: UnapprovedCounts = {
    drafts: unapproved.reduce((n, u) => n + u.drafts, 0),
    untranslated: unapproved.reduce((n, u) => n + u.untranslated, 0),
  }
  const names = new Map(files.map(f => [f.job_id, f.file_name]))
  const note = comment.trim()
  return (
    <>
      <DialogHeader>
        <DialogTitle>Complete step for {files.length} {files.length === 1 ? 'file' : 'files'}</DialogTitle>
        <DialogDescription>
          Each file's current step is completed on its own. A file on its last step is completed, and its approved segments are saved to Translation memory.
        </DialogDescription>
      </DialogHeader>

      <ul className="max-h-48 overflow-y-auto text-sm divide-y divide-gray-100 dark:divide-gray-800" data-testid="scope-complete-files">
        {files.map((f, i) => {
          const left = unapproved[i].drafts + unapproved[i].untranslated
          return (
            <li key={f.job_id} className="flex items-center justify-between gap-3 py-1">
              <span className="truncate">{f.file_name}</span>
              <span className="shrink-0 text-xs text-gray-500 tabular-nums">
                {left === 0 ? 'all approved' : `${left} not approved`}
              </span>
            </li>
          )
        })}
      </ul>

      {total.drafts + total.untranslated > 0 && (
        <Alert tone="warning" icon={<TriangleAlert />} data-testid="scope-complete-unapproved">
          {unapprovedSentence(total)}
        </Alert>
      )}

      {failures && (
        <Alert tone="error" icon={<TriangleAlert />} data-testid="scope-complete-failures">
          These files were not completed:
          <ul className="mt-1 list-disc pl-5">
            {failures.map(r => (
              <li key={r.job_id}>
                <strong>{names.get(r.job_id) ?? `Job ${r.job_id}`}</strong>: {failureReason(r)}
              </li>
            ))}
          </ul>
        </Alert>
      )}

      <HandOffComment value={comment} onChange={setComment} />

      <CompleteStepButtons
        pending={pending}
        offerApproveAll={total.drafts > 0}
        tone="success"
        completeLabel="Complete step"
        onComplete={() => onConfirm(note, false)}
        onApproveAll={() => onConfirm(note, true)}
      />
    </>
  )
}

/** Why a file did not complete, in the user's words. */
function failureReason(r: ScopeCompleteResult): string {
  if (r.outcome === OutcomeBlocked) {
    const n = r.blocked?.length ?? 0
    return `${n} ${n === 1 ? 'segment' : 'segments'} could not be approved (a QA error, a change made meanwhile, or someone else editing it)`
  }
  if (r.outcome === OutcomeFailed) return r.error?.message ?? 'it could not be completed'
  return 'it could not be completed'
}
