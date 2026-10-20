import { Button } from '@/components/ui/button'
import { EditorSidebarSection } from './EditorSidebarSection'
import { useQAFindingIgnore } from '../hooks/use-qa-finding-ignore'
import { activeIssues, findingDotClass, severityLabel, severityTextClass, type QaFinding } from '../lib/qa-findings'

interface Props {
  issues: QaFinding[]
  /** null when no segment is active — the empty state says so rather than
   *  claiming the segment is clean. */
  translationId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Receives the segment's fresh finding list after ignore / un-ignore. */
  onRefresh?: (translationId: number, issues: QaFinding[]) => void
}

/**
 * The active segment's stored QA findings, listed in full with a per-finding
 * ignore / un-ignore button.
 *
 * The only place to ignore / un-ignore a finding: the row's dot
 * (QASummaryDot) is read-only, one dot per segment with the list in a tooltip.
 *
 * Findings appear here after a QA run: the job-wide `Run QA`, the per-segment
 * run on autosave, or a confirm. The section does
 * not run QA itself.
 */
export default function QAFindingsSection({ issues, translationId, open, onOpenChange, onRefresh }: Props) {
  // Ignored findings stay listed — a reviewer has to see what was suppressed to
  // un-ignore it — but the header counts them apart from the open ones, so the
  // number here matches the toolbar's `QA (N)`.
  const openCount = activeIssues(issues).length
  const ignoredCount = issues.length - openCount
  const countLabel = [
    openCount > 0 ? String(openCount) : null,
    ignoredCount > 0 ? `${ignoredCount} ignored` : null,
  ].filter(Boolean).join(' + ')

  return (
    <EditorSidebarSection
      data-testid="qa-findings-section"
      title="QA findings"
      open={open}
      onToggle={() => onOpenChange(!open)}
      meta={countLabel && <span data-testid="qa-findings-count">{countLabel}</span>}
      bodyClassName="space-y-0.5"
    >
      {issues.length === 0 && (
        <p className="text-[10px] text-gray-400 px-1">
          {translationId ? 'No issues' : 'Select a segment'}
        </p>
      )}
      {issues.map(issue => (
        <QAFindingRow key={issue.check_type} issue={issue} onRefresh={onRefresh} />
      ))}
    </EditorSidebarSection>
  )
}

function QAFindingRow({ issue, onRefresh }: { issue: QaFinding; onRefresh?: Props['onRefresh'] }) {
  const { busy, toggle } = useQAFindingIgnore(issue, fresh => onRefresh?.(issue.translation_id, fresh))

  return (
    <div
      data-testid="qa-finding-row"
      data-check-type={issue.check_type}
      data-ignored={issue.ignored || undefined}
      className="flex items-center gap-2 text-xs rounded px-1.5 py-1"
    >
      <span
        aria-hidden="true"
        className={`shrink-0 inline-block w-2 h-2 rounded-full ${findingDotClass(issue)}`}
      />
      <div className="min-w-0 flex-1">
        <span className={`font-semibold ${issue.ignored ? 'text-gray-400 dark:text-gray-500' : severityTextClass(issue.severity)}`}>
          {severityLabel(issue.severity)}
        </span>
        <span className={issue.ignored ? 'text-gray-400 dark:text-gray-500' : 'text-gray-700 dark:text-gray-300'}>
          : {issue.message}
        </span>
        {issue.ignored && (
          <p className="text-[10px] text-gray-400 dark:text-gray-500" data-testid="qa-finding-ignored-meta">
            Ignored{issue.ignored_by_name ? ` by ${issue.ignored_by_name}` : ''}
            {issue.ignored_at ? ` on ${new Date(issue.ignored_at).toLocaleDateString()}` : ''}
          </p>
        )}
      </div>
      <Button
        size="xs"
        variant="outline"
        className="shrink-0"
        data-testid={issue.ignored ? 'qa-finding-unignore-btn' : 'qa-finding-ignore-btn'}
        disabled={busy}
        onClick={toggle}
      >
        {issue.ignored ? 'Un-ignore' : 'Ignore'}
      </Button>
    </div>
  )
}
