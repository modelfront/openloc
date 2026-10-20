import { useState, useEffect } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { createTMCollection, listLanguages, getMe, type TMCollection } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isManagerRole } from '../lib/roles'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox, MultiCombobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions } from './lang-options'
import { CustomerSelect } from './customer/CustomerSelect'

const tmSchema = z.object({
  name: z.string().min(1, 'TM name is required'),
  source_language: z.string().min(1, 'Source language is required'),
  target_languages: z.array(z.string()).min(1, 'Add at least one target language'),
})
type TMFormValues = z.infer<typeof tmSchema>

interface Props {
  open: boolean
  onClose: () => void
  /** Called with the new TM id after a successful create. */
  onCreated: (id: number) => void
  /** Pre-fill the source language when the dialog opens (e.g. from the project). */
  initialSourceLanguage?: string
  /** Pre-fill the target languages when the dialog opens (e.g. from the project). */
  initialTargetLanguages?: string[]
}

/**
 * Modal dialog to create a new Translation memory without leaving the current page.
 * Opened from the "Create new TM…" action of the TM pickers in project settings,
 * so users create a TM inline.
 *
 * A TM captures a source language (required), one or more target languages
 * (required), and an optional customer — same shape as project creation.
 * customer_id is kept as local state (not in the zod schema) since
 * it's optional, mirroring ProjectCreate.
 */
export function CreateTMDialog({ open, onClose, onCreated, initialSourceLanguage, initialTargetLanguages }: Props) {
  const queryClient = useQueryClient()
  const [customerID, setCustomerID] = useState<number | null>(null)
  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<TMFormValues>({
    resolver: zodResolver(tmSchema),
    defaultValues: { name: '', source_language: initialSourceLanguage ?? '', target_languages: initialTargetLanguages ?? [] },
  })

  // Pre-fill source/target from the project each time the dialog opens. The form
  // is mounted persistently (only `open` toggles), so defaultValues alone won't
  // re-apply on a second open — reset on the open transition.
  useEffect(() => {
    if (open) {
      reset({ name: '', source_language: initialSourceLanguage ?? '', target_languages: initialTargetLanguages ?? [] })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const langOptions = languages ?? []

  // Admin-gated inline "+ Create new customer" in CustomerSelect (read is open
  // to all). Tri-state avoids the cold-load flicker — see ProjectCreate.
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isManager: boolean | undefined = me === undefined ? undefined : isManagerRole(me?.role)

  const createMut = useMutation({
    mutationFn: (values: TMFormValues) =>
      createTMCollection({
        name: values.name.trim(),
        source_language: values.source_language,
        target_languages: values.target_languages,
        customer_id: customerID,
      }),
    onSuccess: (tm) => {
      // Insert the new TM directly into the cache instead of awaiting a refetch.
      // This eliminates the race where onCreated fires before the parent's
      // tmCollections prop updates, causing a brief placeholder flash in the
      // TM picker's field.
      // created_by_name only comes from the list query's JOIN, so stamp it
      // from `me` here — otherwise the fresh row reads "—" until the refetch.
      const optimistic: TMCollection = { ...tm, created_by_name: tm.created_by_name || me?.name || me?.email }
      queryClient.setQueryData<TMCollection[]>(queryKeys.tms.all, (old) =>
        old ? [optimistic, ...old] : [optimistic],
      )
      // Still invalidate so other subscribers (TMList page) refetch on next mount.
      queryClient.invalidateQueries({ queryKey: queryKeys.tms.all })
      toast.success(`TM "${tm.name}" created`)
      handleReset()
      onCreated(tm.id)
    },
    onError: (err: Error) => {
      toast.error(`Failed to create TM: ${err.message}`)
    },
  })

  const handleReset = () => {
    reset()
    setCustomerID(null)
  }

  const handleClose = () => {
    handleReset()
    onClose()
  }

  // No nested `<form>`: `DialogPortal` renders this dialog to
  // document.body, but React synthetic events (including `submit`) still
  // bubble through the REACT component tree into a parent form such as
  // ProjectCreate's. So this is a `<div>`, and the Create button
  // (type="button") calls react-hook-form's handleSubmit. Enter-to-submit is
  // scoped to the name input only (below) so it doesn't fire while
  // the user is picking languages in a combobox popover.
  const submit = handleSubmit((data) => createMut.mutate(data))

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Create translation memory</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <FormField label="Name" error={errors.name?.message} required>
            <Input
              {...register('name')}
              placeholder="TM name"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  e.stopPropagation()
                  submit()
                }
              }}
            />
          </FormField>

          {/* Source + Target languages on one row. The multi-select is
              fixed-height (overflow collapses to "+N"), so it fits the
              half-width column. Customer gets its own row below. */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <FormField label="Source language" error={errors.source_language?.message} required>
              <Controller
                name="source_language"
                control={control}
                render={({ field }) => (
                  <div data-testid="tm-create-source">
                    <Combobox
                      options={toLangOptions(langOptions)}
                      value={field.value}
                      onChange={field.onChange}
                      placeholder="Source language"
                      className="w-full"
                      {...langPickerProps}
                    />
                  </div>
                )}
              />
            </FormField>
            <FormField label="Target languages" error={errors.target_languages?.message} required>
              <Controller
                name="target_languages"
                control={control}
                render={({ field }) => (
                  <div data-testid="tm-create-target" className="w-full">
                    <MultiCombobox
                      options={toLangOptions(langOptions)}
                      value={field.value}
                      onChange={field.onChange}
                      placeholder="Add target languages..."
                      className="w-full"
                      {...langPickerProps}
                    />
                  </div>
                )}
              />
            </FormField>
          </div>

          <FormField label="Customer (optional)">
            <CustomerSelect
              value={customerID}
              onChange={setCustomerID}
              canCreate={isManager === true}
              disabled={isManager === undefined}
              triggerClassName="w-full"
            />
          </FormField>

          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              type="button"
              onClick={() => submit()}
              disabled={createMut.isPending}
            >
              {createMut.isPending ? 'Creating...' : 'Create'}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
