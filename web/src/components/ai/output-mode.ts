// Shared output-mode logic for AI prompts. A prompt's "mode" is a
// single choice that maps to the underlying output-field flags the backend +
// engine validation use. QE judges (approved/score/comment);
// APE + QE+APE additionally return a postedit; MT returns only the target.
// Used by the prompt editor (AIPromptsSection) and the engine row's full-prompt
// preview (AIEnginesSection) so both render the identical appended contract.
import type { ComboboxOption } from '@/components/ui/combobox'

export const OUTPUT_MODES: ComboboxOption[] = [
  { value: 'qe', label: 'Quality estimation' },
  { value: 'qe_ape', label: 'Quality estimation and automatic post-editing' },
  { value: 'ape', label: 'Automatic post-editing' },
  { value: 'mt', label: 'Machine translation' },
]

export type OutputMode = 'qe' | 'qe_ape' | 'ape' | 'mt'
export type OutputFlags = { out_status: boolean; out_text: boolean; out_note: boolean; out_comments: boolean }

export const MODE_FLAGS: Record<OutputMode, OutputFlags> = {
  qe:     { out_status: true,  out_text: false, out_note: true,  out_comments: true },
  qe_ape: { out_status: true,  out_text: true,  out_note: true,  out_comments: true },
  ape:    { out_status: false, out_text: true,  out_note: true,  out_comments: true },
  mt:     { out_status: false, out_text: true,  out_note: true,  out_comments: false },
}

export const MODE_LABELS: Record<OutputMode, string> = { qe: 'QE', qe_ape: 'QE+APE', ape: 'APE', mt: 'MT' }

// Display order for the built-in default prompts (they sink to the bottom of
// the list; this orders them among themselves).
export const DEFAULT_MODE_ORDER: OutputMode[] = ['mt', 'qe_ape', 'qe', 'ape']

// The name a new prompt takes when its type is picked. Long enough to say what
// the prompt is for, short enough to read in the list and in an engine's prompt
// picker — QE+APE's card title is longer than a name wants to be, so this
// echoes the card rather than copying it.
export const MODE_DEFAULT_NAMES: Record<OutputMode, string> = {
  qe:     'Quality estimation',
  qe_ape: 'Quality estimation and post-editing',
  ape:    'Automatic post-editing',
  mt:     'Machine translation',
}

// defaultPromptName suffixes the type's name until it is free. Names are unique
// per tenant, so without this the second prompt of a type is created, rejected
// by the API and handed back to the user to rename.
export function defaultPromptName(mode: OutputMode, taken: string[]): string {
  const base = MODE_DEFAULT_NAMES[mode]
  if (!taken.includes(base)) return base
  for (let n = 2; ; n++) {
    const candidate = `${base} ${n}`
    if (!taken.includes(candidate)) return candidate
  }
}

export const MODE_DESCRIPTIONS: Record<OutputMode, string> = {
  qe:     'Judges each translation and approves or rejects it with a score.',
  qe_ape: 'Judges each translation and post-edits it when it needs fixing.',
  ape:    'Post-edits every translation, correcting defects in place.',
  mt:     'Translates the source segment from scratch.',
}

// Derive the mode from stored flags when editing. APE and MT both output text
// with no status; they're told apart by the comment flag (APE allows a comment,
// MT doesn't).
export function flagsToMode(f: OutputFlags): OutputMode {
  if (f.out_status && f.out_text) return 'qe_ape'
  if (f.out_status) return 'qe'
  if (f.out_comments) return 'ape'
  return 'mt'
}

/**
 * The order a human reads prompts in: your own newest first, then the
 * read-only built-ins by mode. Shared by the list page and every picker, so a
 * prompt sits in the same place wherever it is shown.
 *
 * Newest first because the prompt you just saved is the one you came back for.
 * The API sorts your own oldest-first, which puts a new one between the older
 * customs and the defaults — the least findable row in the list
 */
export function orderPrompts<T extends OutputFlags & { is_default: boolean; created_at: string }>(prompts: T[]): T[] {
  const customs = prompts.filter(p => !p.is_default)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
  const defaults = prompts.filter(p => p.is_default)
    .sort((a, b) => DEFAULT_MODE_ORDER.indexOf(flagsToMode(a)) - DEFAULT_MODE_ORDER.indexOf(flagsToMode(b)))
  return [...customs, ...defaults]
}

// Derive an AI engine's purpose. Purpose is never chosen directly —
// it follows from the row's provider + prompt:
//   • an existing engine keeps its stored purpose (immutable after creation);
//   • an extension row reviews with the extension's own predict, so it's
//     always QE+APE;
//   • an LLM row inherits its system prompt's mode — an MT prompt makes an MT
//     system, anything else (QE, APE, QE+APE) makes a QE+APE engine.
// Mirrors the server-side promptMode ↔ purpose check in
// internal/aiengine/handler.go (keep in sync). Kept as a pure helper so the
// FE↔BE contract is unit-testable without driving the whole form.
export function derivePurpose(
  existingPurpose: 'mt' | 'qe_ape' | undefined,
  isExtension: boolean,
  prompt: OutputFlags | undefined,
): 'mt' | 'qe_ape' {
  if (existingPurpose) return existingPurpose
  if (isExtension) return 'qe_ape'
  return prompt && flagsToMode(prompt) === 'mt' ? 'mt' : 'qe_ape'
}

// What an extension row does, from the extension's capabilities: it carries
// no prompt, so this stands in for the prompt's mode in chips and names.
export function extensionMode(capabilities: readonly string[]): OutputMode {
  const qe = capabilities.includes('qe')
  const ape = capabilities.includes('ape')
  if (ape && !qe) return 'ape'
  if (qe && !ape) return 'qe'
  return 'qe_ape'
}

// The output contract a mode appends to the system prompt is assembled by the
// server and fetched by PromptContract.tsx — see internal/ai/assemble.go. It is
// not rebuilt here: one wire format, one description of it.
