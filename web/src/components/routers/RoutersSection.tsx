// One list + dialog for a router library, shared by AI routers (target
// language → QE+APE engine, what a workflow step points at) and MT routers
// (target language → MT engine, what a project points at). The two differ
// only in their API and engine list, which the adapter props carry; the
// dialog's rows are "languages → engine", with "All languages" in a row making
// its engine the default (lib/router-rows.ts). Built from gallery primitives
// (Dialog, Input, Combobox, Chip, AddItemButton, DeleteButton) plus the
// static section header (ListSection).
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useForm, useFieldArray, Controller, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery, type QueryKey } from '@tanstack/react-query'
import { ArrowRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useAttentionTarget } from '@/hooks/useAttention'
import { listLanguages } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { uniqueName } from '../../lib/ai-auto-name'
import { ALL_LANGUAGES, EMPTY_ROW, exclusiveAll, shapeToRows, rowsToShape, type RouterShape } from '../../lib/router-rows'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { useScrollToError } from '../../hooks/useScrollToError'
import { toLangOptions, langPickerProps } from '../lang-options'
import ConfirmDialog from '../ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { EmptyState } from '@/components/layout/EmptyState'
import { ListSection } from '@/components/layout/SectionHeader'
import { Combobox, MultiCombobox, type ComboboxOption, type ComboboxAction } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { chipSurfaceClass } from '@/components/ui/chip'
import { DeleteButton } from '@/components/ui/delete-button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AddItemButton } from '@/components/list/AddItemButton'
import { listRowClass } from '@/components/list/list-row'
import { rowButtonProps } from '@/components/list/row-button'
import { RouterEngineChip } from '../ai/ai-engine-chips'
import type { UnusableReason } from '../../api'

/** The minimum a router row needs from its API shape. */
export interface RouterLike {
  id: number
  name: string
  /**
   * Why this router cannot fully translate, computed server-side.
   * One source, so the list, the pickers on the project
   * and template forms, and the workflow step dialog cannot disagree.
   */
  unusable?: UnusableReason
}

/** What tells the shared section apart per router kind. */
export interface RouterAdapter<R extends RouterLike, I> {
  /** "AI router" / "MT router" — titles, buttons, toasts. */
  noun: string
  /**
   * Section heading. Omit it when the tab already names the list: the section
   * then renders with no header and the create button sits above the body.
   */
  title?: string
  /** Test-id prefix: `${prefix}-add`, `${prefix}-form`, `${prefix}-rule-${i}`… */
  testIdPrefix: string
  /** Shown next to the heading, so a headerless section has nowhere for it. */
  info?: ReactNode
  deleteDescription: string
  queryKey: QueryKey
  list: () => Promise<R[]>
  create: (input: I) => Promise<unknown>
  update: (id: number, input: I) => Promise<unknown>
  remove: (id: number) => Promise<unknown>
  /** Every key a mutation stales (the router list, plus whatever names routers). */
  invalidateKeys: QueryKey[]
  /**
   * The engines a route may point at; `value` is the engine key.
   *
   * Includes UNUSABLE ones, carrying their reason. The picker hides them from
   * new selections but keeps the one a rule already holds, so a rule never
   * renders blank just because its engine broke.
   */
  engineOptions: (ComboboxOption & { unusable?: string })[]
  /**
   * Pinned row under the picker's search — "New AI engine". Optional because
   * a router whose engines come from more than one place has no single thing
   * to create.
   */
  engineAction?: ComboboxAction
  emptyEngineText: string
  toShape: (router: R) => RouterShape
  fromShape: (name: string, shape: RouterShape) => I
}

const ALL_LANGUAGES_OPTION: ComboboxOption = {
  value: ALL_LANGUAGES, label: 'All languages', chip: 'All (default)', shortLabel: 'All (default)', keywords: 'all languages default *',
}
const rowSchema = z.object({
  langs: z.array(z.string()),
  engine: z.string(),
})

