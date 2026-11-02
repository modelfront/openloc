// A project's steps on its Workflow tab.
//
// They are shown read-only, each with its deadlines, until Edit steps opens
// them as an editor. Everything changed there (a step, the order, an
// assignee, a deadline) is one draft: Save changes asks the server what the
// save would do to the live jobs (a rolled-back preview), says so in a
// confirmation, and only then saves; Discard drops it. The steps jobs have
// reached are locked (the server says which): they keep their place and their
// step, while their assignee and deadline stay editable.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import type { WorkflowPreview } from '../../api'
import { sameWorkflowSteps, withServerLocks } from '../../lib/workflow-steps'
import { WorkflowStepsEditor, unpickedRows, type WorkflowStep } from '../WorkflowStepsEditor'
import ConfirmDialog from '../ConfirmDialog'
import { Button, NOTHING_CHANGED_REASON } from '@/components/ui/button'
import { WorkflowStepChips } from './WorkflowStepChips'
import { WorkflowStepsWithDeadlines } from './WorkflowDeadlines'
import { errorText } from '@/lib/error-text'
import { count } from '@/lib/plural'

interface Props {
  /** The saved steps, with their locks. Pass a stable reference. */
  steps: WorkflowStep[]
  sourceLang: string
  targetLangs: string[]
  /** Heads the list with the Pre-translation row (see WorkflowStepChips). */
  pretranslation?: ReactNode
  onSave: (steps: WorkflowStep[]) => Promise<void>
  onPreview: (steps: WorkflowStep[]) => Promise<WorkflowPreview>
  /** Re-reads the steps, so a refused save shows the locks as they are now. */
  onRefresh: () => void
  /** Told when editing starts and ends: the picker stays shut meanwhile, so
   *  a library workflow is never applied under a draft that would undo it. */
  onDraftOpen?: (open: boolean) => void
}

export function ProjectWorkflowSteps({ steps, sourceLang, targetLangs, pretranslation, onSave, onPreview, onRefresh, onDraftOpen }: Props) {
  // The steps being edited; null while they are only shown.
  const [draft, setDraft] = useState<WorkflowStep[] | null>(null)
  const editing = draft != null
  useEffect(() => { onDraftOpen?.(editing) }, [editing, onDraftOpen])
  const [preview, setPreview] = useState<WorkflowPreview | null>(null)
  const [saving, setSaving] = useState(false)
  const changed = draft != null && !sameWorkflowSteps(draft, steps)

  // A reload or a closed browser tab would drop unsaved changes, so the
  // browser asks first. Leaving for another page in the app does not ask.
  useEffect(() => {
    if (!changed) return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [changed])

  // New saved steps while editing: a draft with no edits yet becomes the new
  // steps (it would otherwise put back what someone just saved); edits stay,
  // with the locks as they are now (a job reached a step).
  const prevStepsRef = useRef(steps)
  useEffect(() => {
    const prev = prevStepsRef.current
    if (steps === prev) return
    prevStepsRef.current = steps
    setDraft(d => (d == null ? d : sameWorkflowSteps(d, prev) ? steps : withServerLocks(d, steps)))
  }, [steps])

  const askToSave = async () => {
    if (!draft) return
    if (draft.length === 0) {
      toast.error('Add at least one step')
      return
    }
    const unpicked = unpickedRows(draft)
    if (unpicked.length > 0) {
      toast.error(`Pick a step for row ${unpicked.join(', ')} or remove it`)
      return
    }
    setSaving(true)
    try {
      setPreview(await onPreview(draft))
    } catch (err) {
      // A step a job reached since the page loaded: show the locks as they are.
      toast.error(errorText(err, 'Workflow not saved'))
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await onSave(draft)
      setDraft(null)
      toast.success('Workflow saved')
    } catch (err) {
      toast.error(errorText(err, 'Workflow not saved'))
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  if (!editing) {
    return (
      <div className="space-y-3" data-testid="project-workflow-steps">
        <WorkflowStepsWithDeadlines steps={steps} targetLanguages={targetLangs} pretranslation={pretranslation} />
        <div className="flex justify-end">
          <Button variant="outline" onClick={() => setDraft(steps)} data-testid="workflow-steps-edit">
            Edit steps
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-3" data-testid="project-workflow-steps">
      {pretranslation && <WorkflowStepChips steps={[]} pretranslation={pretranslation} />}
      <WorkflowStepsEditor
        steps={draft}
        onChange={setDraft}
        sourceLang={sourceLang}
        targetLangs={targetLangs}
        deadlines
      />
      <div className="flex justify-end gap-2" data-testid="workflow-steps-draft-actions">
        <Button variant="ghost" onClick={() => setDraft(null)} disabled={saving} data-testid="workflow-steps-discard">
          Discard
        </Button>
        <Button
          onClick={() => void askToSave()}
          loading={saving}
          disabled={!changed}
          disabledReason={changed ? undefined : NOTHING_CHANGED_REASON}
          data-testid="workflow-steps-save"
        >
          Save changes
        </Button>
      </div>
      <ConfirmDialog
        open={preview != null}
        onOpenChange={open => !open && setPreview(null)}
        title="Save the workflow?"
        description={previewSummary(preview)}
        details={preview && (
          <ul className="list-disc space-y-1 pl-5" data-testid="workflow-steps-preview">
            {preview.effects.map(e => <li key={e}>{e}</li>)}
          </ul>
        )}
        actionLabel="Save"
        variant="default"
        onConfirm={() => void save()}
        testId="workflow-steps-confirm"
      />
    </div>
  )
}

/** "This save adds 1 step and changes 2 steps." — what the save does to the
 *  step list, in one sentence. */
export function previewSummary(p: WorkflowPreview | null): string {
  if (!p) return ''
  const parts = [
    p.added > 0 && `adds ${count(p.added, 'step')}`,
    p.changed > 0 && `changes ${count(p.changed, 'step')}`,
    p.removed > 0 && `removes ${count(p.removed, 'step')}`,
  ].filter((x): x is string => !!x)
  if (parts.length === 0) return 'This save changes assignees and deadlines only.'
  const text = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(', ')} and ${parts.at(-1)}`
  return `This save ${text}.`
}
