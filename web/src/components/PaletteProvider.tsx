import { useEffect } from 'react'
import { useTheme } from 'next-themes'
import { useQuery } from '@tanstack/react-query'
import { getAppearance } from '../api/appearance'
import { queryKeys } from '../lib/query-keys'
import {
  applyPalette,
  cacheAppearance,
  readCachedAppearance,
  resolvePalette,
  DEFAULT_DARK_ID,
  DEFAULT_LIGHT_ID,
} from '../lib/palettes'

/**
 * Applies the user's chosen palette. It watches the active mode from
 * next-themes and the user's appearance from the server, resolves the palette
 * for whichever mode is showing, and writes its CSS variables onto <html>.
 *
 * The server appearance is fetched once and cached to localStorage so a reload
 * can apply the right colors before React renders (see applyCachedPalette in
 * main.tsx) — this provider just keeps things in sync at runtime.
 *
 * Not mounted globally: getAppearance() needs a session, and a 401 would trip
 * the global logout-redirect in request(). The app shell and the docs mount it;
 * signed-out pages pass `anonymous`, which skips the fetch and uses the
 * appearance cached in this browser (or the defaults), still following the
 * mode. The login page gets its colors from applyCachedPalette() at boot.
 * Rendered with no children as a side-effect-only component.
 */
export function PaletteProvider({ anonymous = false, children }: { anonymous?: boolean; children?: React.ReactNode }) {
  const { resolvedTheme } = useTheme()

  const { data } = useQuery({
    queryKey: queryKeys.appearance.current,
    queryFn: getAppearance,
    staleTime: Infinity,
    retry: false,
    enabled: !anonymous,
  })

  useEffect(() => {
    const mode = resolvedTheme === 'dark' ? 'dark' : 'light'
    // Until the server answers, the copy saved in this browser stands in, so
    // the colors main.tsx applied at boot don't drop to the defaults meanwhile.
    const appearance = data ?? readCachedAppearance()
    const customs = appearance?.custom_palettes ?? []
    const id = mode === 'dark'
      ? appearance?.dark_palette ?? DEFAULT_DARK_ID
      : appearance?.light_palette ?? DEFAULT_LIGHT_ID
    applyPalette(resolvePalette(id, mode, customs))
    if (data) cacheAppearance(data)
  }, [resolvedTheme, data, anonymous])

  return <>{children}</>
}
