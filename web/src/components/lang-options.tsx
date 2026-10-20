import type { ComboboxOption } from '@/components/ui/combobox'

// Languages → Combobox options, shaped like every other option: the name as
// the label, the code as the chip on the right (and alone on a multi-select
// chip). The search matches both. The code is shown uppercase, like every
// language code in the app; the value stays the stored code.

export interface LangOption {
  code: string
  name: string
}

export function toLangOptions(languages: LangOption[]): ComboboxOption[] {
  return languages.map((l) => ({
    value: l.code,
    label: l.name,
    chip: l.code.toUpperCase(),
    keywords: l.code,
    shortLabel: l.code.toUpperCase(),
  }))
}

/**
 * Props every language picker shares — spread onto `Combobox` / `MultiCombobox`.
 * `data-testid="lang"` gives rows the `lang-option-${code}` ids the E2E helpers
 * look for.
 */
export const langPickerProps = {
  searchPlaceholder: 'Search languages...',
  emptyText: 'No matching languages',
  'data-testid': 'lang',
} as const
