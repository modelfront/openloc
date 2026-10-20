import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { toast } from 'sonner'
import { useScrollToError } from '../../hooks/useScrollToError'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { errorText } from '@/lib/error-text'

/** Name field shared by every "name this workflow" form (save-as, edit). */
export const workflowNameSchema = z.object({ name: z.string().trim().min(1, 'Workflow name is required') })
export type WorkflowNameValues = z.infer<typeof workflowNameSchema>

interface Props {
  open: boolean
  onClose: () => void
  title: string
  submitLabel: string
  /** One-line explanation under the title. */
  hint?: string
  onSubmit: (name: string) => Promise<unknown>
}

/** Name prompt for "Save as workflow…" on a project or template. The caller
 *  owns the request; a failure stays in the dialog as a toast. */
export function WorkflowNameDialog({ open, onClose, title, submitLabel, hint, onSubmit }: Props) {
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = useForm<WorkflowNameValues>({
    resolver: zodResolver(workflowNameSchema),
    defaultValues: { name: '' },
  })
  useScrollToError(errors)
  useEffect(() => { if (open) reset({ name: '' }) }, [open, reset])

  const submit = handleSubmit(async ({ name }) => {
    try {
      await onSubmit(name)
      onClose()
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
    }
  })

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()} keepOpen={isSubmitting}>
      <DialogContent data-testid="workflow-name-dialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        {hint && <p className="text-sm text-muted-foreground -mt-2">{hint}</p>}
        {/* stopPropagation: a portaled dialog's submit still bubbles up the React
            tree, so inside another form it would submit that one too. */}
        <form className="space-y-4" onSubmit={e => { e.preventDefault(); e.stopPropagation(); void submit() }}>
          <FormField label="Name" error={errors.name?.message} required>
            <Input {...register('name')} placeholder="e.g. Standard review" autoFocus data-testid="save-as-workflow-name" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={isSubmitting}>Cancel</Button>
            <Button type="submit" disabled={isSubmitting} data-testid="save-as-workflow-submit">{submitLabel}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
