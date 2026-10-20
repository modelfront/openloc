import { useState, useMemo, useEffect, useRef, useCallback } from 'react'
import { useCollapsibleOpen } from '@/hooks/useCollapsibleOpen'
import { useForm, useFieldArray, useWatch, Controller, type Control, type FieldErrors, type UseFormSetValue } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import { ChevronRight, Plus } from 'lucide-react'
import { cn } from '@/lib/utils'
import {
  listAIEngines, createAIEngine, updateAIEngine, deleteAIEngine,
  listAIPrompts, listAIVendors,
  type AIEngine, type AIEngineInput, type AIPromptWithAdditions, type AIVendorStatus, type EnginePurpose, type ModelSettings,
} from '../../api'
import { flagsToMode, derivePurpose, extensionMode, MODE_LABELS, MODE_DESCRIPTIONS, type OutputFlags, orderPrompts } from './output-mode'
import { PromptSends } from './PromptSends'
import { OpenPromptLink } from './OpenPromptLink'
import { useEngineCreateActions } from '../../hooks/useEngineCreateActions'
import { ProviderModelChip } from './ProviderModelChip'
import { aiEngineChips, EngineUnusableChip } from './ai-engine-chips'
import { Chip } from '@/components/ui/chip'
import { reviewsTranslations, vendorGroupOf, vendorLabel } from './vendor-catalog'
import { queryKeys } from '../../lib/query-keys'
import {
  ModelSelectionFields, ModelSettingsToggle, ModelSettingsBody, modelSettingsParams,
  fixedModelFor,
} from './model-selection'
import { engineAutoName, uniqueName } from '../../lib/ai-auto-name'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { useAutoName } from '../../hooks/useAutoName'
import ConfirmDialog from '../ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { EmptyState } from '@/components/layout/EmptyState'
import { Button } from '@/components/ui/button'
import { Combobox, type ComboboxOption } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { ListSection, type SectionLayout } from '@/components/layout/SectionHeader'
import { SectionInfo } from '@/components/ui/section-info'
import { AddItemButton } from '@/components/list/AddItemButton'
import { listRowClass } from '@/components/list/list-row'
import { rowButtonProps } from '@/components/list/row-button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { OpenPopupBoundary } from '@/components/ui/layered-dismiss'
import { Input } from '@/components/ui/input'
import { EditableTitle } from '@/components/list/EditableTitle'
import { DeleteButton } from '@/components/ui/delete-button'
import { Switch } from '@/components/ui/switch'

const BAD_OUTPUT_OPTIONS = [
  { value: 'reject', label: 'Reject the segment' },
  { value: 'comment', label: 'Add output as a comment' },
  { value: 'ignore', label: 'Ignore (leave unchanged)' },
]

// An AI engine is exactly one row = a provider (+ prompt for LLMs; an
// extension reviews with its own predict and needs none). The LLM-conditional
// fields are validated at submit time (they depend on which providers are
// extensions), so the schema only requires a provider here.
const rowSchema = z.object({
  system_prompt_id: z.number().int(),
  token_id: z.number().int().positive('Select a key'),
  model: z.string(),
  settings: z.record(z.string(), z.union([z.number(), z.boolean(), z.string()])),
  bad_output_action: z.string(),
})

const engineSchema = z.object({
  name: z.string().min(1, 'Name is required'),
  rows: z.array(rowSchema).min(1, 'Add at least one row'),
  enabled: z.boolean(),
})

type EngineFormValues = z.infer<typeof engineSchema>

// New engines default to "Ignore" on bad output; the prompt/segment/provider
// defaults are filled from the built-in defaults once data loads (see EngineForm).
const emptyRow = {
  system_prompt_id: 0, bad_output_action: 'ignore',
  token_id: 0, model: '', settings: {} as ModelSettings,
}

