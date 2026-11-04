import { useQuery } from '@tanstack/react-query'
import { getMe, listChatModels } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { isVendorOrg } from '@/lib/roles'

/**
 * Whether the assistant can answer: the org has a key for at least one chat
 * model the runtime supports. False while the models load, so an offer to use
 * the assistant never shows for an org that has no key. A vendor org has no
 * assistant, so the models are not even asked for there.
 */
export function useChatAvailable(): boolean {
  const me = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const { data: models } = useQuery({
    queryKey: queryKeys.chat.models,
    queryFn: listChatModels,
    staleTime: 5 * 60_000,
    enabled: !me.isPending && !isVendorOrg(me.data),
  })
  return models?.some(m => m.selectable) ?? false
}
