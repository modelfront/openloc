import { useQuery } from '@tanstack/react-query'
import { listChatModels } from '@/api'
import { queryKeys } from '@/lib/query-keys'

/**
 * Whether the assistant can answer: the org has a key for at least one chat
 * model the runtime supports. False while the models load, so an offer to use
 * the assistant never shows for an org that has no key.
 */
export function useChatAvailable(): boolean {
  const { data: models } = useQuery({
    queryKey: queryKeys.chat.models,
    queryFn: listChatModels,
    staleTime: 5 * 60_000,
  })
  return models?.some(m => m.selectable) ?? false
}