// Every row must be complete — a blank or half-filled one is flagged on save,
// not silently dropped. Duplicate languages and a second "all" are refused
// here before the server is.
const routerSchema = z
  .object({
    name: z.string().trim().min(1, 'Name is required'),
    // Never empty: the last route cannot be removed in the UI, and the server
    // refuses a router with nothing to route to.
    rows: z.array(rowSchema).min(1, 'Add at least one route'),
  })
  .superRefine((v, ctx) => {
    const seen = new Set<string>()
    v.rows.forEach((r, i) => {
      if (r.langs.length === 0) {
        ctx.addIssue({ code: 'custom', message: 'Pick at least one language', path: ['rows', i, 'langs'] })
      }
      for (const lang of r.langs) {
        if (seen.has(lang)) {
          ctx.addIssue({
            code: 'custom',
            message: lang === ALL_LANGUAGES ? 'Only one row can cover all languages' : 'A language is listed twice',
            path: ['rows', i, 'langs'],
          })
          break
        }
        seen.add(lang)
      }
      if (r.engine === '') {
        ctx.addIssue({ code: 'custom', message: 'Pick an engine', path: ['rows', i, 'engine'] })
      }
    })
  })
type RouterFormValues = z.infer<typeof routerSchema>

export function RoutersSection<R extends RouterLike, I>({
  canWrite, adapter, createOnMount, onCreateClosed,
}: {
  canWrite: boolean
  adapter: RouterAdapter<R, I>
  /** Open the create dialog on mount, for the /new route a picker links to. */
  createOnMount?: boolean
  /** Fires when that dialog closes, so the route can go back to the list. */
  onCreateClosed?: () => void
}) {
  const p = adapter.testIdPrefix
  // `initial` is kept while the dialog animates closed — clearing it at once
  // flips the title and button to the create wording mid-fade. Every open
  // sets it afresh.
  const [dialog, setDialog] = useState<{ open: boolean; initial?: R }>({ open: !!createOnMount })
  // Saved or cancelled, the dialog closes the same way — and on a /new route
  // that also has to put the URL back, or reopening it would be a no-op.
  const closeDialog = () => { setDialog(d => ({ ...d, open: false })); onCreateClosed?.() }
  // The target outlives the confirm dialog's close animation — clearing it
  // on confirm would show 'Delete "undefined"?' mid-fade — so open is its own
  // flag and the target is simply replaced on the next delete.
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const askDelete = (target: { id: number; name: string }) => { setDeleteTarget(target); setConfirmOpen(true) }

  const { data: routers, isLoading, isError, error, refetch } = useQuery({ queryKey: adapter.queryKey, queryFn: adapter.list })
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })

  const createMut = useMutationWithToast({
    mutationFn: (input: I) => adapter.create(input),
    successMessage: 'Router created',
    errorMessage: 'Create failed',
    invalidateKeys: adapter.invalidateKeys,
    onSuccess: () => closeDialog(),
  })
  const updateMut = useMutationWithToast({
    mutationFn: ({ id, input }: { id: number; input: I }) => adapter.update(id, input),
    successMessage: 'Router updated',
    errorMessage: 'Update failed',
    invalidateKeys: adapter.invalidateKeys,
    onSuccess: () => closeDialog(),
  })
  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => adapter.remove(id),
    successMessage: 'Router deleted',
    errorMessage: 'Delete failed',
    invalidateKeys: adapter.invalidateKeys,
    onSuccess: () => setConfirmOpen(false),
  })

  const body = isLoading ? (
    <LoadingPlaceholder />
  ) : isError && !routers ? (
    <LoadFailed what={`${adapter.noun}s`} error={error} onRetry={() => refetch()} />
  ) : (
    <>
      <div className="space-y-2" data-testid={`${p}s-list`}>
        {routers?.map(router => (
          <RouterCard
            key={router.id}
            router={router}
            unusable={router.unusable ?? ''}
            testId={`${p}-${router.id}`}
            canWrite={canWrite}
            onEdit={() => setDialog({ open: true, initial: router })}
            onDelete={() => askDelete({ id: router.id, name: router.name })}
          />
        ))}
        {routers?.length === 0 && <EmptyState message={`No ${adapter.noun}s yet.`} />}
      </div>

      <RouterDialog
        open={dialog.open}
        initial={dialog.initial}
        adapter={adapter}
        languages={languages ?? []}
        existingNames={(routers ?? []).filter(r => r.id !== dialog.initial?.id).map(r => r.name)}
        onClose={closeDialog}
        onSubmit={input => dialog.initial ? updateMut.mutate({ id: dialog.initial.id, input }) : createMut.mutate(input)}
        isPending={createMut.isPending || updateMut.isPending}
      />

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={open => !open && setConfirmOpen(false)}
        title={`Delete router "${deleteTarget?.name}"?`}
        description={adapter.deleteDescription}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />
    </>
  )

  return (
    <ListSection
      layout="static"
      title={adapter.title}
      info={adapter.info}
      action={canWrite && (
        <Button size="sm" onClick={() => setDialog({ open: true, initial: undefined })} data-testid={`${p}-add`}>New {adapter.noun}</Button>
      )}
      data-testid={`${p}s-section`}
    >
      {body}
    </ListSection>
  )
}

