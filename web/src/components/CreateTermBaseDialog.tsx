import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { createTermBase, getMe, listLanguages, type TermBase } from '../api'
import { queryKeys } from '../lib/query-keys'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions } from './lang-options'

const termBaseSchema = z.object({
  name: z.string().min(1, 'Term base name is required'),
  srcLang: z.string().min(1, 'Source language is required'),
})
type TermBaseFormValues = z.infer<typeof termBaseSchema>

interface Props {
  open: boolean
  onClose: () => void
  /** Called with the new Term base id after a successful create. */
  onCreated: (id: number) => void
  /** Pre-fill source language (e.g. from the assignment row being edited). */
  initialSrcLang?: string
  /** Pre-fill target language (e.g. from the assignment row being edited). */
  initialTgtLang?: string
}

/**
 * Modal dialog to create a new Term base without leaving the current page.
 * Opened from the "Create new…" action of the term-base pickers in AssignmentSection. Pre-fills source/target
 * languages from the row context so the user doesn't re-type them.
 */
export function CreateTermBaseDialog({ open, onClose, onCreated, initialSrcLang = '' }: Props) {
  const queryClient = useQueryClient()
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })

  const {
    register,
    handleSubmit,
    control,
    reset,
    formState: { errors },
  } = useForm<TermBaseFormValues>({
    resolver: zodResolver(termBaseSchema),
    defaultValues: { name: '', srcLang: initialSrcLang },
    values: { name: '', srcLang: initialSrcLang },
  })

  const createMut = useMutation({
    mutationFn: (data: TermBaseFormValues) => createTermBase(data.name.trim(), '', data.srcLang),
    onSuccess: (tb) => {
      // Insert the new TB directly into the cache to avoid the refetch race
      // (see comment in CreateTMDialog for details).
      // POST returns the raw term_bases row, but the LIST query derives three
      // more fields. Fill them in, because TerminologyList renders
      // `base.languages.length` and the concept count directly — copying the
      // bare response in crashed the page for anyone who reached the list
      // before the invalidation refetch replaced the row.
      //   languages   — distinct languages across the base's terms; a brand-new
      //                 base has none, so [].
      //   entry_count — concepts, likewise 0.
      //   created_by_name — the JOIN on users; stamp it from `me` so the fresh
      //                 row shows the creator instead of "—".
      const optimistic: TermBase = {
        ...tb,
        languages: tb.languages ?? [],
        entry_count: tb.entry_count ?? 0,
        created_by_name: tb.created_by_name || me?.name || me?.email,
      }
      queryClient.setQueryData<TermBase[]>(queryKeys.terminology.all, (old) =>
        old ? [optimistic, ...old] : [optimistic],
      )
      queryClient.invalidateQueries({ queryKey: queryKeys.terminology.all })
      toast.success(`Term base "${tb.name}" created`)
      reset()
      onCreated(tb.id)
    },
    onError: (err: Error) => {
      toast.error(`Failed to create Term base: ${err.message}`)
    },
  })

  const handleClose = () => {
    reset()
    onClose()
  }

  // Same React portal event-bubble guard as CreateTMDialog.tsx:
  // No nested `<form>`, `type="button"` + onClick, dialog-scoped Enter handler.
  const submit = handleSubmit((data) => createMut.mutate(data))

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create term base</DialogTitle>
        </DialogHeader>
        <div
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              e.stopPropagation()
              submit()
            }
          }}
        >
          <FormField label="Name" error={errors.name?.message} required>
            <Input
              {...register('name')}
              placeholder="Term base name"
              autoFocus
            />
          </FormField>
          <div className="mt-3">
            <FormField label="Source language" error={errors.srcLang?.message} required>
              <Controller
                name="srcLang"
                control={control}
                render={({ field }) => (
                  <Combobox
                    options={toLangOptions(languages ?? [])}
                    value={field.value}
                    onChange={field.onChange}
                    placeholder="Source"
                    className="w-full"
                    {...langPickerProps}
                  />
                )}
              />
            </FormField>
            <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
              Target languages are set automatically from the terms you add or import.
            </p>
          </div>
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
