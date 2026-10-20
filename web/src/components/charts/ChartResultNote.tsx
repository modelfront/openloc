// The small "this answer is not quite what you asked for" marker on a chart.
//
// The metrics layer can change an answer in three ways, and they do NOT deserve
// the same treatment:
//
//   - `folded` — the smallest values were combined. The chart already draws a
//     labelled "Other" band, so it announces itself and needs nothing here.
//   - `grain`  — re-bucketed to a wider interval because the range could not
//     carry the requested one. The answer is correct, just coarser.
//   - `truncated` — rows were DROPPED. The chart looks exactly like a complete
//     one, which is why it is the only case that has to be shouted.
//
// Per card, never on the shared filter bar: two charts on one dashboard can be
// re-bucketed differently, because overflow depends on how many values each
// one's dimensions produce.
import { SectionInfo } from '@/components/ui/section-info'

const GRAIN_LABEL: Record<string, string> = { day: 'daily', week: 'weekly', month: 'monthly' }

export function ChartResultNote({ truncated, grain, requestedGrain }: {
  truncated?: boolean
  /** The grain the answer actually uses. */
  grain?: string
  /** What the chart asked for; a note appears only when they differ. */
  requestedGrain?: string
}) {
  const widened = !!grain && !!requestedGrain && grain !== requestedGrain
  if (!truncated && !widened) return null

  if (truncated) {
    return (
      <SectionInfo tone="amber" size="sm" label="Incomplete result" data-testid="chart-truncated-note">
        This chart shows only part of the data — there was more than it can display. Narrow the date range or add a filter to see a complete answer.
      </SectionInfo>
    )
  }
  return (
    <SectionInfo size="sm" label="Resolution changed" data-testid="chart-grain-note">
      {`Shown ${GRAIN_LABEL[grain!] ?? grain}: the selected range is too long to chart ${GRAIN_LABEL[requestedGrain!] ?? requestedGrain}. Shorten the range to see finer detail.`}
    </SectionInfo>
  )
}
