import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { listLanguages } from '../api'
import { queryKeys } from '../lib/query-keys'
import {
  NUMBER_FORMATS,
  numberFormatFromKey,
  numberFormatKey,
  type NumberFormatLanguage,
} from '../lib/qa-number-format'
import { langPickerProps, toLangOptions } from './lang-options'
import { Button } from '@/components/ui/button'
import { Combobox, MultiCombobox } from '@/components/ui/combobox'
import { DeleteButton } from '@/components/ui/delete-button'
import { LanguageCode } from '@/components/ui/language-code'

const FORMAT_OPTIONS = NUMBER_FORMATS.map(f => ({ value: numberFormatKey(f), label: f.label }))

/**
 * The languages of a QA profile that write numbers other than their usual way,
 * each with its format, shown while Number format is on. Several languages can
 * be added at once, all with the same format. Each change saves the whole list
 * through onChange, which rejects when the save fails; the languages being
 * added then stay picked.
 */
export function QANumberFormatLanguages({
  languages,
  onChange,
  saving,
}: {
  languages: NumberFormatLanguage[]
  onChange: (languages: NumberFormatLanguage[]) => Promise<unknown>
  saving: boolean
}) {
  const { data: allLanguages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const [newLanguages, setNewLanguages] = useState<string[]>([])
  const [newFormat, setNewFormat] = useState(FORMAT_OPTIONS[0].value)

  const names = new Map((allLanguages ?? []).map(l => [l.code, l.name]))
  const used = new Set(languages.map(l => l.language))
  const languageOptions = toLangOptions((allLanguages ?? []).filter(l => !used.has(l.code)))

  // A failed save is announced by the caller's toast.
  const save = (next: NumberFormatLanguage[]) => onChange(next).then(() => true, () => false)
  const add = async () => {
    const format = numberFormatFromKey(newFormat)
    if (await save([...languages, ...newLanguages.map(language => ({ language, ...format }))])) setNewLanguages([])
  }

  return (
    <div className="px-4 pb-3 pl-12" data-testid="qa-number-format-languages">
      <div className="text-sm font-medium text-gray-900 dark:text-gray-100">Number format by language</div>
      <div className="text-sm text-gray-400 dark:text-gray-500">
        For a language that writes numbers differently from its usual format. Its numbers and measurements are read this way.
      </div>
      <div className="mt-2 flex flex-col gap-2">
        {languages.map((l, i) => (
          <div key={l.language} className="flex items-center gap-3" data-testid={`qa-number-format-language-${l.language}`}>
            <div className="w-56 min-w-0 flex items-center gap-2">
              <LanguageCode code={l.language} />
              <span className="truncate text-sm text-gray-600 dark:text-gray-400">{names.get(l.language)}</span>
            </div>
            <Combobox
              className="w-40"
              options={FORMAT_OPTIONS}
              value={numberFormatKey(l)}
              data-testid={`qa-number-format-${l.language}`}
              onChange={key => save(languages.map((x, j) => (j === i ? { ...x, ...numberFormatFromKey(key) } : x)))}
              aria-label={`Number format for ${l.language.toUpperCase()}`}
              searchPlaceholder="Search formats..."
              disabled={saving}
            />
            <DeleteButton
              aria-label={`Remove ${l.language.toUpperCase()}`}
              onClick={() => save(languages.filter((_, j) => j !== i))}
              disabled={saving}
            />
          </div>
        ))}
        <div className="flex items-center gap-3">
          <MultiCombobox
            {...langPickerProps}
            className="w-56"
            options={languageOptions}
            value={newLanguages}
            onChange={setNewLanguages}
            placeholder="Languages"
            aria-label="Languages to add"
          />
          <Combobox
            className="w-40"
            options={FORMAT_OPTIONS}
            value={newFormat}
            onChange={setNewFormat}
            aria-label="Number format to add"
            searchPlaceholder="Search formats..."
            data-testid="qa-number-format-new-format"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={add}
            disabled={!newLanguages.length || saving}
            disabledReason={!newLanguages.length ? 'Pick a language first' : saving ? 'Saving...' : undefined}
            startIcon={<Plus />}
            data-testid="qa-number-format-add"
          >
            {newLanguages.length > 1 ? `Add ${newLanguages.length} languages` : 'Add language'}
          </Button>
        </div>
      </div>
    </div>
  )
}
