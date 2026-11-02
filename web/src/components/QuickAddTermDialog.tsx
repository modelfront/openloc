import { useEffect } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listJobTermBases, quickAddJobTerm, type QuickAddTermInput, type QuickAddTermResult } from '../api'
import { queryKeys } from '../lib/query-keys'
import { TERM_STATUS_OPTIONS } from '../lib/term-status'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { invalidateTermLookups } from './SegmentRow.shared'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { Alert } from '@/components/ui/alert'
import { submitOnEnter } from '@/lib/submit-on-enter'
import { quickAddBlockedReason } from '@/lib/term-quick-add'

// The editor's term lookup matches source phrases of up to this many words.
const MAX_MATCHED_WORDS = 4

const schema = z.object({
  source: z.string().trim().min(1, 'Source term is required'),
  target: z.string().trim().min(1, 'Target term is required'),
  baseId: z.string().min(1, 'Term base is required'),
  status: z.string(),
  definition: z.string(),
  notes: z.string(),
})
type Values = z.infer<typeof schema>

interface Props {
  open: boolean
  onClose: () => void
  jobId: number
  projectId: number
  /** Managers pick the status; a linguist's term is saved as pending. */
  isManager: boolean
  /** Prefill from the selection in the source and the translation. */
  initialSource: string
  initialTarget: string
}

/**
 * Adds a source/target term pair to one of the job's term bases without
 * leaving the editor. Opened with Alt+Shift+G or the Terminology panel's +.
 */
export function QuickAddTermDialog({ open, onClose, jobId, projectId, isManager, initialSource, initialTarget }: Props) {
  const queryClient = useQueryClient()
  const { data: list, isLoading, isError } = useQuery({
    queryKey: queryKeys.jobTermBases(jobId),
    queryFn: () => listJobTermBases(jobId),
    enabled: open,
  })
  const bases = list?.term_bases

  const { register, handleSubmit, control, reset, watch, getValues, setValue, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { source: '', target: '', baseId: '', status: 'approved', definition: '', notes: '' },
  })

  // Each opening starts from the current selection. The top term base is
  // picked when the list arrives, which may be after the user started typing.
  useEffect(() => {
    if (!open) return
    reset({ source: initialSource, target: initialTarget, baseId: '', status: 'approved', definition: '', notes: '' })
  }, [open, initialSource, initialTarget, reset])
  const firstBaseId = bases?.[0]?.id
  useEffect(() => {
    if (open && firstBaseId && !getValues('baseId')) setValue('baseId', String(firstBaseId))
  }, [open, firstBaseId, getValues, setValue])

  const addMut = useMutationWithToast({
    mutationFn: (input: QuickAddTermInput) => quickAddJobTerm(jobId, input),
    successMessage: (term: QuickAddTermResult) => `Term "${term.source_term}" added`,
    errorMessage: 'Failed to add the term',
    // The open segment looks its terms up again; the Terminology pages refetch.
    onSuccess: (_term: QuickAddTermResult, input: QuickAddTermInput) => {
      invalidateTermLookups(projectId)
      queryClient.invalidateQueries({ queryKey: queryKeys.terminology.all })
      queryClient.invalidateQueries({ queryKey: queryKeys.concepts.allByBase(input.term_base_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.termBaseLanguages(input.term_base_id) })
      onClose()
    },
  })

  const submit = handleSubmit(v => addMut.mutate({
    term_base_id: Number(v.baseId),
    source_term: v.source.trim(),
    target_term: v.target.trim(),
    status: isManager ? v.status : undefined,
    definition: v.definition.trim(),
    notes: v.notes.trim(),
  }))

  const loadFailed = isError && !list
  // The same reasons, worded the same, as the editor's shortcut shows.
  const blocked = list || loadFailed ? quickAddBlockedReason(list, { failed: loadFailed, isManager }) : null
  const sourceWords = watch('source').trim().split(/\s+/).filter(Boolean).length

  return (
    <Dialog open={open} keepOpen={addMut.isPending} onOpenChange={v => { if (!v) onClose() }}>
      <DialogContent className="sm:max-w-md" data-testid="quick-add-term-dialog">
        <DialogHeader>
          <DialogTitle>Add term</DialogTitle>
        </DialogHeader>
        {blocked ? (
          <Alert tone={loadFailed ? 'error' : 'warning'}>{blocked}</Alert>
        ) : (
          <div
            className="flex flex-col gap-3"
            onKeyDown={submitOnEnter(submit)}
          >
            <FormField label="Source term" error={errors.source?.message} required>
              <Input {...register('source')} autoFocus data-testid="quick-add-source" />
            </FormField>
            {sourceWords > MAX_MATCHED_WORDS && (
              <p className="-mt-2 text-xs text-gray-500 dark:text-gray-400">
                Terms of more than {MAX_MATCHED_WORDS} words are not highlighted in segments.
              </p>
            )}
            <FormField label="Target term" error={errors.target?.message} required>
              <Input {...register('target')} data-testid="quick-add-target" />
            </FormField>
            {(bases?.length ?? 0) > 1 && (
              <FormField label="Term base" error={errors.baseId?.message} required>
                <Controller
                  name="baseId"
                  control={control}
                  render={({ field }) => (
                    <Combobox
                      options={(bases ?? []).map(b => ({ value: String(b.id), label: b.name }))}
                      value={field.value}
                      onChange={field.onChange}
                      searchable={false}
                      className="w-full"
                    />
                  )}
                />
              </FormField>
            )}
            {isManager ? (
              <FormField label="Status">
                <Controller
                  name="status"
                  control={control}
                  render={({ field }) => (
                    <Combobox
                      options={TERM_STATUS_OPTIONS}
                      value={field.value}
                      onChange={field.onChange}
                      searchable={false}
                      className="w-full"
                    />
                  )}
                />
              </FormField>
            ) : (
              <p className="text-xs text-gray-500 dark:text-gray-400">
                {list?.linguist_status === 'approved'
                  ? 'The term is saved as approved.'
                  : 'The term is saved as pending until a project manager approves it.'}
              </p>
            )}
            <FormField label="Definition">
              <Textarea {...register('definition')} rows={2} data-testid="quick-add-definition" />
            </FormField>
            <FormField label="Note">
              <Textarea {...register('notes')} rows={2} data-testid="quick-add-notes" />
            </FormField>
          </div>
        )}
        <DialogFooter className="mt-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={addMut.isPending}>
            Cancel
          </Button>
          {!blocked && (
            <Button
              type="button"
              onClick={() => void submit()}
              loading={addMut.isPending}
              disabled={isLoading || !bases?.length}
              disabledReason="Loading the term bases."
              data-testid="quick-add-save"
            >
              Add term
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
