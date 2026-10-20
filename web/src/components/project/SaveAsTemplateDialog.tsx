import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'

const schema = z.object({ name: z.string().trim().min(1, 'Template name is required') })
type Values = z.infer<typeof schema>

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-filled template name (the form's project name, usually). */
  defaultName: string
  /** Resolve to close the dialog; reject to keep it open with the typed name. */
  onSave: (name: string) => Promise<void>
}

/**
 * Names the template the New project form is about to be saved as. Rendered
 * outside the page's <form>, so its submit never reaches the project form.
 */
export function SaveAsTemplateDialog({ open, onOpenChange, defaultName, onSave }: Props) {
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: defaultName },
  })
  // The form is mounted persistently; re-seed the name on each open.
  useEffect(() => { if (open) reset({ name: defaultName }) }, [open, defaultName, reset])

  const submit = handleSubmit(async ({ name }) => {
    await onSave(name.trim())
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange} keepOpen={isSubmitting}>
      <DialogContent data-testid="save-as-template-dialog">
        <form onSubmit={submit} className="contents">
          <DialogHeader>
            <DialogTitle>Save as template</DialogTitle>
            <DialogDescription>
              The template keeps this form's project name, languages, customer, domain, workflow and settings. Files are not saved.
            </DialogDescription>
          </DialogHeader>
          <FormField label="Template name" error={errors.name?.message} required>
            <Input {...register('name')} placeholder="Template name" autoFocus data-testid="save-as-template-name" />
          </FormField>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={isSubmitting}>Cancel</Button>
            <Button type="submit" disabled={isSubmitting} data-testid="save-as-template-confirm">
              {isSubmitting ? 'Saving...' : 'Save template'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
