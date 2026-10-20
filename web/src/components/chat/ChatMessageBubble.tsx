// One chat message: tool chips, inline charts, markdown answer, a request
// for a screenshot or a recording, confirm cards and the in-flight activity line.
import { Wrench } from 'lucide-react'
import { Markdown } from '@/components/markdown/Markdown'
import { Chip } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import { SpecChart } from '../charts/SpecChart'
import type { ChatToolCall, ChartSpec } from '../../api'
import { ConfirmAction, type ConfirmState, type PendingConfirm } from './ConfirmAction'
import { ChatScreenshotRequest } from './ChatScreenshotRequest'
import { ChatRepeatRequest } from './ChatRepeatRequest'
import type { ChatAsk, ChatSend } from './chat-ask'

export interface DisplayMessage {
  role: 'user' | 'assistant'
  content: string
  toolCalls: ChatToolCall[]
  /** Inline charts from render_chart client actions.
   *  In-memory only — reloading a conversation keeps the text, not the chart. */
  charts: ChartSpec[]
  /** Guarded tool previews awaiting the user's click. */
  confirmations: PendingConfirm[]
  /** Tool the assistant is running right now (streaming only). */
  runningTool?: string
  error?: string
  /** The assistant asked for a screenshot or a recording. In-memory only. */
  ask?: ChatAsk
}

export function MessageBubble({
  message,
  working,
  elapsed,
  getConversationId,
  onConfirmState,
  onSend,
}: {
  message: DisplayMessage
  working?: boolean
  elapsed?: number
  getConversationId: () => number | undefined
  onConfirmState: (confirmIndex: number, state: ConfirmState) => void
  /** Answers an ask; the example conversation has none. */
  onSend?: ChatSend
}) {
  if (message.role === 'user') {
    return (
      <div className="ml-auto w-fit max-w-[85%] rounded-2xl bg-gray-100 px-3 py-2 text-sm text-gray-900 dark:bg-gray-800 dark:text-gray-100">
        {message.content}
      </div>
    )
  }
  return (
    <div className="mr-4 text-sm text-gray-800 dark:text-gray-200">
      {message.toolCalls.length > 0 && (
        <div className="mb-1 flex flex-wrap gap-1">
          {groupToolCalls(message.toolCalls).map((g, i) => (
            <ToolChip key={i} call={{ name: g.name, ok: g.ok }} count={g.count} />
          ))}
        </div>
      )}
      {message.runningTool && <ToolChip call={{ name: message.runningTool, ok: true }} running />}
      {message.charts.map((c, i) => (
        <SpecChart key={i} spec={c} testId="chat-chart" />
      ))}
      {message.content && <Markdown variant="chat">{message.content}</Markdown>}
      {onSend && message.ask?.kind === 'screenshot' && (
        <ChatScreenshotRequest reason={message.ask.reason} disabled={working} onSend={onSend} />
      )}
      {onSend && message.ask?.kind === 'repeat' && (
        <ChatRepeatRequest reason={message.ask.reason} disabled={working} onSend={onSend} />
      )}
      {message.confirmations.map((c, ci) => (
        <ConfirmAction
          key={ci}
          item={c}
          getConversationId={getConversationId}
          onStateChange={state => onConfirmState(ci, state)}
        />
      ))}
      {/* Always-visible activity line while the turn runs — a long multi-tool
          answer can go 30s+ between visible events, and a panel with nothing
          moving reads as hung. The elapsed count appears
          after 5s so quick answers don't flicker. */}
      {working && !message.error && (
        <p className="mt-1 flex items-center gap-1.5 text-xs text-gray-400" data-testid="chat-working">
          <span className="inline-flex gap-0.5">
            <span className="h-1 w-1 animate-bounce rounded-full bg-gray-400 [animation-delay:0ms]" />
            <span className="h-1 w-1 animate-bounce rounded-full bg-gray-400 [animation-delay:150ms]" />
            <span className="h-1 w-1 animate-bounce rounded-full bg-gray-400 [animation-delay:300ms]" />
          </span>
          {message.runningTool
            ? 'Running tools…'
            : message.toolCalls.length > 0
              ? 'Thinking over the results…'
              : 'Working…'}
          {(elapsed ?? 0) >= 5 && <span data-testid="chat-elapsed">{elapsed}s</span>}
        </p>
      )}
      {message.error && (
        <p className="mt-1 rounded bg-red-50 px-2 py-1 text-xs text-red-700 dark:bg-red-950/40 dark:text-red-300" data-testid="chat-error">
          {message.error}
        </p>
      )}
    </div>
  )
}

// groupToolCalls merges consecutive same-name calls into one chip. A model
// answering "which projects are overdue?" can legitimately fan out dozens of
// identical lookups; 39 identical chips is noise where "get_project ×39" is
// signal. A group counts as failed if ANY call in it failed — a hidden failure
// must not disappear into a green chip.
function groupToolCalls(calls: ChatToolCall[]): { name: string; ok: boolean; count: number }[] {
  const groups: { name: string; ok: boolean; count: number }[] = []
  for (const c of calls) {
    const last = groups[groups.length - 1]
    if (last && last.name === c.name) {
      last.count++
      last.ok = last.ok && c.ok
    } else {
      groups.push({ name: c.name, ok: c.ok, count: 1 })
    }
  }
  return groups
}

function ToolChip({ call, running, count = 1 }: { call: ChatToolCall; running?: boolean; count?: number }) {
  // Chip label drops the openloc_ prefix — it's namespacing for models, noise for humans.
  const label = call.name.replace(/^openloc_/, '')
  return (
    <Chip
      data-testid="chat-tool-chip"
      color={running ? 'blue' : call.ok ? 'grey' : 'red'}
      icon={<Wrench />}
      className={cn('font-normal', running && 'animate-pulse')}
    >
      {label}
      {count > 1 && <span className="opacity-70">×{count}</span>}
    </Chip>
  )
}
