// The assistant in the demo organization. Visitors share
// one identity and the demo is read-only, so the real assistant does not run
// here; this panel shows an example conversation instead, with the same
// launcher, frame and message bubbles, so guided tours can point at them.
import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { CHAT_CLOSE_EVENT, CHAT_OPEN_EVENT, CHAT_PROMPT_EVENT } from '../../lib/chat-bus'
import { ChatLauncher, ChatModeBadge, chatWindowClassName } from './ChatLauncher'
import { MessageBubble, type DisplayMessage } from './ChatMessageBubble'
import { ProviderLogo } from './ModelPicker'
import { DEFAULT_SIZE } from './useChatPanelSize'
import { TextLink } from '@/components/ui/text-link'

const message = (m: Partial<DisplayMessage> & Pick<DisplayMessage, 'role' | 'content'>): DisplayMessage => ({
  toolCalls: [], charts: [], confirmations: [], ...m,
})

/**
 * The example conversation: one exchange per thing the assistant does, each
 * with its own anchor so the assistant tour can point at it.
 */
const DEMO_EXCHANGES: { anchor: string; messages: DisplayMessage[] }[] = [
  {
    anchor: 'chat.example.project',
    messages: [
      message({ role: 'user', content: 'Create a project for summer-booking.json into German and French with the Nordwind template, and pre-translate it.' }),
      message({
        role: 'assistant',
        content: 'Done. **Nordwind — Summer 2026 booking flow** is in progress: two jobs, pre-translated from the translation memory and machine translation, with AI review queued for German.',
        toolCalls: [
          { name: 'openloc_create_project', ok: true },
          { name: 'openloc_attach_uploaded_file', ok: true },
          { name: 'openloc_pretranslate_project', ok: true },
        ],
      }),
    ],
  },
  {
    anchor: 'chat.example.knowledge',
    messages: [
      message({ role: 'user', content: 'How do we translate "Nordwind Miles" in French, and when does a fuzzy match get a penalty?' }),
      message({
        role: 'assistant',
        content: 'The glossary keeps **Nordwind Miles** in English, and 12 memory entries agree. A match from another regional variant, such as de-AT for de-DE, loses a few points; the docs page on TM matching has the details.',
        toolCalls: [
          { name: 'openloc_lookup_term', ok: true },
          { name: 'openloc_search_tm', ok: true },
          { name: 'openloc_search_docs', ok: true },
        ],
      }),
    ],
  },
  {
    anchor: 'chat.example.dashboard',
    messages: [
      message({ role: 'user', content: 'Build a shared Operations dashboard: volume per week, MT engines, what AI review approved per language, editing effort in German, QA issues for Nordwind Travel and AI tokens by model. Filters for dates, customer and language.' }),
      message({
        role: 'assistant',
        content: 'The shared dashboard **Operations** is ready, with seven charts and filters for dates, customer and language. Here is the first one:',
        toolCalls: [
          { name: 'openloc_query_metrics', ok: true },
          ...Array.from({ length: 7 }, () => ({ name: 'openloc_create_chart', ok: true })),
          { name: 'openloc_set_dashboard_filters', ok: true },
        ],
        charts: [{
          title: 'Post-editing volume per week', type: 'bar', x_key: 'week', series: ['words'],
          data: [{ week: 'Week 1', words: 38200 }, { week: 'Week 2', words: 45100 }, { week: 'Week 3', words: 41800 }, { week: 'Week 4', words: 54900 }],
        }],
      }),
    ],
  },
  {
    anchor: 'chat.example.tour',
    messages: [
      message({ role: 'user', content: 'Show me how to set up AI review for German.' }),
      message({
        role: 'assistant',
        content: 'Here is a six-step tour: it starts with the AI engines and ends on your project\'s workflow tab. Move through it with Next, or close it any time.',
        toolCalls: [{ name: 'start_tour', ok: true }],
      }),
    ],
  },
  {
    anchor: 'chat.example.confirm',
    messages: [
      message({ role: 'user', content: 'Delete the old Helix memory.' }),
      message({
        role: 'assistant',
        content: 'That deletes a shared translation memory, so it waits for your confirmation:',
        toolCalls: [{ name: 'openloc_delete_tm', ok: true }],
        confirmations: [{
          state: 'pending',
          request: {
            tool: 'openloc_delete_tm', args: { tm_id: 0 }, token: 'demo', expires_at: '2099-01-01T00:00:00Z',
            summary: 'Delete the translation memory "Helix IFU EN→DE" (1,240 entries).',
            effects: ['Projects and templates that use it stop reading from it.'],
          },
        }],
      }),
    ],
  },
]

export function ChatDemoPanel() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const onOpen = () => setOpen(true)
    const onClose = () => setOpen(false)
    window.addEventListener(CHAT_OPEN_EVENT, onOpen)
    window.addEventListener(CHAT_PROMPT_EVENT, onOpen)
    window.addEventListener(CHAT_CLOSE_EVENT, onClose)
    return () => {
      window.removeEventListener(CHAT_OPEN_EVENT, onOpen)
      window.removeEventListener(CHAT_PROMPT_EVENT, onOpen)
      window.removeEventListener(CHAT_CLOSE_EVENT, onClose)
    }
  }, [])

  if (!open) {
    return <ChatLauncher provider="claude" title="See what the assistant does" onOpen={() => setOpen(true)} />
  }
  return (
    <div role="dialog" aria-label="Assistant example" data-testid="chat-demo-panel" style={{ width: DEFAULT_SIZE.w, height: DEFAULT_SIZE.h }} className={chatWindowClassName}>
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-gray-200 px-3 dark:border-gray-800">
        <ProviderLogo provider="claude" className="h-4 w-4" />
        <span className="text-sm font-medium text-gray-900 dark:text-gray-100">Assistant</span>
        <ChatModeBadge allowWrites={false} />
        <div className="ml-auto flex items-center gap-0.5">
          <Button variant="ghost" size="icon-xs" onClick={() => setOpen(false)} title="Close" data-testid="chat-close">
            <X className="h-4 w-4" />
          </Button>
        </div>
      </header>
      <div className="flex-1 space-y-3 overflow-y-auto p-3" data-testid="chat-messages" data-guide="chat.messages">
        {DEMO_EXCHANGES.map(x => (
          <div key={x.anchor} data-guide={x.anchor} className="space-y-3">
            {x.messages.map((m, i) => (
              <MessageBubble key={i} message={m} getConversationId={() => 0} onConfirmState={() => {}} />
            ))}
          </div>
        ))}
      </div>
      <footer className="shrink-0 border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-gray-800 dark:text-gray-400">
        This is an example. In your own organization the assistant answers from your data.{' '}
        <TextLink to="/docs/guides/ai-assistant">How it works</TextLink>
      </footer>
    </div>
  )
}
