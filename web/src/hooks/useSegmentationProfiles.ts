import { useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listSegmentationProfiles, type SegmentationProfile } from '../api'
import { queryKeys } from '../lib/query-keys'

/** The key prefix of the default built-in, for any language; equal to segmentation.DefaultKeyPrefix (pinned by a Go test). */
const DEFAULT_BUILTIN_PREFIX = 'builtin:default@'

/** Whether a profile is the default built-in for any language. */
export function isAnyLanguageDefault(profile: Pick<SegmentationProfile, 'builtin_key'>): boolean {
  return !!profile.builtin_key?.startsWith(DEFAULT_BUILTIN_PREFIX)
}

/**
 * The profile a project or template in the list's language uses: `id` while
 * the list holds it, else the one the server marks `suggested`. A list for no
 * language marks none; then it is the default built-in, which is also what
 * the server picks for no language.
 * Null while the list is empty (still loading).
 */
export function pickSegmentationProfile(profiles: SegmentationProfile[], id: number | null | undefined): number | null {
  if (id != null && profiles.some(p => p.id === id)) return id
  const fallback = profiles.find(p => p.suggested) ?? profiles.find(isAnyLanguageDefault)
  return fallback?.id ?? null
}

/**
 * Whether a form must wait for a profile pick before it saves: the list holds
 * none that `id` resolves to. A list that failed to load does not block; the
 * server then picks the suggested profile itself.
 */
export function isSegmentationProfileMissing(profiles: SegmentationProfile[], error: unknown, id: number | null | undefined): boolean {
  return !error && pickSegmentationProfile(profiles, id) == null
}

/**
 * The segmentation profiles that suit a source language (every profile when
 * there is none yet), and `pick` to resolve a chosen id against them. Shared
 * by the settings picker and the New project form, which sends what it shows.
 */
export function useSegmentationProfiles(sourceLanguage: string | undefined) {
  const lang = sourceLanguage || undefined
  const query = useQuery({
    queryKey: queryKeys.segmentationProfiles.list(lang),
    queryFn: () => listSegmentationProfiles(lang),
  })
  const profiles = query.data
  const pick = useCallback((id: number | null | undefined) => pickSegmentationProfile(profiles ?? [], id), [profiles])
  const missing = useCallback((id: number | null | undefined) => isSegmentationProfileMissing(profiles ?? [], query.error, id), [profiles, query.error])
  return { profiles: profiles ?? [], isLoading: query.isLoading, error: query.error, refetch: query.refetch, pick, missing }
}

/**
 * A setup form's segmentation (New project, New template): the form sends the
 * profile it shows — the pick while it suits the source language, else the
 * suggested one — and cannot save while no profile resolves.
 */
export function useFormSegmentation(sourceLanguage: string | undefined) {
  const { pick, missing } = useSegmentationProfiles(sourceLanguage)
  const withSegmentation = useCallback(
    <T extends { segmentationProfileId: number | null }>(s: T): T => ({ ...s, segmentationProfileId: pick(s.segmentationProfileId) }),
    [pick],
  )
  const missingReason = useCallback(
    (id: number | null | undefined) => (missing(id) ? 'Pick a segmentation profile.' : undefined),
    [missing],
  )
  return { withSegmentation, missingReason }
}
