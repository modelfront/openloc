import { useMemo } from 'react'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { getTMMatches, type TMMatch } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useDebounce } from './useDebounce'

/** Enough delay to skip the intermediate segments while arrowing through a
 *  file, short enough that landing on one feels immediate. */
export const TM_LOOKUP_DEBOUNCE_MS = 150

/**
 * Fuzzy TM hits for a segment's source text. The translation id names the
 * segment, so an exact hit can score 101% in context, as pre-translation does.
 *
 * Two consumers share this: EditorSidebar renders the hits as cards, and
 * CATEditor needs the top hit to diff the active segment's source against. Both
 * land on the same query key, so react-query serves one request.
 */
export function useTMMatches(jobId: number, sourceText: string, translationId: number | null): {
  /** Every hit to DISPLAY. Stale ones are still listed — the panel dims them
   *  rather than flashing to empty — so callers must check `stale` before
   *  acting on one. */
  matches: TMMatch[]
  /** The hits that may be ACTED on: inserted by shortcut, diffed against.
   *  Empty while stale. */
  usable: TMMatch[]
  isFetching: boolean
  /** True while the hits belong to a DIFFERENT segment than the active one —
   *  either the source is still inside its debounce, or the new segment's
   *  request is in flight and keepPreviousData is serving the previous
   *  segment's rows.
   *  Callers must treat such hits as inert: inserting one writes the previous
   *  segment's translation into this cell, and diffing against one compares
   *  two unrelated sentences.
   *
   *  Not keyed on isFetching alone: a background refetch of the SAME source
   *  (staleTime expiry) shows correct rows and must stay usable. */
  stale: boolean
  debouncedSource: string
} {
  // Source and translation id are debounced as a pair: they change together
  // on a segment switch.
  const segment = useMemo(() => ({ source: sourceText, translationId }), [sourceText, translationId])
  const debounced = useDebounce(segment, TM_LOOKUP_DEBOUNCE_MS)
  const debouncedSource = debounced.source
  // A source under 3 chars isn't worth a TM fuzzy search.
  const enabled = debouncedSource.length > 2

  const { data, isFetching, isPlaceholderData } = useQuery({
    queryKey: queryKeys.tmMatches.byJob(jobId, debouncedSource, debounced.translationId),
    queryFn: () => getTMMatches(jobId, debouncedSource, debounced.translationId),
    enabled,
    staleTime: 60_000,
    placeholderData: keepPreviousData,
  })

  // A gated query never resolves, so keepPreviousData would serve the previous
  // segment's rows into that pending state indefinitely — including on
  // deactivate, where the source goes empty. Blanking whenever the gate is
  // closed keeps keepPreviousData to bridging real in-flight fetches
  const matches = enabled ? data ?? [] : []
  const stale = isPlaceholderData || debounced !== segment

  return {
    matches,
    usable: stale ? [] : matches,
    isFetching,
    stale,
    debouncedSource,
  }
}
