import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { errorText } from '@/lib/error-text'

interface Props {
  open: boolean
  onClose: () => void
  title: string
  submitLabel: string
  /** Optional one-line explanation under the title. */
  hint?: string
  /** Optional checkbox under the fields (e.g. "follow the new profile"). */
  linkOption?: { label: string; defaultChecked?: boolean }
  onSubmit: (name: string, description: string, link: boolean) => Promise<unknown>
}

/**
 * Name + description prompt for a TM profile. Used both for "New TM profile" on
 * the TM page and for "Save current TMs as profile" in project/template
 * settings, so the two flows can't drift. The caller owns the request.
 */
export function TMProfileNameDialog({ open, onClose, title, submitLabel, hint, linkOption, onSubmit }: Props) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [link, setLink] = useState(linkOption?.defaultChecked ?? false)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) { setName(''); setDescription(''); setLink(linkOption?.defaultChecked ?? false) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const submit = async () => {
    const trimmed = name.trim()
    if (!trimmed) return
    setBusy(true)
    try {
      await onSubmit(trimmed, description.trim(), link)
      onClose()
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()} keepOpen={busy}>
      <DialogContent data-testid="tm-profile-dialog">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        {hint && <p className="text-sm text-muted-foreground -mt-2">{hint}</p>}
        <form
          className="space-y-4"
          // stopPropagation: React submit events from a portaled dialog still bubble
          // up the component tree, so inside the New Project form this would also
          // submit the project (ConfirmDialog guards the same way).
          onSubmit={e => { e.preventDefault(); e.stopPropagation(); void submit() }}
        >
          <FormField label="Name" required>
            <Input
              id="tm-profile-name"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="e.g. Acme legal"
              autoFocus
              data-testid="tm-profile-name"
            />
          </FormField>
          <FormField label="Description">
            <Input
              id="tm-profile-description"
              value={description}
              onChange={e => setDescription(e.target.value)}
              placeholder="Optional"
              data-testid="tm-profile-description"
            />
          </FormField>
          {linkOption && (
            <CheckboxField checked={link} onChange={setLink} label={linkOption.label} data-testid="tm-profile-link" />
          )}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
            <Button type="submit" disabled={busy || !name.trim()} data-testid="tm-profile-submit">{submitLabel}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