// One router per row: its name, and a warning when it routes to an engine that
// cannot translate — what it routes to in detail is in the dialog.
function RouterCard({ router, unusable, testId, canWrite, onEdit, onDelete }: {
  router: RouterLike
  unusable: UnusableReason
  testId: string
  canWrite: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <div
      // A link elsewhere can open this page pointing at the card (its key is
      // the test id, e.g. `ai-router-12`).
      ref={useAttentionTarget(testId)}
      className={cn(listRowClass, canWrite && 'cursor-pointer transition-colors hover:bg-muted')}
      {...rowButtonProps(canWrite ? onEdit : undefined)}
      data-testid={testId}
    >
      <span className="flex min-w-0 flex-1 items-center gap-2">
        <span className="min-w-0 truncate font-medium text-gray-900 dark:text-gray-100">{router.name}</span>
        {/* Visible without opening the router — otherwise a router that can no
            longer translate looks exactly like one that can. */}
        <RouterEngineChip reason={unusable} />
      </span>
      {canWrite && (
        <DeleteButton
          className="shrink-0"
          aria-label="Delete"
          title="Delete"
          onClick={e => { e.stopPropagation(); onDelete() }}
        />
      )}
    </div>
  )
}

// A new router starts as one route, "All languages" → the first engine, so
// the common case is one click away and a router can never be saved empty.
function defaultsFrom<R extends RouterLike, I>(initial: R | undefined, existingNames: string[], adapter: RouterAdapter<R, I>): RouterFormValues {
  return {
    name: initial?.name ?? uniqueName(`New ${adapter.noun}`, existingNames),
    rows: initial ? shapeToRows(adapter.toShape(initial)) : [{ langs: [ALL_LANGUAGES], engine: adapter.engineOptions[0]?.value ?? '' }],
  }
}

