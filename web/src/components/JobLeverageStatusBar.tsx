// JobLeverageStatusBar — always-visible status bar
// pinned to the bottom of the CAT editor. Shows the same 6-band leverage
// breakdown as saved analyses, but live (recomputed on every reload + on
// translation invalidation via the queryClient cache).
//
// One row: the job's size (segments · words) on the left, then the bands in
// the middle — each `<label>: <words> words`, with the Draft share as a
// subtle suffix when non-zero. Compact intentionally — this is a glanceable
// bar, not a table.

import { useQuery } from '@tanstack/react-query'
import { getJobLeverage, ANALYSIS_BANDS, type JobLeverage } from '../api/analyses'
import { queryKeys } from '../lib/query-keys'
import type { RepetitionStats } from '../lib/cat-editor-logic'
import { CountMeta } from '@/components/ui/count-meta'

// The match bands from New up to 101%, then Repetitions last. Saved analyses
// keep ANALYSIS_BANDS' own order; this bar reads left to right by leverage.
const BAR_BANDS = [
  ...ANALYSIS_BANDS.filter(b => b.id !== 'repetitions'),
  ...ANALYSIS_BANDS.filter(b => b.id === 'repetitions'),
]

interface Props {
  jobId: number
  // Repetition stats computed client-side from the segments list (only
  // later occurrences count). The backend's repetition bands are always 0,
  // so this prop overrides the band's display when provided. Parent
  // (CATEditor) owns the calculation via `computeRepetitionStats`.
  localRepetitionStats?: RepetitionStats
  /** The job's size, shown first. */
  segmentCount: number
  wordCount: number
}

export default function JobLeverageStatusBar({ jobId, localRepetitionStats, segmentCount, wordCount }: Props) {
  const { data, isLoading } = useQuery({
    // Centralised key (queryKeys.job.leverage) so invalidateJobContext
    // refetches on confirm/save; the 15s poll catches edits the cache helper
    // doesn't see (e.g. another user editing the job from a different tab).
    queryKey: queryKeys.job.leverage(jobId),
    queryFn: () => getJobLeverage(jobId),
    refetchInterval: 15000,
    staleTime: 1000,
  })

  return (
    <div className="fixed bottom-0 left-0 right-0 z-30 bg-gray-900 text-gray-100 dark:bg-gray-950 border-t border-gray-700 dark:border-gray-800 text-[11px] font-mono">
      {/* Equal flex-1 sides keep the bands centred on the bar. */}
      <div className="px-4 py-1 flex items-center gap-4 overflow-x-auto" data-testid="job-leverage-bar">
        <CountMeta
          items={[{ count: segmentCount, noun: 'segment' }, { count: wordCount, noun: 'word' }]}
          className="flex-1 shrink-0 whitespace-nowrap text-white"
        />
        <div className="flex shrink-0 items-center gap-4">
        <span className="text-gray-400 shrink-0">Leverage:</span>
        {isLoading || !data ? (
          <span className="text-gray-500">loading…</span>
        ) : (
          BAR_BANDS.map(b => {
            // FE-computed override for the Repetitions band — see Props comment.
            // The backend always returns 0 for repetitions today, so when the
            // parent provided local stats we use them. needsReview doesn't
            // apply to repetitions (they're a source-text relationship, not a
            // review state).
            if (b.id === 'repetitions' && localRepetitionStats) {
              return (
                <span key={b.id} className="shrink-0 whitespace-nowrap">
                  <span className="text-gray-400">{b.label}:</span>{' '}
                  <span className="text-white">{localRepetitionStats.words.toLocaleString()} words</span>
                  {localRepetitionStats.segments > 0 && (
                    <span className="text-gray-400"> ({localRepetitionStats.segments.toLocaleString()} seg)</span>
                  )}
                </span>
              )
            }
            const band = data[b.id as keyof JobLeverage] as { words: number; words_needs_review: number } | undefined
            const words = band?.words ?? 0
            const needsReview = band?.words_needs_review ?? 0
            return (
              <span key={b.id} className="shrink-0 whitespace-nowrap">
                <span className="text-gray-400">{b.label}:</span>{' '}
                <span className="text-white">{words.toLocaleString()} words</span>
                {needsReview > 0 && (
                  <span className="text-amber-400"> ({needsReview.toLocaleString()} Draft)</span>
                )}
              </span>
            )
          })
        )}
        </div>
        <div className="flex-1" />
      </div>
    </div>
  )
}
