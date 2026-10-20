// Name + ordered library steps: the form behind "Edit workflow" on the
// Workflows list and "New workflow" in the New Project wizard.
// The caller owns the request — a rejection keeps the
// dialog open so the user can correct and retry.
import { useEffect, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { toast } from 'sonner'
import { useScrollToError } from '../../hooks/useScrollToError'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { WorkflowStepsEditor, unpickedRows, type WorkflowStep } from '../WorkflowStepsEditor'
import { workflowNameSchema, type WorkflowNameValues } from './WorkflowNameDialog'
import { errorText } from '@/lib/error-text'

interface Props {
  open: boolean
  onClose: () => void
  title: string
  submitLabel: string
  /** One-line explanation under the title. */
  hint?: string
  /** Sentence above the step editor. */
  stepsHint?: string
  /** Renders a placeholder instead of the form (the workflow is still loading). */
  loading?: boolean
  initialName: string
  /** Seeds the step draft. Pass a stable reference — a new array reseeds the
   *  editor, dropping edits in progress. */
  initialSteps: WorkflowStep[]
  onSubmit: (input: { name: string; steps: WorkflowStep[] }) => Promise<unknown>
  /** `<prefix>-dialog`, `<prefix>-name` and `<prefix>-save` test ids. */
  testIdPrefix: string
}

export function WorkflowFormDialog({
  open, onClose, title, submitLabel, hint, stepsHint, loading = false,
  initialName, initialSteps, onSubmit, testIdPrefix,
}: Props) {
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = useForm<WorkflowNameValues>({
    resolver: zodResolver(workflowNameSchema),
    defaultValues: { name: initialName },
  })
  useScrollToError(errors)
  const [drafts, setDrafts] = useState<WorkflowStep[]>(initialSteps)

  // Seed the form whenever the dialog opens or its source changes (the edit
  // dialog's workflow arrives after the dialog is already open).
  useEffect(() => {
    if (open) reset({ name: initialName })
  }, [open, initialName, reset])
  useOnChange([open, initialName, initialSteps], () => { if (open) setDrafts(initialSteps) })

  const submit = handleSubmit(async ({ name }) => {
    if (drafts.length === 0) {
      toast.error('Add at least one step')
      return
    }
    const unpicked = unpickedRows(drafts)
    if (unpicked.length > 0) {
      toast.error(`Pick a step for row ${unpicked.join(', ')} or remove it`)
      return
    }
    try {
      await onSubmit({ name, steps: drafts })
      onClose()
    } catch (err) {
      // Staying open keeps the draft the user can correct.
      toast.error(`Failed: ${errorText(err, String(err))}`)
    }
  })

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()} keepOpen={isSubmitting}>
      <DialogContent className="sm:max-w-3xl" data-testid={`${testIdPrefix}-dialog`}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        {hint && <p className="text-sm text-muted-foreground -mt-2">{hint}</p>}
        {loading ? (
          <LoadingPlaceholder />
        ) : (
          // stopPropagation: a portaled dialog's submit still bubbles up the
          // React tree, so inside another form it would submit that one too.
          <form className="space-y-4" onSubmit={e => { e.preventDefault(); e.stopPropagation(); void submit() }}>
            <FormField label="Name" error={errors.name?.message} required>
              <Input {...register('name')} placeholder="e.g. Standard review" autoFocus data-testid={`${testIdPrefix}-name`} />
            </FormField>
            <div>
              {stepsHint && <p className="mb-2 text-xs text-gray-400 dark:text-gray-500">{stepsHint}</p>}
              <WorkflowStepsEditor
                steps={initialSteps}
                initialEditing
                onDraftChange={setDrafts}
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={onClose} disabled={isSubmitting}>Cancel</Button>
              <Button type="submit" disabled={isSubmitting} data-testid={`${testIdPrefix}-save`}>
                {isSubmitting ? 'Saving…' : submitLabel}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
