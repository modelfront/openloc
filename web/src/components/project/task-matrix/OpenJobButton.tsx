// Open a job in the CAT editor, on its current task, in a new tab. While its
// AI step runs (the AI-step editor lock) the button stays in place, disabled,
// and says why. A click never also selects the row it sits in.

import { Link } from 'react-router-dom'

import type { Job } from '@/api'
import { editorPath, EDITOR_LINK_TARGET } from '@/lib/editor-link'
import { isAIStepRunning } from '@/lib/project-logic'
import { Button } from '@/components/ui/button'

interface Props {
  projectId: number
  job: Job
  /** Runs on open: the editor's tab changes what this page shows. */
  onOpen: () => void
  className?: string
}

export function OpenJobButton({ projectId, job, onOpen, className }: Props) {
  const openProps = {
    variant: 'outline',
    size: 'xs',
    className,
    'aria-label': `Open ${job.file_name} (${job.target_language}) in the CAT editor`,
    'data-testid': `task-row-open-${job.id}`,
  } as const
  return isAIStepRunning(job) ? (
    // Capture: a disabled button runs no onClick, and the click must not
    // select the row it sits in either.
    <Button
      {...openProps}
      disabled
      disabledReason="The AI step is running. The job opens when it finishes."
      onClickCapture={e => e.stopPropagation()}
    >
      Open
    </Button>
  ) : (
    <Button
      {...openProps}
      nativeButton={false}
      render={<Link to={editorPath(projectId, job.id)} {...EDITOR_LINK_TARGET} />}
      onClick={e => { e.stopPropagation(); onOpen() }}
      aria-label={`${openProps['aria-label']} (new tab)`}
    >
      Open
    </Button>
  )
}
