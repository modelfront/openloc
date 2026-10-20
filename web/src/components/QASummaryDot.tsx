import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { findingDotClass, IGNORED_DOT_CLASS, severityDotClass, severityLabel, worstSeverity, type QaFinding } from '../lib/qa-findings'

/**
 * A segment's QA findings as ONE dot in the row: the colour of the most severe
 * open finding (error > warning > info), or a hollow grey ring when every
 * finding is ignored. Hover lists all findings, ignored ones included.
 * Read-only: ignore / un-ignore lives in the sidebar's QA findings section.
 */
export default function QASummaryDot({ issues }: { issues: QaFinding[] }) {
  if (issues.length === 0) return null
  const worst = worstSeverity(issues)
  const summary = issues
    .map(f => `${f.ignored ? 'Ignored ' : ''}${severityLabel(f.severity)}: ${f.message}`)
    .join('; ')

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            tabIndex={0}
            data-testid="qa-issue-badge"
            data-severity={worst ?? undefined}
            data-ignored={worst === null || undefined}
            data-count={issues.length}
            aria-label={`QA: ${summary}`}
            className="inline-flex items-center cursor-help"
          />
        }
      >
        <span className={`inline-block w-2 h-2 rounded-full ${worst ? severityDotClass(worst) : IGNORED_DOT_CLASS}`} />
      </TooltipTrigger>
      <TooltipContent side="top">
        <ul className="space-y-1" data-testid="qa-issue-tooltip">
          {issues.map(f => (
            <li key={f.check_type} className={`flex items-start gap-1.5 ${f.ignored ? 'opacity-60' : ''}`}>
              <span aria-hidden="true" className={`mt-1 shrink-0 inline-block w-2 h-2 rounded-full ${findingDotClass(f)}`} />
              <span>
                <span className="font-semibold">{severityLabel(f.severity)}</span>
                {f.ignored && ' (ignored)'}: {f.message}
              </span>
            </li>
          ))}
        </ul>
      </TooltipContent>
    </Tooltip>
  )
}
