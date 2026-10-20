// The provider / model / mode summary of an AI engine, shared by every place
// an engine is named or picked: the AI page's engine list, the MT engine
// pickers (default engine + per-language rows), and the step-definition
// engine picker. One derivation, one look.
import { useMemo, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listAIPrompts, listAIVendors, type AIEngine, type AiPrompt, type AIVendorStatus } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import {
  EngineDisabledChip, EngineNoKeyChip, RouterEngineNoKeyChip, RouterEngineOffChip,
} from '@/components/ui/chip-presets'
import { extensionMode, flagsToMode, MODE_LABELS } from './output-mode'
import { vendorLabel } from './vendor-catalog'
import { ProviderModelChip } from './ProviderModelChip'
import type { UnusableReason } from '../../api'

/**
 * The warning an engine wears when it cannot translate — nothing when it can.
 *
 * One component for every surface (MT engines, LLM engines, AI engines, and
 * both router rule lists) so the same breakage never gets described two
 * different ways. The reason comes from the server, which is also what the
 * pipeline fails the job on, so the warning and the failure cannot disagree.
 */
export function EngineUnusableChip({ reason }: { reason: UnusableReason | undefined }) {
  if (reason === 'disabled') return <EngineDisabledChip />
  if (reason === 'no_key') return <EngineNoKeyChip />
  return null
}

/**
 * The same warning worn by a ROUTER that routes to a broken engine.
 *
 * Separate wording, same hues: on a router row the subject is the engine it
 * points at, so "No key" alone would name the wrong thing.
 */
export function RouterEngineChip({ reason }: { reason: UnusableReason | undefined }) {
  if (reason === 'disabled') return <RouterEngineOffChip />
  if (reason === 'no_key') return <RouterEngineNoKeyChip />
  return null
}

export interface AIEngineChip {
  key: string
  provider?: string | null
  /** The provider's display name, when the catalog doesn't know it. */
  name?: string | null
  model?: string | null
  mode: string
}

/** Distinct provider/model/mode chips across an engine's rows. An extension
 *  row (no prompt) names the extension on its own, with the mode its
 *  capabilities give it; its model is just the extension id. An LLM row shows
 *  its token's provider, its model, and its prompt's output mode. Deduped by
 *  the full key; rows whose prompt is unknown are skipped. */
export function aiEngineChips(
  engine: Pick<AIEngine, 'rows'>,
  promptById: Map<number, AiPrompt>,
  vendorByToken: Map<number, AIVendorStatus>,
): AIEngineChip[] {
  const seen = new Set<string>()
  const out: AIEngineChip[] = []
  for (const r of engine.rows ?? []) {
    let chip: Omit<AIEngineChip, 'key'> | null
    if (r.system_prompt_id == null) {
      const vendor = r.token_id != null ? vendorByToken.get(r.token_id) : undefined
      chip = {
        provider: vendor?.provider ?? (r.model ? `ext:${r.model}` : null),
        name: vendor ? vendorLabel(vendor) : r.model,
        model: null,
        mode: MODE_LABELS[extensionMode(vendor?.extension?.capabilities ?? [])],
      }
    } else {
      const p = promptById.get(r.system_prompt_id)
      const provider = r.token_id != null ? vendorByToken.get(r.token_id)?.provider : undefined
      chip = p ? { provider, model: r.model, mode: MODE_LABELS[flagsToMode(p)] } : null
    }
    if (!chip) continue
    const key = `${chip.provider ?? ''}|${chip.model ?? ''}|${chip.mode}`
    if (!seen.has(key)) {
      seen.add(key)
      out.push({ key, ...chip })
    }
  }
  return out
}

/** Loads the prompt + vendor catalogs the chips need (both React Query-cached
 *  and manager-readable) and returns the lookups plus a render helper. */
export function useAIEngineChips() {
  const { data: prompts } = useQuery({ queryKey: queryKeys.aiPrompts.all, queryFn: listAIPrompts })
  const { data: vendors } = useQuery({ queryKey: queryKeys.aiVendors.all, queryFn: listAIVendors })
  const promptById = useMemo(() => new Map((prompts ?? []).map(p => [p.id, p])), [prompts])
  const vendorByToken = useMemo(() => {
    const m = new Map<number, AIVendorStatus>()
    for (const v of vendors ?? []) for (const t of v.tokens ?? []) m.set(t.id, v)
    return m
  }, [vendors])
  const chipsFor = (engine: Pick<AIEngine, 'rows'>) => aiEngineChips(engine, promptById, vendorByToken)
  const metaFor = (engine: Pick<AIEngine, 'rows'>): ReactNode => <AIEngineChipList chips={chipsFor(engine)} />
  return { promptById, vendorByToken, chipsFor, metaFor }
}

/** Renders a chip list (as computed by aiEngineChips). */
export function AIEngineChipList({ chips, className }: { chips: AIEngineChip[]; className?: string }) {
  if (chips.length === 0) return null
  return (
    <span className={className ?? 'inline-flex flex-wrap items-center gap-1'} data-testid="ai-engine-chips">
      {chips.map(c => (
        <ProviderModelChip key={c.key} provider={c.provider} name={c.name} model={c.model} mode={c.mode} />
      ))}
    </span>
  )
}
