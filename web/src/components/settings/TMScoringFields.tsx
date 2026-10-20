import { useAutosaveDraft } from '@/hooks/useAutosaveDraft'
import { HelpTip } from '@/components/ui/help-tip'
import { Input } from '@/components/ui/input'
import { TM_LOOKUP_ORDER_LABELS, type TMLookupOrder } from '@/lib/settings-values'

/** The match-scoring values a settings row (or TM profile) carries. */
export interface TMScoringValues {
  /** 0–1 */
  fuzzyThreshold: number
  sublanguagePenaltyPct: number
  nearExactPenaltyPct: number
}

/** A partial PATCH body for the match-scoring values, including the TM lookup
 * order that TMLookupOrderField edits. */
export interface TMScoringUpdate {
  fuzzy_threshold?: number
  sublanguage_penalty_pct?: number
  near_exact_penalty_pct?: number
  tm_lookup_order?: TMLookupOrder
}

/**
 * The id every scoring-save toast carries: the fields save while typing, so a
 * run of edits replaces one toast instead of stacking several.
 */
export const SCORING_SAVED_TOAST_ID = 'tm-scoring-saved'

/** The success toast for one scoring update; null when it touches no scoring field. */
export function describeScoringSave(u: TMScoringUpdate): string | null {
  if (u.fuzzy_threshold !== undefined) return `Fuzzy threshold set to ${Math.round(u.fuzzy_threshold * 100)}%`
  if (u.sublanguage_penalty_pct !== undefined) return `Language-variant mismatch penalty set to ${u.sublanguage_penalty_pct}%`
  if (u.near_exact_penalty_pct !== undefined) return `Near-exact penalty set to ${u.near_exact_penalty_pct}%`
  if (u.tm_lookup_order !== undefined) return `TM lookup order set to “${TM_LOOKUP_ORDER_LABELS[u.tm_lookup_order]}”`
  return null
}

interface Props {
  values: TMScoringValues
  /**
   * Create mode saves on every keystroke (nothing goes to the server until the
   * form is submitted); edit mode saves after a pause in typing, and on blur.
   */
  isCreate: boolean
  disabled?: boolean
  /** The values belong to a linked TM profile: shown, but locked. */
  readOnly?: boolean
  /**
   * Saves one update. Return a promise resolving to `false` (or rejecting)
   * when the save failed, so the field shows the stored value again and the
   * same value can be retried.
   */
  onCommit: (update: TMScoringUpdate) => unknown
}

/**
 * Match scoring: the fuzzy threshold that decides what is offered at all, then
 * the two penalties that dock what is. No box or heading of its own; it sits
 * straight on the translation memory card. Used by the project/template
 * settings editor and by the TM profile page, so both surfaces show the same
 * three fields in the same order.
 */
export function TMScoringFields({ values, isCreate, disabled = false, readOnly = false, onCommit }: Props) {
  const locked = disabled || readOnly

  return (
    <div data-testid="tm-scoring" data-readonly={readOnly ? 'true' : undefined}>
      <PercentField
        id="fuzzy-match-threshold"
        label="Fuzzy match threshold"
        help="The minimum similarity a translation memory match needs before it's used at all; matches below it aren't offered. Set it to 100% to accept exact matches only."
        stored={String(Math.round(values.fuzzyThreshold * 100))}
        disabled={locked}
        instant={isCreate}
        onSave={pct => onCommit({ fuzzy_threshold: pct / 100 })}
      />
      <div className="mt-4 flex flex-wrap gap-x-8 gap-y-4">
        <PercentField
          id="sublanguage-penalty"
          label="Language-variant mismatch penalty"
          help="Charged when only the region differs: a match from a different region of the same language, such as an “en” entry on an “en-US” project. Charged separately for the source and the target, so a match that differs on both sides pays it twice. At 0% the two count as the same language and an otherwise-exact match is approved automatically. Also called the sublanguage penalty; 1% is a common default."
          stored={String(values.sublanguagePenaltyPct)}
          disabled={locked}
          instant={isCreate}
          onSave={pct => onCommit({ sublanguage_penalty_pct: pct })}
        />
        <PercentField
          id="near-exact-penalty"
          label="Near-exact penalty"
          help="Charged when only punctuation, capitalisation or spacing differs, such as “Hello.” for “Hello”. Without it, a trailing full stop on a short segment drags the score down to its raw edit distance, around 90% on a three-word segment, which reads as a much worse match than it is."
          stored={String(values.nearExactPenaltyPct)}
          disabled={locked}
          instant={isCreate}
          onSave={pct => onCommit({ near_exact_penalty_pct: pct })}
        />
      </div>
    </div>
  )
}

// A whole 0–100 percentage, or null: an empty or out-of-range field saves
// nothing, and blur puts the stored value back.
const parsePct = (raw: string): number | null => {
  if (raw.trim() === '') return null
  const pct = Number(raw)
  return Number.isInteger(pct) && pct >= 0 && pct <= 100 ? pct : null
}

// A 0–100 percentage input: label with its help tooltip, then the field.
function PercentField({ id, label, help, stored, disabled, instant, onSave }: {
  id: string
  label: string
  help: string
  /** The saved value; the field shows it and snaps back to it when left invalid. */
  stored: string
  disabled: boolean
  instant: boolean
  onSave: (pct: number) => void
}) {
  const field = useAutosaveDraft({ stored, parse: parsePct, format: String, onSave, instant })
  return (
    <div className="min-w-[200px] flex-1">
      <div className="flex items-center gap-1 mb-1">
        <label htmlFor={id} className="text-xs font-medium text-gray-700 dark:text-gray-300">{label}</label>
        <HelpTip text={help} />
      </div>
      <div className="w-24">
        <Input
          type="number"
          id={id}
          aria-label={label}
          min={0}
          max={100}
          step={1}
          value={field.value}
          onChange={e => field.onChange(e.target.value)}
          onBlur={field.onBlur}
          className="h-8 w-24 px-2"
          action={<span className="text-sm text-gray-500">%</span>}
          disabled={disabled}
        />
      </div>
    </div>
  )
}
