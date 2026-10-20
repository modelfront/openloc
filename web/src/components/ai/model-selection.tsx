import { useState } from 'react'
import { Controller, type Control, type FieldPath, type FieldValues } from 'react-hook-form'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import type { AIVendorStatus, AIModelParam, ModelSettings } from '../../api'
import { Combobox, type ComboboxOption } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { ProviderIcon } from './ProviderIcon'
import { ModelSettingsPanel } from './model-preset-ui'
import { vendorLabel } from './vendor-catalog'

/** The provider that owns a token, or undefined for 0 / an unknown id. */
export function vendorForToken(vendors: AIVendorStatus[], tokenID?: number | null) {
  if (!tokenID) return undefined
  return vendors.find(v => (v.tokens ?? []).some(t => t.id === tokenID))
}

/** An installed extension: no models, no parameters, no prompt. */
export const isExtensionVendor = (v?: AIVendorStatus) => !!v?.extension

/** The model an engine row sends for a vendor's token: an extension's id, or
 *  '' for a provider whose model the user picks. */
export const fixedModelFor = (v?: AIVendorStatus) => v?.extension?.id ?? ''

/** Flat "Provider · Token" options across every connected provider. */
export function tokenOptionsFor(vendors: AIVendorStatus[], filter?: (v: AIVendorStatus) => boolean): ComboboxOption[] {
  return vendors
    .filter(v => filter?.(v) ?? true)
    .flatMap(v =>
      (v.tokens ?? []).map(t => ({
        value: String(t.id),
        label: `${vendorLabel(v)} · ${t.name}`,
        icon: <ProviderIcon provider={v.provider} className="size-4 text-[10px]" />,
      })),
    )
}

export function modelOptionsFor(vendor?: AIVendorStatus): ComboboxOption[] {
  return (vendor?.models ?? []).map(m => ({ value: m.id, label: m.label }))
}

/**
 * What the model dropdown says when it has nothing to offer. A vendor call that
 * failed explains itself ("The API key was rejected."); anything else is the
 * ordinary "this key reaches no models" case.
 */
export const modelsEmptyText = (vendor?: AIVendorStatus): string =>
  vendor?.models_error || 'No models available for this key'

export const paramsForModel = (vendor: AIVendorStatus | undefined, model: string): AIModelParam[] =>
  (vendor?.models ?? []).find(m => m.id === model)?.params ?? []

/**
 * Drop settings the newly-picked model doesn't accept. Without this a
 * Haiku→Opus 5 switch keeps `temperature` in the blob and the save is rejected
 * for a field the form no longer shows.
 */
export function pruneSettings(current: ModelSettings, params: AIModelParam[]): ModelSettings {
  const allowed = new Set(params.map(p => p.key))
  return Object.fromEntries(Object.entries(current).filter(([k]) => allowed.has(k)))
}

/**
 * The Model + Token pair and the model's settings panel — the {token, model,
 * settings} triple a provider call needs. Field paths are
 * passed in so it can sit at the form root (`model`) or inside a row
 * (`rows.0.model`).
 *
 * An extension has no Model field and no settings panel: its model is its id,
 * set when its token is picked.
 */
