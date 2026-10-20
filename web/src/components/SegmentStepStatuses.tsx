import { useQuery } from '@tanstack/react-query'
import { listSegmentStepStatuses } from '../api'
import { queryKeys } from '../lib/query-keys'
import { Chip } from '@/components/ui/chip'
import { OUTCOME_LABELS } from './SegmentRow.shared'
import { stepEndStatusColor, stepEndStatusLabel } from './step-status-labels'

/**
 * How each finished workflow step left the segment, oldest first: the status
 * it ended with, whether a person edited the text, and the AI verdict the step
 * produced. Renders nothing before the first step has finished.
 */
export function SegmentStepStatuses({ translationId }: { translationId: number }) {
  const { data: rounds = [] } = useQuery({
    queryKey: queryKeys.segmentHistory.stepStatuses(translationId),
    queryFn: () => listSegmentStepStatuses(translationId),
    staleTime: 30_000,
  })
  if (rounds.length === 0) return null
  return (
    <div data-testid="segment-step-statuses" className="rounded border border-gray-200 dark:border-gray-700 px-2 py-1.5 space-y-1">
      <p className="text-[10px] text-gray-500 dark:text-gray-400">Workflow steps</p>
      {rounds.map(r => (
        <div key={`${r.step_order}-${r.round}-${r.ended_at}`} data-testid="segment-step-status-row" className="flex flex-wrap items-center gap-1.5">
          <Chip size="small">{r.round > 1 ? `${r.step_name} · round ${r.round}` : r.step_name}</Chip>
          <Chip size="small" color={stepEndStatusColor(r.status)}>{stepEndStatusLabel(r.status)}</Chip>
          <span className="text-[10px] text-gray-500 dark:text-gray-400">
            {[
              r.edited ? 'Edited' : 'Not edited',
              r.ai_outcome ? OUTCOME_LABELS[r.ai_outcome] ?? r.ai_outcome : null,
              r.end_reason === 'rejected' ? 'Sent back' : null,
            ].filter(Boolean).join(' · ')}
          </span>
        </div>
      ))}
    </div>
  )
}
