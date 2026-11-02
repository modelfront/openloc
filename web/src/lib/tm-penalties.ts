/**
 * The explanation the Resources panel gives for a docked TM match: the text
 * match struck out beside the percent, and one line per penalty in its tooltip.
 */

import type { TMMatch, TMPenalty } from '../api'

export interface TMPenaltyLine {
  percent: number
  label: string
  /** Set on the variant kinds: the TM entry's code and the project's. */
  tmLanguage?: string
  projectLanguage?: string
}

export interface TMScoreBreakdown {
  /** The text similarity before penalties. It is the shown percent plus the
   *  penalties, so the lines always add up to the number on the card. */
  textPercent: number
  lines: TMPenaltyLine[]
}

const LABELS: Record<string, string> = {
  source_variant: 'Source language variant',
  target_variant: 'Target language variant',
  near_exact: 'Differs only in punctuation, case or spacing',
  tm: 'Penalty set on this TM',
}

/** A kind this client doesn't know yet still shows its deduction. */
const label = (p: TMPenalty) => LABELS[p.kind] ?? 'Penalty'

/** null when nothing docked the match. */
export function tmScoreBreakdown(match: Pick<TMMatch, 'percent' | 'penalties'>): TMScoreBreakdown | null {
  const penalties = match.penalties ?? []
  if (penalties.length === 0) return null
  return {
    textPercent: match.percent + penalties.reduce((sum, p) => sum + p.percent, 0),
    lines: penalties.map(p => ({
      percent: p.percent,
      label: label(p),
      tmLanguage: p.tm_language,
      projectLanguage: p.project_language,
    })),
  }
}

/** One line as plain text, for the card's accessible description. */
export function tmPenaltyLineText(line: TMPenaltyLine): string {
  const langs = line.tmLanguage && line.projectLanguage
    ? ` (TM: ${line.tmLanguage}, project: ${line.projectLanguage})`
    : ''
  return `−${line.percent}% ${line.label}${langs}`
}