export function ModelSelectionFields<T extends FieldValues>({
  control, vendors, tokenID, model, settings, setValue,
  tokenPath, modelPath, settingsPath, tokenError, modelError,
  tokenFilter, onTokenChange, testIdPrefix, inlineSettings = true,
}: {
  control: Control<T>
  vendors: AIVendorStatus[]
  tokenID?: number | null
  model: string
  settings: ModelSettings
  setValue: (path: FieldPath<T>, value: never) => void
  tokenPath: FieldPath<T>
  modelPath: FieldPath<T>
  settingsPath: FieldPath<T>
  tokenError?: string
  modelError?: string
  /** Restrict which providers' tokens are offered (e.g. no extension for MT). */
  tokenFilter?: (v: AIVendorStatus) => boolean
  /** Extra side effects when the token changes (the provider may flip to an extension). */
  onTokenChange?: (tokenID: number, vendor?: AIVendorStatus) => void
  testIdPrefix?: string
  /** Off when the caller places `ModelSettingsDisclosure` itself (e.g. in a row
   *  of toggles further down the form). */
  inlineSettings?: boolean
}) {
  const vendor = vendorForToken(vendors, tokenID)
  const isExt = isExtensionVendor(vendor)
  const set = (path: FieldPath<T>, value: unknown) => setValue(path, value as never)
  const inlineParams = isExt ? [] : paramsForModel(vendor, model)
  // Same rule as the standalone toggle: collapsed unless overrides already exist.
  const [inlineOpen, setInlineOpen] = useState(() => Object.keys(settings ?? {}).length > 0)

  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <FormField label="Provider" error={tokenError} required>
          <Controller control={control} name={tokenPath} render={({ field }) => (
            <Combobox
              value={field.value ? String(field.value) : ''}
              onChange={v => {
                const id = v ? Number(v) : 0
                field.onChange(id)
                const nextVendor = vendorForToken(vendors, id)
                // The new provider has its own catalog, so the model can't carry
                // over. An extension's is fixed; anything else starts empty.
                set(modelPath, fixedModelFor(nextVendor))
                set(settingsPath, {})
                onTokenChange?.(id, nextVendor)
              }}
              options={tokenOptionsFor(vendors, tokenFilter)}
              placeholder="Select provider…"
              emptyText="Add a key under AI settings first"
              className="w-full"
              data-testid={testIdPrefix && `${testIdPrefix}-token`}
            />
          )} />
        </FormField>
        {!isExt && (
          <FormField label="Model" error={modelError} required>
            <Controller control={control} name={modelPath} render={({ field }) => (
              <Combobox
                value={(field.value as string) ?? ''}
                onChange={v => {
                  field.onChange(v)
                  set(settingsPath, pruneSettings(settings ?? {}, paramsForModel(vendor, v)))
                }}
                options={modelOptionsFor(vendor)}
                placeholder="Select…"
                emptyText={modelsEmptyText(vendor)}
                className="w-full"
                data-testid={testIdPrefix && `${testIdPrefix}-model`}
              />
            )} />
          </FormField>
        )}
      </div>
      {inlineSettings && inlineParams.length > 0 && (
        <>
          <ModelSettingsToggle
            open={inlineOpen}
            onToggle={() => setInlineOpen(v => !v)}
            overrideCount={Object.keys(settings ?? {}).length}
            testId={testIdPrefix && `${testIdPrefix}-settings-toggle`}
          />
          {inlineOpen && <ModelSettingsBody control={control} settingsPath={settingsPath} params={inlineParams} />}
        </>
      )}
    </>
  )
}

/**
 * Parameters the chosen model accepts — empty for an extension (no LLM knobs) and
 * before a model is picked. Callers use it to decide whether a "Model settings"
 * affordance should exist at all.
 */
export function modelSettingsParams(vendors: AIVendorStatus[], tokenID: number | null | undefined, model: string) {
  const vendor = vendorForToken(vendors, tokenID)
  return isExtensionVendor(vendor) ? [] : paramsForModel(vendor, model)
}

/**
 * The "Model settings" toggle, styled like the AI engine form's Advanced /
 * Prompt-preview buttons so it can sit on the same line.
 *
 * Open state is the caller's — the toggle and the panel are separate elements
 * (the toggle lives in a flex row of buttons, the panel below it), so a
 * self-contained disclosure component can't span both.
 */
export function ModelSettingsToggle({
  open, onToggle, overrideCount, testId,
}: {
  open: boolean
  onToggle: () => void
  overrideCount: number
  testId?: string
}) {
  return (
    <span className="flex items-center gap-2">
      <Button
        type="button"
        variant="ghost"
        size="xs"
        className="text-muted-foreground"
        aria-expanded={open}
        onClick={onToggle}
        data-testid={testId}
      >
        <ChevronRight className={cn('transition-transform', open && 'rotate-90')} />
        Model settings
      </Button>
      {/* Collapsing must not hide that overrides exist. */}
      {!open && overrideCount > 0 && (
        <span className="text-xs text-muted-foreground">{overrideCount} set</span>
      )}
    </span>
  )
}

/** The parameter panel itself, headerless (the toggle already labels it). */
export function ModelSettingsBody<T extends FieldValues>({
  control, settingsPath, params,
}: {
  control: Control<T>
  settingsPath: FieldPath<T>
  params: AIModelParam[]
}) {
  return (
    <Controller control={control} name={settingsPath} render={({ field }) => (
      <ModelSettingsPanel
        params={params}
        value={(field.value as ModelSettings) ?? {}}
        onChange={field.onChange}
        showHeader={false}
      />
    )} />
  )
}
