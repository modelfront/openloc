import { request } from './request'
import type { AiPrompt } from '../types/generated'

// ─── System prompt templates ───
//
// A prompt is its instruction text plus the output fields the model is expected
// to emit (status / text / note / comments). Provider / model / settings live on
// model presets, not prompts.
export type { AiPrompt }

/**
 * Extra instruction text that applies only to jobs into some target languages —
 * "any job into German". At least one language must be set. Matching is by base
 * subtag, so `de` covers a de-AT job.
 */
export interface LanguageAddition {
  target_languages: string[]
  text: string
}

/** A prompt as the API returns it: the row plus its language additions. */
export interface AIPromptWithAdditions extends AiPrompt {
  /** Never null — an empty list is the common case. */
  language_additions: LanguageAddition[]
  /**
   * The OPTIONAL per-segment context this prompt sends. The source text, and on
   * a reviewing mode the translation, are implied by the output mode and are
   * not listed here.
   */
  segment_context: string[]
}

export interface AIPromptInput {
  name: string
  prompt_text: string
  out_status: boolean
  out_text: boolean
  out_note: boolean
  out_comments: boolean
  /** Optional per-segment context keys (see SEGMENT_CONTEXT_FIELDS). */
  segment_context: string[]
  /** Replace-all: whatever is sent becomes the prompt's whole set. */
  language_additions: LanguageAddition[]
}

export const listAIPrompts = () =>
  request<{ data: AIPromptWithAdditions[] }>('/ai/prompts').then(r => r.data)

export const createAIPrompt = (input: AIPromptInput) =>
  request<{ data: AIPromptWithAdditions }>('/ai/prompts', {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data)

export const updateAIPrompt = (id: number, input: AIPromptInput) =>
  request<{ data: AIPromptWithAdditions }>(`/ai/prompts/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  }).then(r => r.data)

export const deleteAIPrompt = (id: number) =>
  request<void>(`/ai/prompts/${id}`, { method: 'DELETE' })

// ─── Preview ───
//
// The server assembles the prompt the same way a run does and hands back both
// halves: `instruction` is the author's own text, `appended` is the language
// direction and output contract the platform adds. The editor renders `appended`
// read-only — it is not the author's to change, but it is theirs to see.
export interface AIPromptPreviewInput {
  /** Preview using the provider default when no model is supplied. */
  provider?: string
  prompt_text: string
  out_status: boolean
  out_text: boolean
  out_note: boolean
  out_comments: boolean
  /**
   * The per-segment context the draft has switched on. The contract names
   * exactly these fields, so the preview is wrong without it.
   */
  segment_context?: string[]
  /** Optional. Omitted, the preview shows `<source_lang>` / `<target_lang>`. */
  source_lang?: string
  target_lang?: string
  /**
   * Optional model id. A prompt is model-independent so the prompt editor has
   * none; an engine row does, and passing it answers whether that model has the
   * reply shape enforced by the provider.
   */
  model?: string
}

export interface AIPromptPreview {
  /** The batched user message as the runtime builds it: a flat array, each
   *  segment's id beside its context keys. No wrapper, no `content` nesting. */
  context_preview: Array<{ id: string } & Record<string, unknown>>
  output_format: string
  instruction: string
  appended: string
  system_prompt: string
  mode: string
  /** True unless the model is known not to take a schema; with no model in hand
   *  a schema is assumed, because the shape follows from the mode. */
  schema_enforced: boolean
  output_schema?: Record<string, unknown>
}

export const previewAIPrompt = (input: AIPromptPreviewInput) =>
  request<{ data: AIPromptPreview }>('/ai/prompts/preview', {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data)
