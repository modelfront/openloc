// Create / edit one step definition. The only place a
// workflow step gets a name, a type and (hybrid / ai) an AI router.
import { useEffect, useMemo, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useForm, useWatch, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import { listAIRouters, STEP_TYPES, type StepDefinitionInput, type StepDefinitionRow, type StepType } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useEngineCreateActions } from '../../hooks/useEngineCreateActions'
import { useScrollToError } from '../../hooks/useScrollToError'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { RouterEngineChip } from '../ai/ai-engine-chips'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { cn } from '@/lib/utils'
import { STEP_TYPE_CHIPS } from '@/components/ui/chip-presets'
import { errorText } from '@/lib/error-text'

const STEP_TYPE_VALUES = STEP_TYPES.map(t => t.value) as [StepType, ...StepType[]]

const schema = z
  .object({
    name: z.string().trim().min(1, 'Name is required'),
    step_type: z.enum(STEP_TYPE_VALUES),
    ai_router_id: z.number().nullable(),
  })
  .refine(v => v.step_type === 'human' || v.ai_router_id != null, {
    message: 'AI router is required',
    path: ['ai_router_id'],
  })
type FormValues = z.infer<typeof schema>

function defaultsFrom(initial?: StepDefinitionRow): FormValues {
  return {
    name: initial?.name ?? '',
    step_type: (initial?.step_type as StepType | undefined) ?? 'human',
    ai_router_id: initial?.ai_router_id ?? null,
  }
}

interface Props {
  open: boolean
  onClose: () => void
  /** Existing definition to edit; undefined = create. */
  initial?: StepDefinitionRow
  onSubmit: (input: StepDefinitionInput) => Promise<void>
}

export function StepDefinitionDialog({ open, onClose, initial, onSubmit }: Props) {
  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting, isDirty },
  } = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: defaultsFrom(initial) })
  useScrollToError(errors)
  const [serverError, setServerError] = useState<string | null>(null)

  // The dialog is mounted persistently; re-seed the fields on every open.
  useEffect(() => {
    if (open) reset(defaultsFrom(initial))
  }, [open, initial, reset])
  useOnChange([open, initial], () => { if (open) setServerError(null) })

  const { data: routers } = useQuery({ queryKey: queryKeys.aiRouters.all, queryFn: listAIRouters })
  const { aiRouter } = useEngineCreateActions()
  const aiRouterActions = useMemo(() => [aiRouter()], [aiRouter])

  const [stepType, routerID] = useWatch({ control, name: ['step_type', 'ai_router_id'] })
  const runsAI = stepType !== 'human'
  const routerStale = routerID != null && routers !== undefined && !routers.some(r => r.id === routerID)
  const submit = handleSubmit(async values => {
    setServerError(null)
    try {
      await onSubmit({ name: values.name, step_type: values.step_type, ai_router_id: runsAI ? values.ai_router_id : null })
      onClose()
    } catch (err) {
      setServerError(errorText(err, 'Save failed'))
    }
  })

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()} keepOpen={isSubmitting}>
      {/* No field is focused on open: the dialog leaves focus alone instead
          of jumping into the Name field. */}
      <DialogContent className="sm:max-w-lg" initialFocus={false} data-testid="step-definition-dialog">
        <DialogHeader>
          <DialogTitle>{initial ? 'Edit step' : 'New step'}</DialogTitle>
        </DialogHeader>
        <form className="space-y-4" onSubmit={e => { e.preventDefault(); void submit() }}>
          <Controller
            name="step_type"
            control={control}
            render={({ field }) => (
              <div role="radiogroup" aria-label="Step type" className="grid grid-cols-3 gap-2" data-field="step_type">
                {STEP_TYPES.map(t => {
                  const StepTypeChip = STEP_TYPE_CHIPS[t.value]
                  return (
                  // eslint-disable-next-line no-restricted-syntax -- step-type segmented control option, not an action button
                  <button
                    key={t.value}
                    type="button"
                    role="radio"
                    aria-checked={field.value === t.value}
                    onClick={() => field.onChange(t.value)}
                    data-testid={`step-type-option-${t.value}`}
                    className={cn(
                      'rounded-md border p-2 text-left text-xs transition-colors',
                      field.value === t.value
                        ? 'border-primary bg-primary/5 dark:bg-primary/10'
                        : 'border-gray-200 dark:border-gray-800 hover:border-gray-300 dark:hover:border-gray-700',
                    )}
                  >
                    <StepTypeChip />
                    <p className="mt-1.5 text-gray-500 dark:text-gray-400 leading-snug">{t.description}</p>
                  </button>
                  )
                })}
              </div>
            )}
          />

          <FormField label="Name" error={errors.name?.message} required>
            <Input
              {...register('name')}
              placeholder="e.g. Review"
              data-testid="step-definition-name"
            />
          </FormField>

          {runsAI && (
            <FormField label="AI router" error={errors.ai_router_id?.message} required>
              <Controller
                name="ai_router_id"
                control={control}
                render={({ field }) => (
                  <div data-field="ai_router_id">
                    <Combobox
                      value={field.value != null ? String(field.value) : ''}
                      onChange={v => field.onChange(v === '' ? null : Number(v))}
                      options={[
                        ...(routerStale ? [{ value: String(field.value), label: '(deleted router — pick replacement)' }] : []),
                        // Same warning the routers page shows, so a step is not
                        // pointed at a router that cannot run.
                        ...(routers ?? []).map(r => ({
                          value: String(r.id),
                          label: r.name,
                          chip: r.unusable ? <RouterEngineChip reason={r.unusable} /> : undefined,
                        })),
                      ]}
                      placeholder="Choose an AI router…"
                      emptyText="No AI routers yet — create one on the AI page."
                      actions={aiRouterActions}
                      className="w-full"
                      aria-invalid={routerStale}
                      contentClassName="w-[28rem]"
                      aria-label="AI router"
                      data-testid="step-definition-router"
                    />
                  </div>
                )}
              />
              <p className="mt-1 text-xs text-muted-foreground">
                The router picks the QE+APE engine for each job by its target language.
              </p>
            </FormField>
          )}

          {serverError && <Alert tone="error">{serverError}</Alert>}

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={isSubmitting}>Cancel</Button>
            {/* Update stays disabled until something changed; Create is always live. */}
            <Button type="submit" disabled={isSubmitting || (!!initial && !isDirty)} data-testid="step-definition-submit">
              {isSubmitting ? 'Saving…' : initial ? 'Update' : 'Create step'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