// AI engines: a name + language-routing rows. Manager-visible; write
// actions gated to owner/admin via `canWrite`.
export function AIEnginesSection({ canWrite, purpose, title, layout = 'collapsible', createOnMount, onCreateClosed }: {
  canWrite: boolean
  /** Which engines this instance owns: MT engines page vs AI page. */
  purpose: EnginePurpose
  /**
   * Section heading — also the collapse toggle. Omitted, the section renders
   * headerless with the create button above the list, for a tab that names it.
   */
  title?: string
  /**
   * `collapsible` (default) is the toggling row the MT engines page stacks
   * several of; `static` is a plain title with the create button on its right,
   * for an AI page tab that holds this section alone.
   */
  layout?: SectionLayout
  /**
   * Open the create dialog as soon as the section mounts, for the /new route.
   * Something linking in from elsewhere (a router's engine picker) lands on
   * the form rather than on a list with a button to find.
   */
  createOnMount?: boolean
  /** Fires when that dialog closes, so the route can go back to the list. */
  onCreateClosed?: () => void
}) {
  const [showCreate, setShowCreate] = useState(!!createOnMount)
  const [editingId, setEditingId] = useState<number | null>(null)
  // The target outlives the confirm dialog's close animation — clearing it
  // on confirm would show 'Delete "undefined"?' mid-fade — so open is its own
  // flag and the target is simply replaced on the next delete.
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const askDelete = (target: { id: number; name: string }) => { setDeleteTarget(target); setConfirmOpen(true) }
  // Saved or cancelled, the dialog closes the same way — and on the /new route
  // that also has to put the URL back, or reopening it would be a no-op.
  const closeCreate = () => { setShowCreate(false); onCreateClosed?.() }

  const { data: allEngines, isLoading, isError, error, refetch } = useQuery({ queryKey: queryKeys.aiEngines.all, queryFn: listAIEngines })
  const engines = useMemo(
    () => (allEngines ?? []).filter(e => e.purpose === purpose),
    [allEngines, purpose],
  )
  // Prompts and vendors only label the engine rows, so a failed load of
  // either leaves the list up with bare names rather than failing the section.
  const { data: systemPrompts } = useQuery({ queryKey: queryKeys.aiPrompts.all, queryFn: listAIPrompts })
  const { data: vendors } = useQuery({ queryKey: queryKeys.aiVendors.all, queryFn: listAIVendors })
  const promptById = useMemo(() => new Map((systemPrompts ?? []).map(p => [p.id, p])), [systemPrompts])
  const vendorByToken = useMemo(() => {
    const m = new Map<number, AIVendorStatus>()
    for (const v of vendors ?? []) for (const t of v.tokens ?? []) m.set(t.id, v)
    return m
  }, [vendors])

  const createMut = useMutationWithToast({
    mutationFn: (input: AIEngineInput) => createAIEngine(input),
    successMessage: 'Engine created',
    errorMessage: 'Create failed',
    invalidateKeys: [queryKeys.aiEngines.all],
    onSuccess: () => closeCreate(),
  })

  const updateMut = useMutationWithToast({
    mutationFn: ({ id, input }: { id: number; input: AIEngineInput }) => updateAIEngine(id, input),
    successMessage: 'Engine updated',
    errorMessage: 'Update failed',
    invalidateKeys: [queryKeys.aiEngines.all],
    onSuccess: () => setEditingId(null),
  })

  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => deleteAIEngine(id),
    successMessage: 'Engine deleted',
    errorMessage: 'Delete failed',
    invalidateKeys: [queryKeys.aiEngines.all],
    onSuccess: () => setConfirmOpen(false),
  })

  const info = (
    <SectionInfo label="About AI engines">
      Configure an AI engine here, then attach it to a workflow step to have it perform
      tasks automatically — machine translation, quality estimation, or post-editing.
    </SectionInfo>
  )

  const isMT = purpose === 'mt'
  const isStatic = layout === 'static'
  const createLabel = isMT ? 'New LLM translation engine' : 'New AI engine'
  const editing = engines.find(e => e.id === editingId)
  const body = (
    <>
      {isLoading ? (
        <LoadingPlaceholder />
      ) : isError && !allEngines ? (
        <LoadFailed what={isMT ? 'LLM translation engines' : 'AI engines'} error={error} onRetry={() => refetch()} />
      ) : (
        <div className="space-y-2">
          {engines.map(e => (
            <EngineCard
              key={e.id}
              engine={e}
              canWrite={canWrite}
              promptById={promptById}
              vendorByToken={vendorByToken}
              onEdit={() => setEditingId(e.id)}
              onDelete={() => askDelete({ id: e.id, name: e.name })}
            />
          ))}
          {engines.length === 0 && (isStatic || !canWrite) && (
            <EmptyState message={isMT ? 'No LLM translation engines yet.' : 'No AI engines yet.'} />
          )}
          {!isStatic && canWrite && (
            <AddItemButton label={createLabel} onClick={() => setShowCreate(true)} testId="ai-engine-add" />
          )}
        </div>
      )}

      {/* Create and edit open the same dialog; mounted only while open so the
          form seeds from `initial` fresh each time. */}
      {showCreate && (
        <EngineForm
          existingNames={engines.map(e => e.name)}
          onSubmit={input => createMut.mutate(input)}
          onCancel={closeCreate}
          isPending={createMut.isPending}
          sectionPurpose={purpose}
        />
      )}
      {editing && (
        <EngineForm
          initial={editing}
          onSubmit={input => updateMut.mutate({ id: editing.id, input })}
          onCancel={() => setEditingId(null)}
          isPending={updateMut.isPending}
          sectionPurpose={purpose}
        />
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={open => !open && setConfirmOpen(false)}
        title={`Delete engine "${deleteTarget?.name}"?`}
        description="AI routers that use this engine must be removed first."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />
    </>
  )

  return (
    <ListSection
      layout={layout}
      title={title}
      info={info}
      defaultOpen
      action={canWrite && (
        <Button size="sm" onClick={() => { setEditingId(null); setShowCreate(true) }} data-testid="ai-engine-add">{createLabel}</Button>
      )}
      data-testid="ai-engines-section"
    >
      {body}
    </ListSection>
  )
}

function EngineCard({
  engine, canWrite, promptById, vendorByToken, onEdit, onDelete,
}: {
  engine: AIEngine
  canWrite: boolean
  promptById: Map<number, AIPromptWithAdditions>
  vendorByToken: Map<number, AIVendorStatus>
  onEdit: () => void
  onDelete: () => void
}) {
  const chips = useMemo(() => aiEngineChips(engine, promptById, vendorByToken), [engine, promptById, vendorByToken])
  return (
    <div
      className={cn(
        listRowClass,
        canWrite && 'cursor-pointer transition-colors hover:bg-muted',
      )}
      {...rowButtonProps(canWrite ? onEdit : undefined)}
      data-testid={`ai-engine-${engine.id}`}
    >
      <div className="flex items-center gap-2 flex-wrap min-w-0">
        <EditableTitle name={engine.name} canEdit={false} className="font-medium text-gray-900 dark:text-gray-100" />
        {chips.map(c => (
          <ProviderModelChip key={c.key} provider={c.provider} name={c.name} model={c.model} mode={c.mode} />
        ))}
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

// A read-only preview of one resolved prompt (name + full body), shown inside a
// row's expanded "full prompts" panel.
function PromptPreview({
  label, name, body, emptyHint, prompt, linkTestId,
}: {
  label: string
  name?: string
  body?: string
  emptyHint: string
  /** When set, the prompt's NAME becomes the link to its page. */
  prompt?: OutputFlags & { id: number }
  linkTestId?: string
}) {
  return (
    <div>
      <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
        {name && (
          <>
            {' · '}
            {/* The name IS the link — it is what you are looking at when you
                decide you want to go and change it. */}
            {prompt ? (
              <OpenPromptLink prompt={prompt} label={name} data-testid={linkTestId} />
            ) : (
              <span className="normal-case text-foreground/70">{name}</span>
            )}
          </>
        )}
      </div>
      {body ? (
        <pre className="whitespace-pre-wrap font-mono text-xs leading-relaxed text-muted-foreground/90">{body}</pre>
      ) : (
        <p className="text-xs italic text-muted-foreground">{emptyHint}</p>
      )}
    </div>
  )
}

// One engine row: the routing fields plus a collapsible "full prompts" preview
// that shows the resolved system prompt (with its appended output contract) and
// the per-segment prompt exactly as they'll be sent. Collapsed by default.
function EngineRow({
  index, control, errors, systemOptions, systemPrompts, vendors, extensionFor, tokenFilter, setValue,
  purpose,
}: {
  index: number
  control: Control<EngineFormValues>
  errors: FieldErrors<EngineFormValues>
  systemOptions: ComboboxOption[]
  systemPrompts?: AIPromptWithAdditions[]
  vendors: AIVendorStatus[]
  /** The extension vendor behind a token, or undefined for an LLM provider. */
  extensionFor: (tokenID?: number | null) => AIVendorStatus | undefined
  tokenFilter: (v: AIVendorStatus) => boolean
  setValue: UseFormSetValue<EngineFormValues>
  /** Which section's prompts this row picks from — decides where both links go. */
  purpose: EnginePurpose
}) {
  // Same pinned "New …" machinery every other picker uses: a new tab onto the
  // create form, and the list refreshed when the author comes back.
  const { prompt: newPromptAction } = useEngineCreateActions()
  const newPromptActions = useMemo(
    () => [{ ...newPromptAction(purpose === 'mt' ? 'mt' : 'qe_ape'), icon: <Plus /> }],
    [newPromptAction, purpose],
  )
  // Advanced and Prompt preview start closed and only one is open at a time:
  // opening one closes the other, so the dialog never stacks both panels.
  // Model settings is independent and opens itself when overrides exist.
  const [showFull, setShowFull] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const toggleAdvanced = () => { setShowAdvanced(v => !v); setShowFull(false) }
  const toggleFull = () => { setShowFull(v => !v); setShowAdvanced(false) }
  const systemId = useWatch({ control, name: `rows.${index}.system_prompt_id` })
  const tokenId = useWatch({ control, name: `rows.${index}.token_id` })
  const model = useWatch({ control, name: `rows.${index}.model` })
  const settings = useWatch({ control, name: `rows.${index}.settings` })
  const extVendor = extensionFor(tokenId)
  const isExt = !!extVendor
  const systemPrompt = systemPrompts?.find(p => p.id === systemId)
  const rowErr = errors.rows?.[index]
  const settingsParams = modelSettingsParams(vendors, tokenId, model ?? '')
  const overrideCount = Object.keys(settings ?? {}).length
  // Auto-open once when the row arrives carrying overrides, so editing an
  // existing engine never hides a value that is in effect. A manual toggle
  // afterwards wins.
  const { open: showModelSettings, toggle: toggleModelSettings } = useCollapsibleOpen(false, overrideCount > 0)
  // Keep the advanced knobs open whenever they carry a validation error, so a
  // required-field message is never hidden inside a collapsed section.
  const advancedOpen = showAdvanced || !!rowErr?.bad_output_action

  return (
    <div data-testid="ai-engine-row">
      <div className="space-y-3">
        <ModelSelectionFields
          control={control}
          vendors={vendors}
          tokenID={tokenId}
          model={model ?? ''}
          settings={settings ?? {}}
          setValue={setValue as never}
          tokenPath={`rows.${index}.token_id`}
          modelPath={`rows.${index}.model`}
          settingsPath={`rows.${index}.settings`}
          tokenError={rowErr?.token_id?.message}
          modelError={rowErr?.model?.message}
          tokenFilter={tokenFilter}
          onTokenChange={(_id, vendor) => {
            // An extension row carries no system prompt; clear it so the hidden
            // field can't fail validation.
            if (vendor?.extension) setValue(`rows.${index}.system_prompt_id`, 0)
          }}
          testIdPrefix={`ai-engine-row-${index}`}
          inlineSettings={false}
        />
        <div className="grid grid-cols-2 gap-3">
          {!isExt && (
            <FormField label="System prompt" error={rowErr?.system_prompt_id?.message} required>
              <Controller
                control={control}
                name={`rows.${index}.system_prompt_id`}
                render={({ field }) => (
                  <Combobox
                    value={field.value ? String(field.value) : ''}
                    onChange={v => field.onChange(v ? Number(v) : 0)}
                    options={systemOptions}
                    // Creating is pinned inside the picker, where someone
                    // already is when they find nothing that fits.
                    actions={newPromptActions}
                    placeholder="Select"
                    className="w-full"
                    data-testid={`ai-engine-row-${index}-system-prompt`}
                  />
                )}
              />
              {systemPrompt && (
                <OpenPromptLink
                  prompt={systemPrompt}
                  className="mt-1"
                  data-testid={`ai-engine-row-${index}-open-prompt`}
                />
              )}
            </FormField>
          )}
        </div>
        {/* Advanced (segment prompt + bad-output) and Prompt preview toggles share
            one line. Advanced auto-expands if either field errors so the
            required-field messages aren't hidden. */}
        <div className="flex items-center gap-2 border-t border-gray-200 dark:border-gray-700/60 pt-2.5">
          {extVendor ? (
            <span className="text-xs text-muted-foreground" data-testid={`ai-engine-row-${index}-extension-note`}>
              {vendorLabel(extVendor)} reviews translations itself — no prompt needed
            </span>
          ) : (
            <>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                className="text-muted-foreground"
                aria-expanded={advancedOpen}
                onClick={toggleAdvanced}
                data-testid="ai-engine-row-advanced-toggle"
              >
                <ChevronRight className={cn('transition-transform', advancedOpen && 'rotate-90')} />
                Advanced
              </Button>
              {settingsParams.length > 0 && (
                <ModelSettingsToggle
                  open={showModelSettings}
                  onToggle={toggleModelSettings}
                  overrideCount={overrideCount}
                  testId={`ai-engine-row-${index}-settings-toggle`}
                />
              )}
              <Button
                type="button"
                variant="ghost"
                size="xs"
                className="text-muted-foreground"
                aria-expanded={showFull}
                onClick={toggleFull}
                data-testid="ai-engine-row-toggle"
              >
                <ChevronRight className={cn('transition-transform', showFull && 'rotate-90')} />
                Prompt preview
              </Button>
            </>
          )}
        </div>
        {showModelSettings && !isExt && settingsParams.length > 0 && (
          <ModelSettingsBody
            control={control}
            settingsPath={`rows.${index}.settings`}
            params={settingsParams}
          />
        )}
        {advancedOpen && !isExt && (
          <div className="grid grid-cols-2 gap-3">
            <FormField label="On bad output" error={rowErr?.bad_output_action?.message} required>
              <Controller
                control={control}
                name={`rows.${index}.bad_output_action`}
                render={({ field }) => (
                  <Combobox
                    value={field.value ?? ''}
                    onChange={v => field.onChange(v)}
                    options={BAD_OUTPUT_OPTIONS}
                    placeholder="Select…"
                    className="w-full"
                  />
                )}
              />
            </FormField>
          </div>
        )}
        {showFull && !isExt && (
          <div
            className="rounded-md border border-dashed border-gray-300 dark:border-gray-700 bg-background/60 divide-y divide-dashed divide-gray-300 dark:divide-gray-700"
            data-testid="ai-engine-row-full"
          >
            <div className="p-3">
              <PromptPreview
                label="Prompt"
                name={systemPrompt?.name}
                body={systemPrompt?.prompt_text}
                emptyHint="Select a prompt to preview it here."
                prompt={systemPrompt}
                linkTestId={`ai-engine-row-${index}-preview-open-prompt`}
              />
            </div>
            {systemPrompt && (
              <div className="bg-muted/40 p-3">
                <PromptSends
                  mode={flagsToMode(systemPrompt)}
                  segmentContext={systemPrompt.segment_context}
                  model={model ?? undefined}
                />
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

const NEW_ENGINE_NAME = 'New AI engine'

// The create / edit dialog for one engine. Mounted only while open (the
// caller conditionally renders it), so the form seeds from `initial` at mount.
function EngineForm({
  initial, existingNames = [], onSubmit, onCancel, isPending, sectionPurpose,
}: {
  initial?: AIEngine
  /** Names already taken, so an autofilled one can avoid the UNIQUE constraint. */
  existingNames?: string[]
  onSubmit: (input: AIEngineInput) => void
  onCancel: () => void
  isPending: boolean
  /** Purpose of the list this form belongs to — see `purpose` below. */
  sectionPurpose: EnginePurpose
}) {
  const { data: systemPrompts } = useQuery({ queryKey: queryKeys.aiPrompts.all, queryFn: listAIPrompts })

  const { data: vendors } = useQuery({ queryKey: queryKeys.aiVendors.all, queryFn: listAIVendors })
  const vendorByToken = useMemo(() => {
    const m = new Map<number, AIVendorStatus>()
    for (const v of vendors ?? []) for (const t of v.tokens ?? []) m.set(t.id, v)
    return m
  }, [vendors])
  // Purpose is derived, not chosen: an extension ⇒ QE+APE; an LLM inherits its
  // prompt's mode (MT prompt ⇒ MT engine, else QE+APE). Immutable after
  // creation, so an existing engine keeps its stored purpose. A NEW one takes
  // the purpose of the list it is being created in rather than staying open
  // until a prompt is picked: every filter below reads this, so on the MT
  // engines page the form offers only MT prompts and no extension token —
  // which is what makes derivePurpose land on 'mt'. Left undefined, "New LLM
  // translation engine" could be saved as a QE+APE engine and vanish from the
  // list that created it.
  const purpose = initial?.purpose ?? sectionPurpose
  const createTitle = sectionPurpose === 'mt' ? 'New LLM translation engine' : 'New AI engine'

  // A row runs an extension (no prompt, no model choice) when its TOKEN
  // belongs to an extension vendor — the same rule the backend applies.
  const extensionFor = useCallback(
    (tokenID?: number | null) => {
      const v = tokenID ? vendorByToken.get(tokenID) : undefined
      return v?.extension ? v : undefined
    },
    [vendorByToken],
  )

  // Pickers inside the form report themselves here — see the close handler
  // below, which is what stops one backdrop press closing both.
  const openPopups = useRef(0)

  const { register, control, watch, setValue, setError, handleSubmit, formState: { errors, isDirty } } = useForm<EngineFormValues>({
    resolver: zodResolver(engineSchema),
    defaultValues: {
      name: initial?.name ?? NEW_ENGINE_NAME,
      enabled: initial?.enabled ?? true,
      // Exactly one row. Legacy multi-row engines collapse to their
      // first row here (they can't be edited back into existence).
      rows: initial && initial.rows.length > 0
        ? [{
            system_prompt_id: initial.rows[0].system_prompt_id ?? 0,
            bad_output_action: initial.rows[0].bad_output_action ?? '',
            token_id: initial.rows[0].token_id ?? 0,
            model: initial.rows[0].model ?? '',
            settings: initial.rows[0].settings ?? {},
          }]
        : [{ ...emptyRow }],
    },
  })
  const { fields } = useFieldArray({ control, name: 'rows' })
  // Which providers an AI engine can actually be built on.
  //
  // MT providers (DeepL, Google, LibreTranslate) are excluded for BOTH purposes:
  // they are translation endpoints, not models an engine drives — they can't
  // follow the QE+APE output contract and take no prompt. Their credential is
  // projected into an mt_engines row and shows in the
  // provider-engine list on the MT engines page; the backend rejects them as AI
  // engines.
  //
  // An extension is offered only to a QE+APE engine, and only when it can
  // review translations: it can't machine-translate.
  const tokenFilter = useCallback(
    (v: AIVendorStatus) => {
      if (vendorGroupOf(v.provider) === 'mt') return false
      if (v.extension) return purpose !== 'mt' && reviewsTranslations(v.extension)
      return true
    },
    [purpose],
  )

  // Prompt-mode ↔ purpose compatibility — an MT engine takes MT
  // prompts only; a QE+APE engine takes status/postedit prompts (qe, qe_ape,
  // ape) only. Mirrors the backend's promptMode check.
  const systemOptions = useMemo(
    // orderPrompts so a prompt sits where the list page puts it — your own
    // newest first, then the built-ins by mode.
    () => orderPrompts(systemPrompts ?? [])
      // Only mode-compatible prompts show, for a new engine and an existing one
      // alike — see `purpose` above for where a new one's comes from.
      .filter(p => (flagsToMode(p) === 'mt') === (purpose === 'mt'))
      // The mode rides along as a chip, matching the prompt cards. A QE+APE
      // engine can still take a qe, ape or qe_ape prompt, and those behave
      // differently at runtime, and a custom prompt's name alone does not say
      // which one it is.
      .map(p => {
        const mode = flagsToMode(p)
        return {
          value: String(p.id),
          label: p.name,
          chip: <Chip title={MODE_DESCRIPTIONS[mode]} className="shrink-0">{MODE_LABELS[mode]}</Chip>,
        }
      }),
    [systemPrompts, purpose],
  )

  // A NEW engine's purpose isn't stored yet — it follows the chosen engine
  // prompt (and an extension forces QE+APE). An existing engine keeps its
  // immutable stored purpose.
  const rowSystemPromptID = useWatch({ control, name: 'rows.0.system_prompt_id' })
  const rowTokenID = useWatch({ control, name: 'rows.0.token_id' })
  const rowModel = useWatch({ control, name: 'rows.0.model' })

  // Autofill the name from the chosen provider + what the engine does, and keep
  // following both while the name is still the one we generated. The mode comes
  // from the prompt (QE / QE+APE / APE / MT) rather than the coarse purpose, so
  // a QE engine doesn't get labelled QE+APE — and a prompt swap renames it.
  const rowPrompt = systemPrompts?.find(p => p.id === rowSystemPromptID)
  const rowExtension = extensionFor(rowTokenID)?.extension
  const modeLabel = rowExtension
    // An extension carries no prompt — its capabilities say what it does.
    ? MODE_LABELS[extensionMode(rowExtension.capabilities)]
    : rowPrompt ? MODE_LABELS[flagsToMode(rowPrompt)] : ''
  // The name follows the row's own provider + model.
  const rowVendor = rowTokenID ? vendorByToken.get(rowTokenID) : undefined
  const rowVendorLabel = rowVendor ? vendorLabel(rowVendor) : ''
  // An extension's model is its id — the display name already says it.
  const modelLabel = rowExtension
    ? ''
    : (rowVendor?.models ?? []).find(m => m.id === rowModel)?.label ?? rowModel ?? ''
  const suggestedName = uniqueName(
    engineAutoName([rowVendorLabel, modelLabel].filter(Boolean).join(' '), modeLabel),
    existingNames,
  )
  useAutoName({
    enabled: !initial,
    current: watch('name'),
    suggestion: suggestedName,
    placeholders: [NEW_ENGINE_NAME],
    onChange: name => setValue('name', name),
  })

  // Prefill a new engine with sensible defaults once the reference data loads:
  // the built-in system prompt for this list's purpose, and the first available
  // LLM provider. Runs once; edits keep their saved values.
  const inited = useRef(false)
  useEffect(() => {
    if (initial || inited.current || !systemPrompts || !vendors) return
    inited.current = true
    // The built-in for THIS list's purpose — 'mt' on the MT engines page,
    // 'qe_ape' on the AI page. Hardcoding qe_ape here would prefill an MT form
    // with a prompt its own picker doesn't offer, leaving the field blank and
    // the derived name reading QE+APE.
    const wantMode = purpose === 'mt' ? 'mt' : 'qe_ape'
    const builtin = systemPrompts.find(p => p.is_default && flagsToMode(p) === wantMode)
    if (builtin) setValue('rows.0.system_prompt_id', builtin.id)
    // `tokenFilter` first: only a provider the Token picker actually offers may
    // be prefilled. An MT provider (DeepL, Google) can't run an engine, so a
    // tenant whose only token is an MT one opens the form with nothing selected.
    // Then prefer an LLM token — an extension needs no prompt, so defaulting to
    // it would hide the prompt fields the user most likely wants.
    const eligible = (vendors ?? []).filter(v => tokenFilter(v) && (v.tokens ?? []).length > 0)
    const llm = eligible.find(v => !v.extension)
    const vendor = llm ?? eligible[0]
    const tok = vendor?.tokens?.[0]
    if (tok) {
      setValue('rows.0.token_id', tok.id)
      // Default the model too — a form that prefills a token but leaves "Select…"
      // on the model isn't actually ready to save.
      setValue('rows.0.model', llm ? llm.models?.[0]?.id ?? '' : fixedModelFor(vendor))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial, systemPrompts, vendors, setValue])

  const onFormSubmit = (data: EngineFormValues) => {
    const r = data.rows[0]
    const extVendor = extensionFor(r.token_id)
    // An extension takes no prompts. LLM providers need an engine prompt and a
    // bad-output action; validate them here since the schema can't (it doesn't
    // know which providers are extensions).
    if (!extVendor) {
      let bad = false
      if (r.system_prompt_id <= 0) { setError('rows.0.system_prompt_id', { message: 'Select a prompt' }); bad = true }
      if (!r.bad_output_action) { setError('rows.0.bad_output_action', { message: 'Select an action' }); bad = true }
      if (bad) return
    }
    // Derive the purpose: existing engines keep theirs (immutable); a
    // new extension engine is QE+APE; a new LLM engine inherits its prompt's
    // mode. See derivePurpose for the full rule + the BE mirror.
    const prompt = systemPrompts?.find(p => p.id === r.system_prompt_id)
    const derivedPurpose = derivePurpose(initial?.purpose, !!extVendor, prompt)
    onSubmit({
      name: data.name.trim(),
      purpose: derivedPurpose,
      enabled: data.enabled,
      // An engine applies to all language pairs (wildcard rows).
      rows: [extVendor
        ? {
            source_lang: '*', target_langs: ['*'],
            // The backend requires an extension row's model to be its id.
            token_id: r.token_id, model: fixedModelFor(extVendor), settings: {},
            system_prompt_id: null, bad_output_action: null,
          }
        : {
            source_lang: '*',
            target_langs: ['*'],
            token_id: r.token_id,
            model: r.model,
            settings: r.settings as ModelSettings,
            system_prompt_id: r.system_prompt_id,
            bad_output_action: r.bad_output_action as AIEngineInput['rows'][number]['bad_output_action'],
          }],
    })
  }

  return (
    <Dialog
      open
      keepOpen={isPending}
      onOpenChange={(open, details) => {
        if (open) {
          return
        }
        // A press on the backdrop belongs to the innermost open layer. The
        // pickers in this form portal their popups to the body, so that press
        // is "outside" for the picker AND for the form — taking both closes an
        // engine somebody was still configuring. The picker closes itself; we
        // stay. With nothing stacked on top the press is ours as usual, and
        // Esc and ✕ close unless a save is running.
        if (details.reason === 'outside-press' && openPopups.current > 0) {
          return
        }
        onCancel()
      }}
    >
      {/* The fields scroll, not the dialog. A centred dialog has no page
          scroll to fall back on, so a long form would push its own Create
          button off a laptop screen. min-h-0 on the flex children, or they
          refuse to shrink below their content and nothing scrolls at all. */}
      <DialogContent className="sm:max-w-2xl max-h-[85vh] flex flex-col" data-testid="ai-engine-form">
        <DialogHeader>
          <DialogTitle>{initial ? 'Edit AI engine' : createTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex min-h-0 flex-1 flex-col" onSubmit={handleSubmit(onFormSubmit)}>
          <OpenPopupBoundary countRef={openPopups}>
            <div className="-mx-4 min-h-0 flex-1 space-y-4 overflow-y-auto px-4">
      <FormField label="Name" error={errors.name?.message} required>
        <Input {...register('name')} placeholder="e.g. Production reviewer" data-testid="ai-engine-name" />
      </FormField>

      <div className="space-y-2">
        {fields.slice(0, 1).map((field, i) => (
          <EngineRow
            key={field.id}
            index={i}
            control={control}
            errors={errors}
            systemOptions={systemOptions}
            systemPrompts={systemPrompts}
            vendors={vendors ?? []}
            extensionFor={extensionFor}
            tokenFilter={tokenFilter}
            setValue={setValue}
            purpose={purpose}
          />
        ))}
        {typeof errors.rows?.message === 'string' && (
          <p className="text-xs text-destructive">{errors.rows.message}</p>
        )}
      </div>
            </div>
          </OpenPopupBoundary>
          {/* Last, because it is an operational state rather than part of the
              engine's configuration — and the same control, in the same place, as
              the MT engine dialog. */}
          <Controller control={control} name="enabled" render={({ field }) => (
            <label className="flex cursor-pointer items-center gap-3 rounded-md -mx-2 px-2 py-1.5 transition-colors hover:bg-accent">
              <Switch
                checked={field.value}
                onCheckedChange={field.onChange}
                aria-label={field.value ? 'Disable this engine' : 'Enable this engine'}
                data-testid="ai-engine-enabled"
              />
              <span className="min-w-0">
                <span className="block text-sm font-medium text-gray-900 dark:text-gray-100">Enabled</span>
                <span className="block text-xs text-muted-foreground">
                  A switched-off engine stops translating but stays in its routers.
                </span>
              </span>
            </label>
          )} />

          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={onCancel} disabled={isPending}>Cancel</Button>
            {/* An edit stays disabled until something changed; Create is always live. */}
            <Button type="submit" disabled={isPending || (!!initial && !isDirty)} data-testid="ai-engine-submit">
              {isPending ? 'Saving…' : initial ? 'Update' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
