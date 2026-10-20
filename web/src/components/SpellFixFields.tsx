import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'

/**
 * The source / flagged-target / suggestions / "Change to" block shared by both
 * spell check surfaces: the review dialog (`SpellCheckDialog`) and the inline
 * fix panel (rendered by `RichTextCellEditor`).
 *
 * `replacement` is the correction the caller's buttons will apply: suggestion
 * chips fill it and the "Change to" input edits it freely. Nothing is written
 * from here — each surface applies through its own buttons (the dialog's
 * Change / Change all, the panel's scope rows).
 */

export interface SpellFixFieldsProps {
  /** Source text of the segment. Omitted when the caller has no source to show. */
  sourceText?: string
  /** Target text the offsets are measured against. */
  targetText: string
  /** Plain-text offset + length of the flagged span within `targetText`. */
  offset: number
  length: number
  /** LanguageTool's explanation. */
  message: string
  suggestions: string[]
  /** The correction the caller's buttons will apply. */
  replacement: string
  onReplacementChange: (value: string) => void
  disabled?: boolean
}

/**
 * Renders the flagged span underlined inside its segment.
 *
 * Offsets index the plain text the check was run against, so slicing directly is
 * correct here — unlike mapping into the editor, which needs `plainOffsetToPm`.
 */
export function FlaggedText({ text, offset, length }: { text: string; offset: number; length: number }) {
  const start = Math.max(0, Math.min(offset, text.length))
  const end = Math.max(start, Math.min(offset + length, text.length))
  return (
    <>
      {text.slice(0, start)}
      <span className="spellcheck-error text-red-600 dark:text-red-400">{text.slice(start, end)}</span>
      {text.slice(end)}
    </>
  )
}

export default function SpellFixFields({
  sourceText, targetText, offset, length, message,
  suggestions, replacement, onReplacementChange, disabled,
}: SpellFixFieldsProps) {
  return (
    <div className="space-y-2">
      {sourceText !== undefined && (
        <FormField label="Source" labelVariant="caps">
          <p className="text-[13px] text-gray-600 dark:text-gray-400 break-words" data-testid="spellfix-source">
            {sourceText}
          </p>
        </FormField>
      )}

      <FormField label="Not in dictionary" labelVariant="caps">
        <p className="text-[13px] break-words" data-testid="spellfix-context">
          <FlaggedText text={targetText} offset={offset} length={length} />
        </p>
      </FormField>

      {message && (
        <p className="text-[11px] leading-snug text-gray-500 dark:text-gray-400 break-words">
          {message}
        </p>
      )}

      <FormField label="Suggestions" labelVariant="caps">
        {suggestions.length === 0 ? (
          <p className="text-[11px] text-gray-500 dark:text-gray-400">
            No suggestions — type the correction below.
          </p>
        ) : (
          <div role="radiogroup" aria-label="Suggestions" className="flex flex-wrap gap-1" data-testid="spellfix-suggestions">
            {suggestions.map(s => {
              const isSelected = s === replacement
              return (
                // eslint-disable-next-line no-restricted-syntax -- spelling suggestion chip, not an action button
                <button
                  key={s}
                  type="button"
                  role="radio"
                  aria-checked={isSelected}
                  disabled={disabled}
                  onClick={() => onReplacementChange(s)}
                  className={`px-2 py-0.5 rounded text-xs cursor-pointer border disabled:opacity-40 disabled:cursor-not-allowed ${
                    isSelected
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-gray-200 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800'
                  }`}
                >
                  {s}
                </button>
              )
            })}
          </div>
        )}
      </FormField>

      <FormField label="Change to" labelVariant="caps">
        <Input
          value={replacement}
          onChange={e => onReplacementChange(e.target.value)}
          disabled={disabled}
          aria-label="Change to"
          className="h-8 text-[13px]"
          data-testid="spellcheck-replacement"
        />
      </FormField>
    </div>
  )
}
