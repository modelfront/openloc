import { HelpTip } from '@/components/ui/help-tip'
import { Combobox } from '@/components/ui/combobox'
import { TM_LOOKUP_ORDER_LABELS, type TMLookupOrder } from '@/lib/settings-values'
import type { TMScoringUpdate } from './TMScoringFields'

const HINTS: Record<TMLookupOrder, string> = {
  match_percent: 'Exact matches from any TM come first, then fuzzy matches in TM order.',
  tm_order: 'A fuzzy match from a higher TM wins over an exact match from a lower TM.',
}

interface Props {
  value: TMLookupOrder
  /** How many TMs are assigned. The order only matters with two or more. */
  tmCount: number
  disabled?: boolean
  /** The value belongs to a linked TM profile: shown, but locked. */
  readOnly?: boolean
  onCommit: (update: TMScoringUpdate) => unknown
}

/**
 * "TM lookup order": which of several memories fills a
 * segment. Rendered under the TM list it orders, on the project/template
 * settings editor and the TM profile page. Hidden below two TMs, where there
 * is nothing to order.
 */
export function TMLookupOrderField({ value, tmCount, disabled = false, readOnly = false, onCommit }: Props) {
  if (tmCount < 2) return null
  const locked = disabled || readOnly
  return (
    <div data-testid="tm-lookup-order-field">
      <div className="flex items-center gap-1 mb-1">
        <label htmlFor="tm-lookup-order" className="text-xs font-medium text-gray-700 dark:text-gray-300">TM lookup order</label>
        <HelpTip text="Decides which memory fills a segment when several memories have a match. By match percentage, all memories are searched for an exact match first, then for a fuzzy one. By TM order, the first memory in the list is searched for an exact and then a fuzzy match before the next memory is asked, so its fuzzy match is kept even if a lower memory has an exact one." />
      </div>
      <Combobox
        id="tm-lookup-order"
        aria-label="TM lookup order"
        data-testid="tm-lookup-order-select"
        value={value}
        onChange={v => {
          if (v !== value && v in TM_LOOKUP_ORDER_LABELS) onCommit({ tm_lookup_order: v as TMLookupOrder })
        }}
        options={(Object.keys(TM_LOOKUP_ORDER_LABELS) as TMLookupOrder[]).map(v => ({
          value: v, label: TM_LOOKUP_ORDER_LABELS[v],
        }))}
        disabled={locked}
        searchable={false}
        className="w-64"
      />
      <p className="mt-1 text-xs leading-snug text-gray-500 dark:text-gray-400">
        {HINTS[value]}
      </p>
    </div>
  )
}
