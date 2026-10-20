// Final-step completion copy. Completing the FINAL workflow
// step is different from every other step: it doesn't advance the job — it
// marks the job completed and saves its approved segments to TM. This
// module is the single source of that copy. Exports by surface:
//   finalStepAutomations — bullet list (CAT editor completion dialog)
//   finalStepSentence    — one sentence (ConfirmDialogs + action-button tooltips)
//   finalStepTooltip     — current-step chip tooltip (CAT editor breadcrumb)
//   BULK_COMPLETE_CLAUSE — plural clause (bulk bar tooltip + dialog)
// Don't hand-write this copy elsewhere.

export interface FinalStepCopyInput {
  approvedCount: number
}

export interface AutomationLine {
  key: 'complete' | 'tm'
  text: string
}

function segmentNoun(n: number): string {
  return n === 1 ? 'segment' : 'segments'
}

// finalStepAutomations returns the automation bullet list for the CAT editor's
// final-step confirmation dialog.
export function finalStepAutomations({ approvedCount }: FinalStepCopyInput): AutomationLine[] {
  return [
    { key: 'complete', text: 'Mark the job as completed' },
    {
      key: 'tm',
      text: approvedCount > 0
        ? `Save ${approvedCount} human-approved ${segmentNoun(approvedCount)} to Translation memory`
        : 'Save human-approved segments to Translation memory (none yet — nothing will be saved)',
    },
  ]
}

// withTMClause appends the TM save, when there is anything to save, to a
// sentence that starts with "… mark the job completed".
function withTMClause(subject: string, { approvedCount }: FinalStepCopyInput): string {
  if (approvedCount === 0) return `${subject}.`
  return `${subject} and save ${approvedCount} human-approved ${segmentNoun(approvedCount)} to Translation memory.`
}

// finalStepSentence is the one-sentence form for ConfirmDialog descriptions
// and action-button tooltips (plain strings, not lists). fileLabel, when
// given, names the job being completed (e.g. `report.docx (de)`).
export function finalStepSentence({
  approvedCount,
  fileLabel,
}: FinalStepCopyInput & { fileLabel?: string }): string {
  const subject = fileLabel ? `This will mark ${fileLabel} as completed` : 'This will mark the job completed'
  return withTMClause(subject, { approvedCount })
}

// finalStepTooltip is the current-step chip form (CAT editor breadcrumb): the chip is
// not itself an action, so the sentence explains what completing the step
// does rather than what "this" will do.
export function finalStepTooltip(input: FinalStepCopyInput): string {
  return withTMClause('Final step — completing it will mark the job completed', input)
}

// BULK_COMPLETE_CLAUSE is the plural form for the bulk bar, where per-job
// counts aren't meaningful.
export const BULK_COMPLETE_CLAUSE = 'approved segments are saved to Translation memory'
