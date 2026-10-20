import { useFieldArray, useWatch, Controller, type Control } from 'react-hook-form'
import { useQuery } from '@tanstack/react-query'
import { listLanguages } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { MultiCombobox } from '@/components/ui/combobox'
import { Textarea } from '@/components/ui/textarea'
import { AddItemButton } from '@/components/list/AddItemButton'
import { DeleteButton } from '@/components/ui/delete-button'
import { EmptyState } from '@/components/layout/EmptyState'
import { cn } from '@/lib/utils'
import { toLangOptions, langPickerProps } from '../lang-options'

// Per-language additions to a system prompt. One prompt serves every language
// its engine routes, so advice that only holds for some of them has nowhere
// else to go: in the shared text it reaches jobs it does not apply to.
//
// The target language is the constraint — a router picks its engine by target,
// so that is the axis a prompt's languages vary along. A row reads "any job
// into German". At least one language is required: a row constraining nothing
// would apply to every job, which is what the prompt's own text is for.

/** The form shape this field edits. Matches AIPromptInput.language_additions. */
export interface LanguageAdditionRow {
  target_languages: string[]
  text: string
}

/** A row with no language admits every job, so it is never a valid addition. */
export function isCompleteAddition(row: LanguageAdditionRow): boolean {
  return row.target_languages.length > 0 && row.text.trim() !== ''
}

/** True for a row the author opened and left completely alone. */
export function isUntouchedAddition(row: LanguageAdditionRow): boolean {
  return row.target_languages.length === 0 && row.text.trim() === ''
}

export function LanguageAdditionsField({
  control, readOnly, error,
}: {
  // Loosely typed so any form carrying `language_additions` can host the field
  // without the whole form's value type leaking in here.
  control: Control<never>
  readOnly?: boolean
  /** The array-level validation message, shown under the label. */
  error?: string
}) {
  const { fields, append, remove } = useFieldArray({ control, name: 'language_additions' as never })
  // The live values, not useFieldArray's `fields` — that is a snapshot taken on
  // add/remove, so it would not notice the author filling the row in.
  const rows = (useWatch({ control, name: 'language_additions' as never }) ?? []) as LanguageAdditionRow[]
  const unfinished = rows.some(r => !isCompleteAddition(r))
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const options = toLangOptions(languages ?? [])

  const picker = (index: number) => (
    <Controller
      control={control}
      name={`language_additions.${index}.target_languages` as never}
      render={({ field }) => (
        // The wrapper carries the row's own test id: langPickerProps sets
        // `data-testid="lang"` on the picker itself, which is what gives its
        // rows the `lang-option-${code}` ids the E2E helpers look for.
        <div className="flex-1" data-testid={`prompt-language-addition-${index}-target`}>
          <MultiCombobox
            options={options}
            value={(field.value ?? []) as string[]}
            onChange={field.onChange}
            placeholder="Pick target languages"
            className="w-full"
            disabled={readOnly}
            wrap
            {...langPickerProps}
          />
        </div>
      )}
    />
  )

  return (
    <div data-field="language_additions">
      <p className="mb-1.5 text-[11px] text-muted-foreground">
        Add prompts that apply only to jobs into particular target languages.
      </p>
      {error && <p role="alert" className="mb-1.5 text-xs text-destructive">{error}</p>}
      <div className="space-y-2" data-testid="prompt-language-additions">
        {fields.length === 0 && readOnly && (
          <EmptyState message="No language-specific additions." />
        )}
        {fields.map((field, index) => {
          // The array-level message says an addition is incomplete but not which
          // one; the frame points at it. Mirrors a router's invalid route row.
          const invalid = !!error && rows[index] && !isCompleteAddition(rows[index])
          return (
          <div
            key={field.id}
            className={cn(
              'group space-y-2 rounded-md border border-dashed border-input p-2',
              invalid && 'border-red-400 dark:border-red-600',
            )}
            data-invalid={invalid ? 'true' : undefined}
            data-testid="prompt-language-addition"
          >
            <div className="flex items-center gap-2">
              <span className="w-8 shrink-0 text-[11px] uppercase tracking-wide text-muted-foreground">Into</span>
              {picker(index)}
              {!readOnly && (
                <DeleteButton
                  className="shrink-0"
                  aria-label="Remove addition"
                  title="Remove addition"
                  onClick={() => remove(index)}
                />
              )}
            </div>
            <Controller
              control={control}
              name={`language_additions.${index}.text` as never}
              render={({ field: text }) => (
                <Textarea
                  value={(text.value ?? '') as string}
                  onChange={text.onChange}
                  onBlur={text.onBlur}
                  rows={2}
                  disabled={readOnly}
                  placeholder="e.g. Address the reader formally."
                  className="font-mono"
                  inputClassName="min-h-14 text-xs"
                  data-testid={`prompt-language-addition-${index}-text`}
                />
              )}
            />
          </div>
          )
        })}
        {!readOnly && (
          <AddItemButton
            label="Add prompt"
            disabled={unfinished}
            title={unfinished ? 'Finish the last prompt first' : undefined}
            onClick={() => append({ target_languages: [], text: '' } as never)}
            testId="prompt-language-addition-add"
          />
        )}
      </div>
    </div>
  )
}
