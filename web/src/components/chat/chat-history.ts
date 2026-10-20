// Conversation history for the assistant panel. The server stores every
// message; the panel remembers which conversation is open so a page refresh
// reopens it, and maps stored messages back to what MessageBubble renders.
import type { ChatMessage } from '../../api'
import type { DisplayMessage } from './ChatMessageBubble'
import { readStorage, removeStorage, writeStorage } from '@/lib/safe-storage'

const ACTIVE_CONVERSATION_KEY = 'openloc-chat-conversation'

export function loadActiveConversation(): number | undefined {
  const raw = readStorage(ACTIVE_CONVERSATION_KEY)
  const id = raw ? Number(raw) : NaN
  return Number.isInteger(id) && id > 0 ? id : undefined
}

/** Blocked storage just means the next refresh starts a new conversation. */
export function saveActiveConversation(id: number | undefined): void {
  if (id === undefined) removeStorage(ACTIVE_CONVERSATION_KEY)
  else writeStorage(ACTIVE_CONVERSATION_KEY, String(id))
}

/**
 * The note the server adds to a message that brought a screenshot, for the
 * model (withScreenshotMarker in internal/chat/screenshot.go). The user's
 * bubble shows what they sent, without it.
 */
const SCREENSHOT_MARKER = /\n\n\[Screenshot from the user \(upload_id: [^)]*\)\.[^\]]*\]$/

/**
 * Stored messages carry text and tool chips. Charts, confirm cards and errors
 * lived only in the page that showed them.
 */
export function toDisplayMessages(rows: ChatMessage[]): DisplayMessage[] {
  return rows.map(m => ({
    role: m.role,
    content: m.role === 'user' ? m.content.replace(SCREENSHOT_MARKER, '') : m.content,
    toolCalls: m.tool_calls ?? [],
    charts: [],
    confirmations: [],
  }))
}
