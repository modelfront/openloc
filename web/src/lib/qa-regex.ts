import type { RegexRuleConfig } from '../types/qa'

export type RegexMode =
  | 'target_must_not_match'
  | 'target_must_match'
  | 'source_match_target_not'
  | 'source_must_not_match'

export const REGEX_MODES: { value: RegexMode; label: string; description: string }[] = [
  { value: 'target_must_not_match', label: 'Target must not match', description: 'Flag the segment when the target contains the pattern.' },
  { value: 'target_must_match', label: 'Target must match', description: 'Flag the segment when the target does not contain the pattern.' },
  { value: 'source_match_target_not', label: 'Source matches, target does not', description: 'Flag the segment when the source contains the source pattern and the target does not contain the target pattern.' },
  { value: 'source_must_not_match', label: 'Source must not match', description: 'Flag the segment when the source contains the pattern.' },
]

export const regexModeLabel = (mode: string) => REGEX_MODES.find(m => m.value === mode)?.label ?? mode

/** The special spaces a pattern often needs and a keyboard rarely types. */
export const SPECIAL_SPACES = [
  { char: '\u00a0', label: 'NBSP', name: 'Non-breaking space' },
  { char: '\u202f', label: 'NNBSP', name: 'Narrow non-breaking space' },
] as const

export type TextPart = { text: string } | { special: (typeof SPECIAL_SPACES)[number] }

/** Splits text into plain runs and special spaces, so the spaces can be shown. */
export function splitSpecialSpaces(text: string): TextPart[] {
  const parts: TextPart[] = []
  let run = ''
  for (const ch of text) {
    const special = SPECIAL_SPACES.find(s => s.char === ch)
    if (!special) {
      run += ch
      continue
    }
    if (run) parts.push({ text: run })
    run = ''
    parts.push({ special })
  }
  if (run) parts.push({ text: run })
  return parts
}

export const hasSpecialSpace = (text: string) => SPECIAL_SPACES.some(s => text.includes(s.char))

/** Inserts `insert` over the selection [start, end) of `value`. */
export function insertAt(value: string, start: number, end: number, insert: string): { value: string; caret: number } {
  return { value: value.slice(0, start) + insert + value.slice(end), caret: start + insert.length }
}

export function parseRegexConfig(raw: unknown): RegexRuleConfig {
  const c = (raw && typeof raw === 'object' ? raw : {}) as Partial<RegexRuleConfig>
  return {
    name: c.name ?? '',
    mode: c.mode ?? 'target_must_not_match',
    pattern: c.pattern ?? '',
    source_pattern: c.source_pattern ?? '',
    message: c.message ?? '',
    target_languages: c.target_languages ?? [],
    ignore_case: c.ignore_case ?? false,
  }
}
