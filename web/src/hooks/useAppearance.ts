import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { getAppearance, updateAppearance, type Appearance } from '../api/appearance'
import { isDemoReadOnly } from '../lib/demo'
import { queryKeys } from '../lib/query-keys'
import { DEFAULT_DARK_ID, DEFAULT_LIGHT_ID } from '../lib/palettes'

const DEFAULTS: Appearance = {
  light_palette: DEFAULT_LIGHT_ID,
  dark_palette: DEFAULT_DARK_ID,
  custom_palettes: [],
}

/**
 * Reads + writes the current user's appearance. Saving updates the
 * `['appearance']` query cache optimistically, so PaletteProvider re-applies the
 * new colors instantly (no wait for the round-trip).
 */
export function useAppearance() {
  const qc = useQueryClient()
  const { data } = useQuery({
    queryKey: queryKeys.appearance.current,
    queryFn: getAppearance,
    staleTime: Infinity,
    retry: false,
  })

  // Raw useMutation: the optimistic update and its rollback need onMutate,
  // which useMutationWithToast does not offer.
  const mut = useMutation({
    mutationFn: updateAppearance,
    onMutate: async (next: Appearance) => {
      await qc.cancelQueries({ queryKey: queryKeys.appearance.current })
      const prev = qc.getQueryData<Appearance>(queryKeys.appearance.current)
      qc.setQueryData(queryKeys.appearance.current, next)
      return { prev }
    },
    onError: (e, _next, ctx) => {
      if (ctx?.prev) qc.setQueryData(queryKeys.appearance.current, ctx.prev)
      // The demo organization's refusal has its own notice (main.tsx).
      if (!isDemoReadOnly(e)) toast.error('Failed to save appearance')
    },
    onSuccess: saved => qc.setQueryData(queryKeys.appearance.current, saved),
  })

  return {
    appearance: data ?? DEFAULTS,
    save: (next: Appearance) => mut.mutate(next),
    saving: mut.isPending,
  }
}
