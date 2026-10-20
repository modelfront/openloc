import { request } from './request'
import type { ModelSettings } from './ai'

// ─── AI engines ───
//
// An engine is a name plus language-routing rows. Each row maps a source
// language + target languages either to a system prompt (+ optional segment
// prompt + bad-output action) or to an installed extension's own review
// (system_prompt_id null). Each row carries its own {token_id, model, settings}.
// The rows drive
// AI-as-MT + QE+APE workflow-step execution.

export type BadOutputAction = 'reject' | 'comment' | 'ignore'

// An engine is used EITHER as an MT engine (settings MT assignment)
// OR as a QE+APE workflow-step engine. Chosen at creation, immutable after.
export type EnginePurpose = 'mt' | 'qe_ape'

export interface AIEngineRow {
  id?: number
  source_lang: string
  target_langs: string[]
  // null ⇒ an extension row: no system prompt, segment prompt, or bad-output
  // action. Derived from the token's vendor, not stored.
  system_prompt_id?: number | null
  bad_output_action?: BadOutputAction | null
  // The row's own vendor connection. `token_id` picks the vendor + API key,
  // `model` must be in that vendor's catalog (an extension's is its id), and
  // `settings` are that model's parameters (none for an extension).
  token_id?: number | null
  model?: string
  settings?: ModelSettings
}

export interface AIEngine {
  id: number
  tenant_id: number
  name: string
  purpose: EnginePurpose
  /** A switched-off engine keeps its rows and its place in every router. */
  enabled: boolean
  /** Why it cannot translate, or '' when it can. See `UnusableReason`. */
  unusable: UnusableReason
  created_at: string
  updated_at: string
  rows: AIEngineRow[]
}

/**
 * Why an engine cannot translate. Shared by MT and AI
 * engines, so a router flags either kind without caring which it is.
 *
 * '' = usable. 'disabled' = switched off. 'no_key' = the AI settings key it
 * spent was deleted; the engine kept everything else.
 */
export type UnusableReason = '' | 'disabled' | 'no_key'

export interface AIEngineInput {
  name: string
  purpose: EnginePurpose
  rows: AIEngineRow[]
  /** Absent keeps the current state, so a row edit cannot switch it back on. */
  enabled?: boolean
}

export const listAIEngines = () =>
  request<{ data: AIEngine[] }>('/ai/engines').then(r => r.data)

export const createAIEngine = (input: AIEngineInput) =>
  request<{ data: AIEngine }>('/ai/engines', {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data)

export const updateAIEngine = (id: number, input: AIEngineInput) =>
  request<{ data: AIEngine }>(`/ai/engines/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  }).then(r => r.data)

export const deleteAIEngine = (id: number) =>
  request<void>(`/ai/engines/${id}`, { method: 'DELETE' })
