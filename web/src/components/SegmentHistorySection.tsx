import { memo, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listSegmentHistory } from '../api'
import { queryKeys } from '../lib/query-keys'
import { timeAgo } from '../lib/time-ago'
import { groupHistoryRows, type HistoryRowModel } from '../lib/segment-history-group'
import { EditorSidebarSection } from './EditorSidebarSection'
import { HISTORY_KIND_CHIP_COLORS, HISTORY_KIND_LABELS, historyKindColor, OUTCOME_LABELS, SegmentText } from './SegmentRow.shared'
import { Chip } from '@/components/ui/chip'
import { SegmentStepStatuses } from './SegmentStepStatuses'

interface Props {
  /** null when no segment is active — the empty state says so. */
  translationId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Every recorded change to the active segment, newest first — the read surface
 * over `translation_history`.
 *
 * Open by default, like every other sidebar section. The query is still gated
 * on `open`, so collapsing it stops the per-segment fetch.
 *
 * Records text changes and status changes (approved / auto-approved /
 * unconfirmed). A draft autosave writes no text, but does record the
 * `unconfirmed` row when it takes an approval away. Locks, TM no-matches and
 * failed MT calls leave nothing at all, so this is not a complete activity
 * log — see the ADR.
 *
 * A person's saves in one step are one row with their last text; machine
 * events keep a row each (lib/segment-history-group.ts). Each row marks what
 * changed since the row before it — added words highlighted, removed words
 * struck through — and the first text, or the first after a clear, is not
 * marked. That is display only, and every event stays in the table.
 */
function SegmentHistorySection({ translationId, open, onOpenChange }: Props) {
  const { data: events = [], isLoading, isError } = useQuery({
    queryKey: queryKeys.segmentHistory.byTranslation(translationId ?? 0),
    queryFn: () => listSegmentHistory(translationId!),
    enabled: open && translationId != null,
    staleTime: 30_000,
  })
  const rows = useMemo(() => groupHistoryRows(events), [events])

  return (
    <EditorSidebarSection
      data-testid="segment-history-section"
      title="History"
      open={open}
      onToggle={() => onOpenChange(!open)}
      // Counts the rows the panel draws, not raw events: a run is one row, and
      // absorbed events are not drawn at all. A raw total would never add up
      // on screen.
      meta={open && rows.length > 0 && <span data-testid="segment-history-count">{rows.length}</span>}
      bodyClassName="space-y-1"
    >
      {!translationId && <p className="text-[10px] text-gray-400 px-1">Select a segment</p>}
      {translationId && isLoading && <p className="text-[10px] text-gray-400 px-1">Loading…</p>}
      {translationId && isError && (
        <p className="text-[10px] text-red-600 dark:text-red-400 px-1">Could not load history.</p>
      )}
      {translationId && !isLoading && !isError && events.length === 0 && (
        <p className="text-[10px] text-gray-400 px-1" data-testid="segment-history-empty">
          No recorded changes yet
        </p>
      )}
      {translationId && <SegmentStepStatuses translationId={translationId} />}
      {rows.map(row => <HistoryRow key={row.status.id} row={row} />)}
    </EditorSidebarSection>
  )
}

function HistoryRow({ row }: { row: HistoryRowModel }) {
  const { shown, status } = row
  const label = shown.kind === 'tm' && shown.tm_match_percent != null
    ? `${shown.tm_match_percent}%`
    : HISTORY_KIND_LABELS[shown.kind] ?? shown.kind

  // One source line per kind: which TM matched, which engine translated, who
  // edited. Falls back to the email when a user has no display name.
  const source = shown.kind === 'tm' ? shown.tm_name
    : shown.kind === 'mt' || shown.kind === 'ape' ? shown.mt_engine_name
    : shown.created_by_name || shown.created_by_email

  const outcome = typeof shown.details?.predict_outcome === 'string'
    ? shown.details.predict_outcome
    : null

  // The machine's approval is a chip, never words: `Auto-approved` stands
  // alone when nothing was rewritten, and sits beside `APE` when the rewrite
  // was approved. One visual means "approved by the pipeline" either way.
  const apeApproved = shown.kind === 'ape' && outcome === 'ape_approved'
  // On those two kinds the chip carries the outcome on its own — an `ape` row
  // is either approved or waiting for a human, so the absent chip says the
  // rest. Any other kind that records an outcome still spells it out.
  const outcomeText = outcome && shown.kind !== 'auto_approved' && shown.kind !== 'ape'
    ? OUTCOME_LABELS[outcome] ?? outcome
    : null

  // Whether the row ended approved is read from its newest event: a plain
  // `human` row is an edit someone confirmed, an `approved` one a confirm
  // without changes, and `propagation` approves the siblings it copies to.
  //
  // The step handoff writes `human` rows too, for text delivered without a
  // confirm, and those say nothing: since every confirm is labelled, a `human`
  // row with no label is the handoff. The panel never has to state a negative,
  // and the status column can't stand in for either — it describes the segment
  // now, not what happened at this event. A row that is itself an `approved`
  // event already says so in its chip.
  const fromHandoff = status.details?.source === 'step_handoff'
  const confirmed = shown.kind !== 'approved' && (
    status.kind === 'approved'
    || (!fromHandoff && (status.kind === 'human' || status.kind === 'propagation')))

  const meta = [
    confirmed ? 'Approved' : null,
    outcomeText,
    shown.step_name,
  ].filter(Boolean)

  return (
    <div
      data-testid="segment-history-row"
      data-kind={shown.kind}
      className="rounded border border-gray-200 dark:border-gray-700 px-2 py-1.5"
    >
      <div className="flex items-center gap-1.5 mb-0.5">
        <Chip size="small" color={historyKindColor(shown.kind, shown.tm_match_percent)} className="shrink-0">
          {label}
        </Chip>
        {apeApproved && (
          <Chip size="small" color={HISTORY_KIND_CHIP_COLORS.auto_approved} className="shrink-0">
            {HISTORY_KIND_LABELS.auto_approved}
          </Chip>
        )}
        {source && <span className="text-[10px] text-gray-500 dark:text-gray-400 truncate">{source}</span>}
        <span
          className="text-[10px] text-gray-400 ml-auto shrink-0"
          title={new Date(status.created_at).toLocaleString()}
        >
          {timeAgo(status.created_at)}
        </span>
      </div>
      {/* Status events carry no text, and the row stays compact — the chip,
          who and when is the whole event. `cleared` is the one text-less kind
          that still needs a line, because "the translation is gone" is not
          something the chip alone conveys. */}
      {shown.text && (
        <div className="text-[11px] text-gray-700 dark:text-gray-300 whitespace-pre-wrap break-words">
          <SegmentText text={shown.text} richText={shown.rich_text} changesFrom={row.previousText} />
        </div>
      )}
      {!shown.text && shown.kind === 'cleared' && (
        <p className="text-[11px] italic text-gray-400">Translation removed</p>
      )}
      {meta.length > 0 && (
        <p className="text-[10px] text-gray-400 mt-0.5">{meta.join(' · ')}</p>
      )}
    </div>
  )
}

// The sidebar re-renders on every keystroke in the active segment; nothing here
// depends on the edited text.
export default memo(SegmentHistorySection, (prev, next) =>
  prev.translationId === next.translationId && prev.open === next.open)