function RouterDialog<R extends RouterLike, I>({ open, initial, adapter, languages, existingNames, onClose, onSubmit, isPending }: {
  open: boolean
  /** Existing router to edit; undefined = create. */
  initial?: R
  adapter: RouterAdapter<R, I>
  languages: { code: string; name: string }[]
  /** Names already taken, so the autofilled one avoids the unique constraint. */
  existingNames: string[]
  onClose: () => void
  onSubmit: (input: I) => void
  isPending: boolean
}) {
  const p = adapter.testIdPrefix
  const langOptions = useMemo(() => toLangOptions(languages), [languages])

  const { register, control, reset, handleSubmit, formState: { errors, isDirty } } = useForm<RouterFormValues>({
    resolver: zodResolver(routerSchema),
    defaultValues: defaultsFrom(initial, existingNames, adapter),
  })
  useScrollToError(errors)
  // The dialog is mounted persistently; re-seed the fields on every open.
  useEffect(() => {
    if (open) reset(defaultsFrom(initial, existingNames, adapter))
    // existingNames / adapter change on every list refetch; only an open should re-seed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial, reset])
  const { fields, append, remove } = useFieldArray({ control, name: 'rows' })
  const rows = useWatch({ control, name: 'rows' })
  // Engine key → why it cannot translate. Built once per render rather than
  // scanned per row, since a router with many routes would otherwise walk the
  // whole option list for each one.
  const unusableByValue = useMemo(
    () => new Map(adapter.engineOptions.map(o => [o.value, o.unusable ?? ''])),
    [adapter.engineOptions],
  )
  // A language picked on one row is not offered on the others — "All
  // languages" included, so only one row can be the default.
  const takenBy = useMemo(() => {
    const m = new Map<string, number>()
    ;(rows ?? []).forEach((r, i) => r.langs.forEach(l => m.set(l, i)))
    return m
  }, [rows])

  const submit = handleSubmit(values => onSubmit(adapter.fromShape(values.name, rowsToShape(values.rows))))

  return (
    <Dialog open={open} onOpenChange={o => !o && onClose()} keepOpen={isPending}>
      {/* No field is focused on open: the dialog leaves focus alone instead
          of jumping into the Name field. */}
      <DialogContent className="sm:max-w-2xl" initialFocus={false} data-testid={`${p}-form`}>
        <DialogHeader>
          <DialogTitle>{initial ? `Edit ${adapter.noun}` : `New ${adapter.noun}`}</DialogTitle>
        </DialogHeader>
        <form className="space-y-4" onSubmit={e => { e.preventDefault(); void submit() }}>
          <FormField label="Name" error={errors.name?.message} required>
            <Input {...register('name')} placeholder="e.g. Production" data-testid={`${p}-name`} />
          </FormField>

          <div className="space-y-2">
            {/* Styled like a FormField label: a field group, not a page section. */}
            <div className="text-xs text-gray-500 dark:text-gray-400">Routes</div>
            {fields.map((field, i) => {
              const rowErr = errors.rows?.[i]
              const langsErr = rowErr?.langs?.message ?? rowErr?.langs?.root?.message
              // The engine this row currently points at, if it cannot translate.
              // From the watched values, not from `field`: picking another
              // engine has to clear the highlight straight away, and `fields`
              // holds the values the array was initialised with.
              const rowUnusable = unusableByValue.get(rows?.[i]?.engine ?? '') ?? ''
              return (
                // Centred: the language picker wraps to several rows when it
                // holds many chips, and the arrow, engine and trash sit in the
                // middle of it rather than hanging off its first line.
                //
                // A broken row is TINTED as well as chipped: in a router with
                // several routes the chip alone makes you read every line to
                // find the one that is wrong.
                <div
                  key={field.id}
                  className={cn(
                    'flex items-center gap-2 rounded-md',
                    rowUnusable && '-mx-2 px-2 py-1',
                    rowUnusable === 'no_key' && chipSurfaceClass('red'),
                    rowUnusable === 'disabled' && chipSurfaceClass('yellow'),
                  )}
                  data-unusable={rowUnusable || undefined}
                  data-testid={`${p}-rule-${i}`}
                >
                  {/* An incomplete route is shown by the red frame alone. */}
                  <div className="w-72 min-w-0">
                    <Controller
                      name={`rows.${i}.langs`}
                      control={control}
                      render={({ field: f }) => (
                        <MultiCombobox
                          {...langPickerProps}
                          wrap
                          value={f.value}
                          onChange={next => f.onChange(exclusiveAll(f.value, next))}
                          options={[ALL_LANGUAGES_OPTION, ...langOptions].filter(o => (takenBy.get(o.value) ?? i) === i)}
                          placeholder="Target languages…"
                          className="w-full"
                          aria-invalid={!!langsErr}
                          aria-label="Target languages"
                        />
                      )}
                    />
                  </div>
                  <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <div className="flex-1 min-w-0">
                    <Controller
                      name={`rows.${i}.engine`}
                      control={control}
                      render={({ field: f }) => (
                        <Combobox
                          value={f.value}
                          onChange={f.onChange}
                          // Usable engines, plus whatever this rule already
                          // points at: you cannot newly route to something
                          // broken, but you can see and fix the one that broke.
                          options={adapter.engineOptions.filter(o => !o.unusable || o.value === f.value)}
                          actions={adapter.engineAction ? [adapter.engineAction] : undefined}
                          placeholder="Engine…"
                          emptyText={adapter.emptyEngineText}
                          className="w-full"
                          aria-invalid={!!rowErr?.engine}
                          contentClassName="w-[28rem]"
                          aria-label="Engine"
                          data-testid={`${p}-rule-${i}-engine`}
                        />
                      )}
                    />
                  </div>
                  <DeleteButton
                    type="button"
                    size="icon-xs"
                    className="shrink-0"
                    aria-label="Remove route"
                    title={fields.length === 1 ? 'A router needs at least one route' : 'Remove route'}
                    disabled={fields.length === 1}
                    onClick={() => remove(i)}
                    data-testid={`${p}-rule-${i}-remove`}
                  />
                </div>
              )
            })}
            {/* As tall as the pickers above it, not a full list row: inside the
                dialog it is a control among controls. */}
            <AddItemButton
              label="Add route"
              onClick={() => append(EMPTY_ROW)}
              testId={`${p}-rule-add`}
              className="min-h-input"
            />
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={isPending}>Cancel</Button>
            {/* Update stays disabled until something changed; Create is always live. */}
            <Button type="submit" disabled={isPending || (!!initial && !isDirty)} data-testid={`${p}-submit`}>
              {isPending ? 'Saving…' : initial ? 'Update' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
