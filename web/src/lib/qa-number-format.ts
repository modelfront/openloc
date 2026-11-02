import type { NumberFormatConfig as StoredNumberFormatConfig, NumberFormatLanguage } from '../types/qa'
import { parseQARuleConfig } from './qa-rule-config'

export type { NumberFormatLanguage }

export type NumberFormatMode = 'allow' | 'require'

/** The number_format rule's config as the page edits it: a known mode, languages always present. */
export type NumberFormatConfig = Required<StoredNumberFormatConfig> & { mode: NumberFormatMode }

/**
 * Every separator pair the API accepts, named by an example. The order puts
 * the common formats first.
 */
export const NUMBER_FORMATS = [
  { decimal: '.', group: ',', label: '1,234.56' },
  { decimal: ',', group: '.', label: '1.234,56' },
  { decimal: ',', group: ' ', label: '1 234,56' },
  { decimal: '.', group: ' ', label: '1 234.56' },
  { decimal: '.', group: "'", label: "1'234.56" },
  { decimal: ',', group: "'", label: "1'234,56" },
  { decimal: '.', group: '', label: '1234.56' },
  { decimal: ',', group: '', label: '1234,56' },
] as const

/** The Combobox value of a separator pair. */
export function numberFormatKey(f: { decimal: string; group: string }): string {
  return JSON.stringify([f.decimal, f.group])
}

export function numberFormatFromKey(key: string): { decimal: string; group: string } {
  const f = NUMBER_FORMATS.find(f => numberFormatKey(f) === key) ?? NUMBER_FORMATS[0]
  return { decimal: f.decimal, group: f.group }
}

/** Reads a number_format rule's stored config; anything missing is the default. */
export function parseNumberFormatConfig(raw: unknown): NumberFormatConfig {
  const c = parseQARuleConfig(raw)
  const languages = Array.isArray(c.languages)
    ? (c.languages as unknown[]).flatMap(l => {
        const o = l as Partial<NumberFormatLanguage> | null
        return o && typeof o.language === 'string' && typeof o.decimal === 'string' && typeof o.group === 'string'
          ? [{ language: o.language, decimal: o.decimal, group: o.group }]
          : []
      })
    : []
  return { mode: c.mode === 'require' ? 'require' : 'allow', languages }
}
