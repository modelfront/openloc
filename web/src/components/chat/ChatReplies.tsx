// suggest_replies buttons above the message box, when no highlight card is
// showing them. A click sends the reply.
import { Button } from '@/components/ui/button'

export function ChatReplies({ replies, onPick }: { replies: string[]; onPick: (reply: string) => void }) {
  if (replies.length === 0) return null
  return (
    <div className="mb-1.5 flex flex-wrap gap-1.5" data-testid="chat-replies">
      {replies.map(r => (
        <Button key={r} variant="outline" size="xs" onClick={() => onPick(r)}>
          {r}
        </Button>
      ))}
    </div>
  )
}
