import type { SegmentationMode, SegmentationRules, SegmentationPreviewDecision, SegmentationRule } from '../api'

export const MODE_LABELS: Record<SegmentationMode, string> = {
  sentence: 'Sentence',
  paragraph: 'Paragraph',
}

export const MODE_OPTIONS = [
  { value: 'sentence' as const, label: 'Sentence', caption: 'Split blocks into sentences' },
  { value: 'paragraph' as const, label: 'Paragraph', caption: 'Each block is one segment' },
]

/** The longest sample text a preview takes; equal to segmentation.MaxPreviewText (pinned by a Go test). */
export const MAX_PREVIEW_TEXT = 20000

export const BREAK_OPTIONS = [
  { value: 'break' as const, label: 'Break' },
  { value: 'no-break' as const, label: 'No break' },
]

export function modeLabel(mode: string): string {
  return MODE_LABELS[mode as SegmentationMode] ?? mode
}

/** A profile's name as lists and pickers show it: a built-in says so. */
export function profileLabel(p: { name: string; builtin: boolean }): string {
  return p.builtin ? `${p.name} (built-in)` : p.name
}

export function emptyRules(): SegmentationRules {
  return { mode: 'sentence', rules: [], abbreviations: [], number_abbreviations: [] }
}

/** The 0-based rule a server validation message names ("Rule 3: …"), or null. */
export function ruleIndexOf(message: string | undefined): number | null {
  const m = message?.match(/^Rule (\d+)[: ]/)
  return m ? Number(m[1]) - 1 : null
}

/**
 * A rule's validation message as shown under that rule, without its number:
 * "Rule 3: the before pattern …" reads "The before pattern …", and
 * "Rule 3 needs …" reads "Rule needs …".
 */
export function ruleErrorText(message: string): string {
  const named = message.match(/^Rule \d+: (.)(.*)$/s)
  if (named) return named[1].toUpperCase() + named[2]
  return message.replace(/^Rule \d+ /, 'Rule ')
}

/** What decided a position in the preview, for its marker's tooltip. */
export function decisionLabel(d: SegmentationPreviewDecision, rules: SegmentationRule[]): string {
  const verdict = d.break ? 'Break' : 'No break'
  if (d.source === 'abbreviation') return `${verdict}: "${d.word}" is in the abbreviation list.`
  if (d.source === 'number_abbreviation') return `${verdict}: "${d.word}" is in the number abbreviation list and a number follows.`
  const n = d.rule ?? 0
  const rule = rules[n - 1]
  if (!rule) return `${verdict}: rule ${n}.`
  return `${verdict}: rule ${n} (before ${rule.before || 'anything'}, after ${rule.after || 'anything'}).`
}
