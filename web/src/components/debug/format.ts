import type { ChipColor } from '@/components/ui/chip'
import { KindExtension } from '@/types/llmdebug'

// Display helpers for the /debug/llm feed. Pure functions, unit-tested in
// format.test.ts.

/**
 * Pretty-print a JSON string for reading; return the input untouched when it
 * isn't JSON. A `\n` inside a string value shows as a real line break, so a
 * message carried as one string reads line by line. Display only: the result
 * is not valid JSON, so copying uses the raw body.
 */
export function tryPretty(s: string): string {
  if (!s) return s
  try {
    return showLineBreaks(JSON.stringify(JSON.parse(s), null, 2))
  } catch {
    return s
  }
}

// An escaped \n preceded by an even number of backslashes (an escaped backslash
// followed by a literal n is not a line break).
const ESCAPED_LINE_BREAK = /(?<!\\)((?:\\\\)*)\\n/g

function showLineBreaks(json: string): string {
  return json.replace(ESCAPED_LINE_BREAK, '$1\n')
}

/** "618ms" under a second, "2.5s" from 1000ms up. */
export function formatDuration(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`
  return `${ms}ms`
}

/** Human label per Entry.kind (falls back to the raw kind). */
export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind
}

const KIND_LABELS: Record<string, string> = {
  mt: 'MT',
  qe: 'QE',
  ape: 'APE',
  [KindExtension]: 'Extension',
}

/** Chip color per Entry.kind. */
export function kindColor(kind: string): ChipColor {
  return KIND_COLORS[kind] ?? 'grey'
}

// One colour per request kind so the feed can be scanned by hue.
const KIND_COLORS: Record<string, ChipColor> = {
  mt: 'blue',
  qe: 'yellow',
  ape: 'green',
  [KindExtension]: 'grey',
}

/** Filter chips shown on the page. */
export const KIND_FILTERS = [
  { value: 'all', label: 'All' },
  { value: 'mt', label: 'MT' },
  { value: 'qe', label: 'QE' },
  { value: 'ape', label: 'APE' },
  { value: KindExtension, label: 'Extension' },
] as const

export type KindFilter = (typeof KIND_FILTERS)[number]['value']

export function matchesKindFilter(kind: string, filter: KindFilter): boolean {
  if (filter === 'all') return true
  return kind === filter
}
