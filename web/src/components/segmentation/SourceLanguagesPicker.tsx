import { useMemo } from 'react'
import { MultiCombobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions, type LangOption } from '@/components/lang-options'

/** The option that stands for an empty list: the profile suits any language. */
export const ANY_LANGUAGE = 'any'

/**
 * The next language list after a change in the picker: picking Any language
 * clears the languages, and picking a language clears Any language. An empty
 * list means any language.
 */
export function nextSourceLanguages(shown: string[], picked: string[]): string[] {
  const anyJustPicked = picked.includes(ANY_LANGUAGE) && !shown.includes(ANY_LANGUAGE)
  return anyJustPicked ? [] : picked.filter(v => v !== ANY_LANGUAGE)
}

/**
 * A segmentation profile's source languages: several languages, or Any
 * language, which stands alone. The field shows Any language when the list
 * is empty.
 */
export function SourceLanguagesPicker({ value, onChange, languages, disabled, 'data-testid': testId }: {
  value: string[]
  onChange: (languages: string[]) => void
  languages: LangOption[]
  disabled?: boolean
  'data-testid'?: string
}) {
  const options = useMemo(() => [{ value: ANY_LANGUAGE, label: 'Any language' }, ...toLangOptions(languages)], [languages])
  const shown = value.length === 0 ? [ANY_LANGUAGE] : value
  return (
    <MultiCombobox {...langPickerProps} wrap value={shown} options={options} disabled={disabled} className="w-full"
      onChange={picked => onChange(nextSourceLanguages(shown, picked))}
      placeholder="Any language" aria-label="Source languages" data-testid={testId} />
  )
}
