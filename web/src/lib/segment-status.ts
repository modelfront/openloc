import type { Translation } from '../api'

export type DisplayStatus =
  | 'untranslated'
  | 'pre_translated'
  | 'needs_review'
  | 'auto_approved'
  | 'approved'

/**
 * Derive the UI-facing segment status from a translation row.
 *
 * The DB `status` column holds three values (needs_review / auto_approved /
 * approved). Approved and Draft belong to the job's current workflow step:
 * each new step puts approved segments back to needs_review and clears every
 * edit mark; auto_approved stays. "untranslated" and
 * "pre_translated" are *derived* — this helper mirrors the predicates of
 * GetJobTranslationCounts in the Go SQL:
 *
 *   - untranslated   : no target text yet (active_text empty)
 *   - pre_translated : text nobody edited in this step and no AI verdict from
 *                      this step — machine output, or an earlier step's work,
 *                      awaiting review
 *   - needs_review   : edited in this step OR given an AI verdict in it, not
 *                      yet confirmed
 *   - auto_approved  : pipeline auto-approved (predict approved)
 *   - approved       : human-confirmed
 *
 * `predictedBeforeStep` says the row's AI verdict came from an earlier step
 * (GET /jobs/{id}/step-statuses); it is history there, not work to do.
 *
 * Every status chip, color, and filter pill keys off this derived value rather
 * than the raw `status`, since the raw column cannot distinguish the first
 * three states.
 */
export function deriveDisplayStatus(
  t: Translation | undefined | null,
  opts?: { predictedBeforeStep?: boolean },
): DisplayStatus {
  if (!t) return 'untranslated'
  if (t.status === 'approved') return 'approved'
  if (t.status === 'auto_approved') return 'auto_approved'
  // Only 'needs_review' remains as a raw value; everything below derives.
  if (!t.active_text) return 'untranslated'
  const predictedThisStep = !!t.predict_outcome?.valid && !opts?.predictedBeforeStep
  if (!t.step_edited && !predictedThisStep) return 'pre_translated'
  return 'needs_review'
}

/**
 * Which derived statuses get a visible badge in the grid's Status column.
 *
 * untranslated / pre_translated are "default" states — the cell stays EMPTY
 * so the column only draws attention to rows that progressed: Draft
 * (human-edited / predict-rejected), Auto-approved, Approved. Where the
 * translation CAME FROM (MT / TM / File) is the Match column's job, so a
 * pretranslated row isn't unlabeled — its provenance chip lives next door.
 */
export function showStatusChip(s: DisplayStatus): boolean {
  return s === 'approved' || s === 'auto_approved' || s === 'needs_review'
}

/** Status-column chip text for a segment the assistant confirmed. */
export const ASSISTANT_CONFIRMED_LABEL = 'Confirmed by assistant'

/**
 * An approved row the assistant confirmed (translations.confirmed_via). It
 * stays out of translation memory until a person confirms it, which resets
 * the marker — so the editor labels it.
 */
export function isAssistantConfirmed(t: Translation | undefined | null): boolean {
  return t?.status === 'approved' && t.confirmed_via === 'assistant'
}

export type MatchChip =
  | 'repetition_source' // →R  first occurrence; confirming auto-propagates
  | 'repetition'        // ⇢R  same source appears earlier in the file
  | 'tm'                // N%  TM match with percent
  | 'ape'               // APE MT post-edited by APE (origin='ape' implies MT lineage)
  | 'mt'                // MT  raw machine translation
  | 'file'              // File bilingual file import

/**
 * The Match column shows exactly ONE provenance chip per row (mutually
 * exclusive by design — locks and QA dots stack separately). Priority:
 *
 *   repetition source > repetition > TM % > APE > MT > File
 *
 * Repetition chips win because they change confirm behavior (propagation);
 * within origin lineage, `ape` outranks `mt` because it's the more specific
 * transformation (APE rewrote the MT output — the chip says so; rendering
 * "MT" there would hide the post-edit).
 */
export function deriveMatchChip(args: {
  isRepetitionSource?: boolean
  isRepetition?: boolean
  origin: string // '' when no automation lineage
}): MatchChip | null {
  if (args.isRepetitionSource) return 'repetition_source'
  if (args.isRepetition) return 'repetition'
  switch (args.origin) {
    case 'tm':          return 'tm'
    case 'ape':         return 'ape'
    case 'mt':          return 'mt'
    case 'file_import': return 'file'
  }
  return null
}
