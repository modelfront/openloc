/**
 * Encode/decode language pair keys for use in UI state (tabs, filters).
 *
 * Language codes can contain hyphens (e.g., "en-us", "es-mx", "pt-br"),
 * so we use "|" as the separator instead of "-" to avoid ambiguity.
 */

const SEPARATOR = '|'

/** A language code's base language: `de-de` → `de`. */
export const baseLanguage = (code: string) => code.split('-')[0]

/** A language code in the server's stored form: lowercase, `-` between
 *  subtags (`pt_BR` → `pt-br`). */
export const normalizeLanguage = (code: string) => code.trim().toLowerCase().replaceAll('_', '-')

export function encodeLangPair(source: string, target: string): string {
  return `${source}${SEPARATOR}${target}`
}

export function decodeLangPair(key: string): { source: string; target: string } | null {
  if (key === 'all') return null
  const idx = key.indexOf(SEPARATOR)
  if (idx === -1) return null
  return { source: key.slice(0, idx), target: key.slice(idx + 1) }
}
