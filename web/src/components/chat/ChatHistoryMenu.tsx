// Earlier conversations of the current user, newest first. Picking one
// reopens it in the panel.
import { History } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { listChatConversations } from '../../api'
import { queryKeys } from '../../lib/query-keys'

export function ChatHistoryMenu({ activeId, onOpen }: { activeId: number | undefined; onOpen: (id: number) => void }) {
  const { data: conversations, isLoading } = useQuery({
    queryKey: queryKeys.chat.conversations,
    queryFn: listChatConversations,
    staleTime: 30_000,
  })
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button variant="ghost" size="icon-xs" title="Earlier conversations" data-testid="chat-history">
            <History className="h-4 w-4" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="max-h-80 w-72 overflow-y-auto">
        {isLoading && <DropdownMenuItem disabled>Loading…</DropdownMenuItem>}
        {!isLoading && !conversations?.length && <DropdownMenuItem disabled>No earlier conversations</DropdownMenuItem>}
        {conversations?.map(c => (
          <DropdownMenuItem
            key={c.id}
            data-testid={`chat-history-${c.id}`}
            onClick={() => onOpen(c.id)}
            description={`${new Date(c.updated_at).toLocaleString()}${c.id === activeId ? ' · open' : ''}`}
          >
            <span className="min-w-0 truncate">{c.title || 'Untitled conversation'}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
