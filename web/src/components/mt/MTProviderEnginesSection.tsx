// Provider MT engines.
//
// An engine is a CONFIGURATION, not a projection of a key: it names a key from
// AI settings and carries the request options that key's provider supports. So
// one DeepL account backs as many engines as you have ways of using it — formal
// and informal, glossary and none — and a router sends each language to the one
// it should use.
//
// The options panel is the SAME component the LLM model editor renders
// (`ModelSettingsPanel`), fed from the backend catalog rather than a list held
// here: a provider gaining an option is then a Go change alone.
import { useState } from 'react'
import { useForm, useWatch, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import {
  listMTEngines, listMTEngineCatalog, createMTEngine, updateMTEngine, deleteMTEngine,
  listAIVendors,
  type MTEngine, type MTEngineInput, type MTEngineSettings,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../ConfirmDialog'
import { SectionHeader } from '@/components/layout/SectionHeader'
import { SectionInfo } from '@/components/ui/section-info'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { loadFailure } from '@/lib/load-failure'
import { EmptyState } from '@/components/layout/EmptyState'
import { listRowClass } from '@/components/list/list-row'
import { rowButtonProps } from '@/components/list/row-button'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { ModelSettingsPanel } from '../ai/model-preset-ui'
import { ProviderModelChip } from '../ai/ProviderModelChip'
import { EngineUnusableChip } from '../ai/ai-engine-chips'
import { TextLink } from '@/components/ui/text-link'

export function MTProviderEnginesSection({ canWrite }: { canWrite: boolean }) {
  const [showCreate, setShowCreate] = useState(false)
  const [editingId, setEditingId] = useState<number | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<MTEngine | null>(null)

  const enginesQuery = useQuery({ queryKey: queryKeys.mtEngines.all, queryFn: listMTEngines })
  const catalogQuery = useQuery({ queryKey: queryKeys.mtEngines.catalog, queryFn: listMTEngineCatalog })
  const vendorsQuery = useQuery({ queryKey: queryKeys.aiVendors.all, queryFn: listAIVendors })
  const { data: engines } = enginesQuery
  const { data: catalog } = catalogQuery
  const { data: vendors } = vendorsQuery
  // Both forms seed their provider from the catalog and their key list from the
  // keys when they mount, so neither opens until all three lists are in.
  const isLoading = enginesQuery.isLoading || catalogQuery.isLoading || vendorsQuery.isLoading

  // The catalog and the keys build the create form's key list, so either one
  // failing fails the section like the engine list does — the form would
  // otherwise offer no keys.
  const failure = loadFailure(enginesQuery, catalogQuery, vendorsQuery)

  // Looked up from the list on every render rather than snapshotted, so a save
  // that refetches is reflected in the open dialog.
  const editing = (engines ?? []).find(e => e.id === editingId)

  // Every key the org holds with a provider this page can build an engine for,
  // flattened so the form looks one up by id without walking vendors again.
  const keys = (catalog ?? []).flatMap(c =>
    (vendors ?? []).filter(v => v.provider === c.engine_type).flatMap(v =>
      v.tokens.map(t => ({ id: t.id, name: t.name, engineType: c.engine_type })),
    ),
  )

  const createMut = useMutationWithToast({
    mutationFn: (input: MTEngineInput) => createMTEngine(input),
    successMessage: 'Engine created', errorMessage: 'Create failed',
    invalidateKeys: [queryKeys.mtEngines.all], onSuccess: () => setShowCreate(false),
  })
  const updateMut = useMutationWithToast({
    mutationFn: ({ id, input }: { id: number; input: Partial<MTEngineInput> }) => updateMTEngine(id, input),
    successMessage: 'Engine updated', errorMessage: 'Update failed',
    invalidateKeys: [queryKeys.mtEngines.all], onSuccess: () => setEditingId(null),
  })
  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => deleteMTEngine(id),
    successMessage: 'Engine deleted', errorMessage: 'Delete failed',
    invalidateKeys: [queryKeys.mtEngines.all], onSuccess: () => setDeleteTarget(null),
  })
  return (
    <section data-testid="mt-vendor-engines-section">
      <SectionHeader
        title="MT"
        info={
          <SectionInfo label="About MT engines">
            Machine translation from a provider. An engine uses one key from AI settings and
            the options that provider supports, so the same account can back several engines
            that translate differently. The switch turns one off without deleting it.
          </SectionInfo>
        }
        // Same slot and same button as the LLM section below, so the two lists
        // on this page are created the same way.
        action={canWrite && (
          <Button size="sm" onClick={() => { setEditingId(null); setShowCreate(true) }} disabled={isLoading || !!failure} data-testid="mt-engine-add">
            New MT engine
          </Button>
        )}
      />
      {/* Keys live on the other page, so say where — the Provider field here
          offers nothing until one exists. */}
      <p className="mb-3 text-sm text-muted-foreground" data-testid="mt-engines-hint">
        Add provider keys in{' '}
        <TextLink to="/ai-settings">AI settings</TextLink>.
      </p>

      {isLoading ? (
        <LoadingPlaceholder />
      ) : failure ? (
        <LoadFailed what="MT engines" error={failure.error} onRetry={failure.retry} />
      ) : (
        <div className="space-y-2">
          {(engines ?? []).map(engine => (
            <EngineRow
              key={engine.id}
              engine={engine}
              canWrite={canWrite}
              onEdit={() => setEditingId(engine.id)}
              onDelete={() => setDeleteTarget(engine)}
            />
          ))}
          {engines?.length === 0 && <EmptyState message="No MT engines yet." />}
        </div>
      )}

      {/* Create and edit open the same dialog, mounted only while open so the
          form seeds from `initial` fresh each time. */}
      {showCreate && (
        <EngineForm
          keys={keys}
          catalog={catalog ?? []}
          onSubmit={input => createMut.mutate(input as MTEngineInput)}
          onCancel={() => setShowCreate(false)}
          saving={createMut.isPending}
        />
      )}
      {editing && (
        <EngineForm
          initial={editing}
          keys={keys}
          catalog={catalog ?? []}
          onSubmit={input => updateMut.mutate({ id: editing.id, input })}
          onCancel={() => setEditingId(null)}
          saving={updateMut.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete "${deleteTarget?.name}"?`}
        description="MT routers that send a language to this engine must be changed first."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />
    </section>
  )
}

type KeyOption = { id: number; name: string; engineType: string }

function EngineRow({ engine, canWrite, onEdit, onDelete }: {
  engine: MTEngine
  canWrite: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    // The WHOLE row is the button, tinting on hover — same composition as the
    // LLM engine card below it, so the two lists on this page behave alike.
    <div
      className={cn(
        listRowClass,
        canWrite && 'cursor-pointer transition-colors hover:bg-muted',
      )}
      {...rowButtonProps(canWrite ? onEdit : undefined)}
      data-testid={`mt-engine-row-${engine.id}`}
    >
      {/* Its own testid, because the row's text is ambiguous: the provider chip
          repeats the name a LibreTranslate engine already carries. */}
      <div className="flex min-w-0 flex-wrap items-center gap-2" data-testid={`mt-engine-open-${engine.id}`}>
        <span className="truncate font-medium text-gray-900 dark:text-gray-100">{engine.name}</span>
        {/* Which provider this engine calls. Its own name is free text and need
            not mention one, so without this the list cannot be read at a glance.
            Same chip the AI-engine list uses, minus the model and mode. */}
        <ProviderModelChip provider={engine.engine_type} />
        {/* Why it cannot translate, if it cannot. Visible without opening the
            row — otherwise a broken engine looks identical to a working one —
            and the same chip every router shows for the same engine. */}
        <EngineUnusableChip reason={engine.unusable} />
      </div>
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

const engineSchema = z.object({
  name: z.string().min(1, 'Name is required'),
  engine_type: z.string().min(1, 'Choose a provider'),
  token_id: z.number().nullable(),
  settings: z.record(z.string(), z.unknown()),
  enabled: z.boolean(),
})
type EngineFormValues = z.infer<typeof engineSchema>

function EngineForm({ initial, keys, catalog, onSubmit, onCancel, saving }: {
  initial?: MTEngine
  keys: KeyOption[]
  catalog: { engine_type: string; name: string; needs_token: boolean; params: import('../../api').AIModelParam[] }[]
  /** A deployment engine submits `enabled` alone, hence Partial. */
  onSubmit: (input: Partial<MTEngineInput>) => void
  onCancel: () => void
  saving: boolean
}) {
  const { register, control, handleSubmit, setValue, formState: { errors, isDirty } } = useForm<EngineFormValues>({
    resolver: zodResolver(engineSchema),
    defaultValues: {
      name: initial?.name ?? '',
      engine_type: initial?.engine_type ?? catalog[0]?.engine_type ?? '',
      token_id: initial?.token_id ?? null,
      settings: initial?.settings ?? {},
      enabled: initial?.enabled ?? true,
    },
  })
  const engineType = useWatch({ control, name: 'engine_type' })
  const entry = catalog.find(c => c.engine_type === engineType)
  const params = entry?.params ?? []
  const keysForType = keys.filter(k => k.engineType === engineType)
  // A type this deployment supplies needs no key, so asking for one would be a
  // control with exactly one right answer: none.
  const needsKey = entry?.needs_token ?? true
  // An engine whose type this build cannot construct by hand — the deployment's
  // LibreTranslate or mock — has no catalog entry, so the Provider picker would
  // show it as blank and retyping it would be a trap (it has no key, and the
  // endpoint it needs is not a field here). Everything else about it is
  // editable, including its name, its switch and its deletion.
  const uncataloged = !!engineType && !catalog.some(c => c.engine_type === engineType)

  return (
    <Dialog open onOpenChange={o => !o && onCancel()} keepOpen={saving}>
      <DialogContent className="sm:max-w-2xl" data-testid="mt-engine-form">
        <DialogHeader>
          <DialogTitle>{initial ? 'Edit MT engine' : 'New MT engine'}</DialogTitle>
        </DialogHeader>
        <form className="space-y-4" onSubmit={handleSubmit(d => onSubmit({
          name: d.name.trim(),
          engine_type: d.engine_type,
          token_id: d.token_id,
          settings: d.settings as MTEngineSettings,
          enabled: d.enabled,
        }))}>
      <FormField label="Name" error={errors.name?.message} required>
        <Input {...register('name')} placeholder="e.g. DeepL formal" data-testid="mt-engine-name" />
      </FormField>

      {/* Side by side, each filling its half. The pickers need `w-full`
          explicitly — a Combobox sizes to its content otherwise. */}
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField label="Provider">
          <Controller control={control} name="engine_type" render={({ field }) => (
            <Combobox
              value={field.value}
              onChange={v => {
                field.onChange(v)
                // The key and the options both belong to the OLD provider —
                // keeping them would submit a Google key on a DeepL engine, or
                // an option that provider has never heard of.
                setValue('token_id', null)
                setValue('settings', {})
              }}
              options={uncataloged
                ? [{ value: engineType, label: engineType }]
                : catalog.map(c => ({ value: c.engine_type, label: c.name }))}
              placeholder="Choose a provider"
              disabled={uncataloged}
              className="w-full"
              data-testid="mt-engine-provider"
            />
          )} />
        </FormField>
        <FormField label="Key">
          <Controller control={control} name="token_id" render={({ field }) => (
            <Combobox
              value={field.value != null ? String(field.value) : ''}
              onChange={v => field.onChange(v ? Number(v) : null)}
              options={keysForType.map(k => ({ value: String(k.id), label: k.name }))}
              placeholder={needsKey ? 'Choose a key' : 'No key required'}
              disabled={!needsKey}
              className="w-full"
              data-testid="mt-engine-key"
            />
          )} />
          {/* An empty picker otherwise reads as a broken control rather than
              "you have not added a key for this provider". */}
          {needsKey && keysForType.length === 0 && (
            <p className="mt-1 text-xs text-muted-foreground">No key for this provider yet — add one in AI settings.</p>
          )}
        </FormField>
      </div>

      {params.length > 0 && (
        <Controller control={control} name="settings" render={({ field }) => (
          <ModelSettingsPanel
            // Re-key on the provider so switching one swaps the whole panel
            // rather than reconciling controls that no longer exist.
            key={engineType}
            params={params}
            value={(field.value ?? {}) as MTEngineSettings}
            onChange={field.onChange}
            title={`${catalog.find(c => c.engine_type === engineType)?.name ?? ''} options`}
            columns={2}
          />
        )} />
      )}
      {/* Last, because it is an operational state rather than part of the
          engine's configuration: a switched-off engine keeps everything above
          and simply stops being called. */}
      <Controller control={control} name="enabled" render={({ field }) => (
        <label className="flex cursor-pointer items-center gap-3 rounded-md -mx-2 px-2 py-1.5 transition-colors hover:bg-accent">
          <Switch
            checked={field.value}
            onCheckedChange={field.onChange}
            aria-label={field.value ? 'Disable this engine' : 'Enable this engine'}
            data-testid="mt-engine-enabled"
          />
          <span className="min-w-0">
            <span className="block text-sm font-medium text-gray-900 dark:text-gray-100">Enabled</span>
            <span className="block text-xs text-muted-foreground">
              A switched-off engine stops translating but stays in its routers.
            </span>
          </span>
        </label>
      )} />

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel} disabled={saving}>Cancel</Button>
            {/* Update stays disabled until something changed; Create is always
                live. Same rule as the LLM engine dialog. */}
            <Button type="submit" disabled={saving || (!!initial && !isDirty)} data-testid="mt-engine-submit">
              {saving ? 'Saving…' : initial ? 'Update' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
