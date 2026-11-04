import { useEffect, useMemo, useRef, useState } from 'react'
import { useStableArray } from '@/hooks/useStableArray'
import { useEditorSearch } from '@/hooks/useEditorRows'
import { filterSegments, DEFAULT_FILTERS, type SegmentFilters } from '@/lib/segment-filters'
import type { IndexRow } from '@/lib/editor-index'
import type { QaFinding } from '@/lib/qa-findings'
import type { EditorId } from '@/lib/editor-scope'

interface Options {
  jid: EditorId
  rows: IndexRow[]
  qaResultsByTranslation: Map<number, QaFinding[]>
  commentCountByTranslation: Map<number, number>
  predictedBeforeStep: Set<number>
  commentCountsFailed: boolean
  qaReportFailed: boolean
}

/**
 * The filter bar's state and the rows it leaves, as positions in the index.
 * The text search runs on the server over the whole file; the other filters
 * run here over the index.
 */
export function useSegmentFiltering({
  jid, rows, qaResultsByTranslation, commentCountByTranslation, predictedBeforeStep, commentCountsFailed, qaReportFailed,
}: Options) {
  const [segmentFilters, setSegmentFilters] = useState<SegmentFilters>(DEFAULT_FILTERS)
  const segmentSearchRef = useRef<HTMLInputElement>(null)

  // A failed counts fetch is indistinguishable from "nothing has comments":
  // indicators disappear and the Commented filter matches zero rows, which
  // reads as fact rather than as a broken request. Drop the filter and let the
  // bar disable the pill so the absence is legible.
  useEffect(() => {
    if (commentCountsFailed) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- a one-way reset on a failed fetch, not derived state
      setSegmentFilters(f => (f.hasComments ? { ...f, hasComments: false } : f))
    }
  }, [commentCountsFailed])

  // A failed report fetch is indistinguishable from "this job is clean": the
  // dots disappear and the QA filters match either every row (No issues) or
  // none, which reads as fact rather than as a broken request. Drop the filter
  // and let the bar disable the pills so the absence is legible.
  //
  // isLoadingError, not isError: a refetch that fails while cached findings are
  // still on screen leaves the dots rendering, so disabling the pills there
  // would contradict them — and the filter reset is one-way, the user's choice
  // does not come back when the next refetch succeeds.
  useEffect(() => {
    if (qaReportFailed) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- a one-way reset on a failed fetch, not derived state
      setSegmentFilters(f => (f.qaFilters.length === 0 ? f : { ...f, qaFilters: [] }))
    }
  }, [qaReportFailed])

  const textSearch = useMemo(() => (segmentFilters.searchText.trim() === '' ? null : {
    text: segmentFilters.searchText,
    source: segmentFilters.searchSource,
    target: segmentFilters.searchTarget,
    caseSensitive: segmentFilters.caseSensitive,
    regex: segmentFilters.useRegex,
  }), [segmentFilters])
  const { matches: textMatches, state: searchState } = useEditorSearch(jid, textSearch)

  // Stable while the visible rows and their order stay the same: a save
  // replaces an index row, and a new array here would make the row
  // virtualizer recompute every row position (its getItemKey depends on it).
  const filteredIndices = useStableArray(useMemo(
    () => filterSegments(rows, qaResultsByTranslation, segmentFilters, textMatches, commentCountByTranslation, predictedBeforeStep),
    [rows, qaResultsByTranslation, segmentFilters, textMatches, commentCountByTranslation, predictedBeforeStep],
  ))

  return { segmentFilters, setSegmentFilters, segmentSearchRef, searchState, filteredIndices }
}
