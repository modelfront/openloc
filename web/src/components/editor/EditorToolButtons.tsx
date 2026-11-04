import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { NO_QA_PROFILE_REASON, qaButtonLabel } from '@/lib/qa-findings'
import type { QAReport } from '@/api'

/** The toolbar's Spell check button: opens the review dialog. */
export function SpellCheckButton({ disabled, onClick }: { disabled: boolean; onClick: () => void }) {
  return (
    <Tooltip>
      <TooltipTrigger render={
        <Button onClick={onClick} disabled={disabled} variant="outline" size="sm" data-testid="open-spellcheck" />
      }>
        Spell check
      </TooltipTrigger>
      <TooltipContent>
        Review spelling one error at a time, with replace all and ignore all
      </TooltipContent>
    </Tooltip>
  )
}

/** The toolbar's Run QA button, with the open findings' count; off while the project has no QA profile. */
export function RunQAButton({ report, pending, disabled, onRun }: {
  report: QAReport | undefined
  pending: boolean
  disabled: boolean
  onRun: () => void
}) {
  if (report?.profile_assigned === false) {
    return (
      <Button data-testid="run-qa-btn" disabled disabledReason={NO_QA_PROFILE_REASON} variant="outline" size="sm">
        {qaButtonLabel(false, report.summary)}
      </Button>
    )
  }
  return (
    <Tooltip>
      <TooltipTrigger render={
        <Button data-testid="run-qa-btn" onClick={onRun} disabled={pending || disabled} variant="outline" size="sm" />
      }>
        {qaButtonLabel(pending, report?.summary)}
      </TooltipTrigger>
      <TooltipContent>Run quality checks on all segments (tags, numbers, terminology, punctuation)</TooltipContent>
    </Tooltip>
  )
}
