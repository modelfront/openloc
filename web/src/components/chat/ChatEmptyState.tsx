// An empty conversation: what the assistant can help with, and one example
// question that fits the user's role and whether the organization has any
// projects yet (chatStarter). Clicking it sends it.
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { getMe, listProjectsFilteredPage } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isLinguistRole } from '@/lib/roles'
import { ProviderLogo } from './ModelPicker'
import { chatStarter } from './chat-starters'

export function ChatEmptyState({ provider, onAsk }: { provider: string; onAsk: (text: string) => void }) {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const linguist = isLinguistRole(me?.role)
  // One project is enough to know. Under the paginated prefix, so creating or
  // deleting a project refreshes it.
  const { data: page } = useQuery({
    queryKey: [...queryKeys.projects.paginated, 'any'],
    queryFn: () => listProjectsFilteredPage({}, 0, 1),
    enabled: !!me && !linguist,
  })
  const starter = me ? chatStarter(me.role, page ? page.data.length > 0 : undefined) : undefined

  return (
    <div className="flex h-full flex-col items-center justify-center gap-1 text-center text-sm text-gray-400">
      <ProviderLogo provider={provider} className="mb-1 h-7 w-7" />
      <p>Ask about your projects, jobs, deadlines,</p>
      <p>quality reports, TM or terminology.</p>
      {starter && (
        <Button variant="outline" size="sm" className="mt-3" onClick={() => onAsk(starter)} data-testid="chat-starter">
          {starter}
        </Button>
      )}
    </div>
  )
}
