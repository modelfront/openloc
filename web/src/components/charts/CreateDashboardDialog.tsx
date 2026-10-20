// "New dashboard".
//
// A dashboard is a folder, so creating one asks for a name and nothing else —
// the charts arrive afterwards, from the builder or from the chat. Which side
// it lands on follows the tab the user pressed the button on; shared is
// owner/admin, which the caller already knows, so the toggle only appears when
// both sides are open to them.
import { useEffect, useRef } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useNavigate } from 'react-router-dom'
import { createDashboard } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { useScrollToError } from '@/hooks/useScrollToError'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { ChoiceToggle } from '@/components/ui/choice-toggle'

const schema = z.object({
  name: z.string().trim().min(1, 'Name is required'),
  side: z.enum(['shared', 'mine']),
})
type Values = z.infer<typeof schema>

export function CreateDashboardDialog({ open, onClose, personal, canShare }: {
  open: boolean
  onClose: () => void
  /** Which tab the button was pressed on — the side the dashboard starts on. */
  personal: boolean
  /** Whether this user may publish to the org at all. */
  canShare: boolean
}) {
  const navigate = useNavigate()
  const startSide = personal ? 'mine' : 'shared'
  const { register, control, handleSubmit, reset, setValue, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', side: startSide },
  })
  useScrollToError(errors)
  // The dialog is mounted with the page, so the tab may have changed since the
  // form was first set up: every opening starts clean, on the current tab. The
  // tab switch is a router transition and can render while the dialog is open;
  // it then moves only an untouched side, never the typed name
  const wasOpen = useRef(false)
  const sideChosen = useRef(false)
  useEffect(() => {
    if (!open) {
      wasOpen.current = false
      return
    }
    if (!wasOpen.current) {
      wasOpen.current = true
      sideChosen.current = false
      reset({ name: '', side: startSide })
    } else if (!sideChosen.current) {
      setValue('side', startSide)
    }
  }, [open, startSide, reset, setValue])

  const createMut = useMutationWithToast({
    mutationFn: (v: Values) => createDashboard(v.name, v.side === 'mine'),
    successMessage: d => `Created "${d.name}"`,
    errorMessage: 'Failed to create the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: d => {
      onClose()
      // Straight into the empty dashboard: the next thing anyone wants is a
      // chart in it, and that button lives there.
      navigate(`/dashboards/${d.id}`)
    },
  })

  // No <form>: the dialog renders in a portal, and a nested form's submit
  // would still bubble to any form around the button. Enter submits
  // from the name field only.
  const submit = handleSubmit(v => { if (!createMut.isPending) createMut.mutate(v) })

  return (
    <Dialog open={open} onOpenChange={v => { if (!v) onClose() }} keepOpen={createMut.isPending}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New dashboard</DialogTitle>
        </DialogHeader>
        <FormField label="Name" error={errors.name?.message} required>
          <Input
            {...register('name')}
            placeholder="Operations"
            autoFocus
            data-testid="dashboard-name"
            onKeyDown={e => {
              if (e.key === 'Enter') {
                e.preventDefault()
                e.stopPropagation()
                submit()
              }
            }}
          />
        </FormField>
        {canShare && (
          <FormField label="Visible to">
            <Controller
              name="side"
              control={control}
              render={({ field }) => (
                <ChoiceToggle
                  value={field.value}
                  onChange={v => { sideChosen.current = true; field.onChange(v) }}
                  aria-label="Visible to"
                  data-testid="dashboard-side"
                  options={[
                    { value: 'shared', label: 'Everyone' },
                    { value: 'mine', label: 'Only me' },
                  ]}
                />
              )}
            />
          </FormField>
        )}
        <DialogFooter className="mt-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={createMut.isPending}>Cancel</Button>
          <Button
            type="button"
            onClick={submit}
            loading={createMut.isPending}
            data-testid="create-dashboard-submit"
          >
            {createMut.isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
