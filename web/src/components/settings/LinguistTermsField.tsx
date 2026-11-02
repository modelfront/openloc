import { Combobox } from '@/components/ui/combobox'
import { HelpTip } from '@/components/ui/help-tip'
import { LINGUIST_TERMS_LABELS, type LinguistTerms } from '@/lib/settings-values'

// What each choice means, shown under the picker.
const HINTS: Record<LinguistTerms, string> = {
  off: 'Only project managers add terms from the editor.',
  pending: "Linguists' terms wait for a project manager to approve them.",
  approved: "Linguists' terms are approved at once.",
}

interface Props {
  value: LinguistTerms
  disabled?: boolean
  /** Why linguists cannot add terms here: disables the picker and replaces the hint. */
  disabledReason?: string
  onChange: (value: LinguistTerms) => void
}

/**
 * "Linguists can add terms": whether linguists add terms to the term base from
 * the CAT editor, and the status their terms get. Managers always can, and
 * pick the status themselves.
 */
export function LinguistTermsField({ value, disabled = false, disabledReason, onChange }: Props) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1" data-testid="settings-linguist-terms">
      <span className="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300">
        <label htmlFor="linguist-terms">Linguists can add terms</label>
        <HelpTip text="Linguists add terms to the term base from the editor. A pending term waits for a project manager to approve it." />
      </span>
      <Combobox
        id="linguist-terms"
        aria-label="Linguists can add terms"
        data-testid="linguist-terms-select"
        value={value}
        onChange={v => {
          if (v !== value && v in LINGUIST_TERMS_LABELS) onChange(v as LinguistTerms)
        }}
        options={(Object.keys(LINGUIST_TERMS_LABELS) as LinguistTerms[]).map(v => ({
          value: v, label: LINGUIST_TERMS_LABELS[v],
        }))}
        disabled={disabled || !!disabledReason}
        searchable={false}
        className="w-64"
      />
      {/* Always one line, so the card keeps its height when the picker locks. */}
      <p className="w-full text-xs leading-snug text-gray-500 dark:text-gray-400">{disabledReason ?? HINTS[value]}</p>
    </div>
  )
}
