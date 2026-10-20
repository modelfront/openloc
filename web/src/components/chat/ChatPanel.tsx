// Built-in AI assistant panel. Floating launcher + slide-over panel.
// Streaming text renders through the shared Markdown component (react-markdown
// → React elements, no raw HTML pass-through — the no-dangerouslySetInnerHTML
// rule holds by construction).
import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { X, Send, Square, Plus, Minus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import {
  sendChatMessage,
  NoClaudeKeyError,
  attachmentNote,
  type ChatUpload,
} from '../../api'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ModelPicker, ProviderLogo, useStoredModel } from './ModelPicker'
import { useStoredState } from '@/hooks/useStoredState'
import { listChatModels, listChatMessages } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { CHAT_CLOSE_EVENT, CHAT_OPEN_EVENT, CHAT_PROMPT_EVENT, type ChatPromptDetail } from '../../lib/chat-bus'
import type { ConfirmState } from './ConfirmAction'
import { MessageBubble, type DisplayMessage } from './ChatMessageBubble'
import { ChatHistoryMenu } from './ChatHistoryMenu'
import { ModePicker } from './ModePicker'
import { ChatAttachButton, ChatAttachmentChips } from './ChatAttachments'
import { useChatPanelSize } from './useChatPanelSize'
import { loadActiveConversation, saveActiveConversation, toDisplayMessages } from './chat-history'
import { ApiError } from '../../api/request'
import { visibleAnchors } from '../../lib/guide'
import { clearHeldRecording } from '../../lib/watch-recording'
import { handleClientAction } from './chat-actions'
import { siteLink, useAppConfig } from '@/pages/docs/DocsHost'
import { ChatReplies } from './ChatReplies'
import { ChatEmptyState } from './ChatEmptyState'
import { ChatLauncher, ChatModeBadge, chatWindowClassName } from './ChatLauncher'
import { TextLink } from '@/components/ui/text-link'




export function ChatPanel() {
  // Three states: closed unmounts the window (history discarded — × means
  // "done with this"); minimized keeps it MOUNTED but hidden, so history,
  // the conversation thread, and even an in-flight stream all survive and
  // the user can pop back into the same conversation.
  const [state, setState] = useState<'closed' | 'open' | 'minimized'>('closed')
  // Model selection lives HERE, not in the window: the launcher must show the
  // current provider's logo even while the window is minimized or closed.
  const [model, setModel] = useStoredModel()
  const { data: models } = useQuery({
    queryKey: queryKeys.chat.models,
    queryFn: listChatModels,
    staleTime: 5 * 60_000,
  })
  const current = models?.find(m => m.id === model) ?? models?.find(m => m.default)
  const provider = current?.provider ?? 'claude'
  const anySelectable = models?.some(m => m.selectable) ?? true
  // Prompt cards (chat-bus): open the window with the prompt prefilled. The
  // seq makes consecutive clicks of the SAME card re-apply the prefill.
  const [pendingPrompt, setPendingPrompt] = useState<{ text?: string; hint?: string; seq: number } | null>(null)
  useEffect(() => {
    const onPrompt = (e: Event) => {
      const detail = (e as CustomEvent<ChatPromptDetail>).detail
      if (!detail?.prompt && !detail?.hint) return
      setPendingPrompt(prev => ({ text: detail.prompt, hint: detail.hint, seq: (prev?.seq ?? 0) + 1 }))
      setState('open')
    }
    // A guided tour opens the panel to point at something inside it.
    const onOpen = () => setState('open')
    // Minimized, not closed: the conversation stays.
    const onClose = () => setState(s => (s === 'open' ? 'minimized' : s))
    window.addEventListener(CHAT_PROMPT_EVENT, onPrompt)
    window.addEventListener(CHAT_OPEN_EVENT, onOpen)
    window.addEventListener(CHAT_CLOSE_EVENT, onClose)
    return () => {
      window.removeEventListener(CHAT_PROMPT_EVENT, onPrompt)
      window.removeEventListener(CHAT_OPEN_EVENT, onOpen)
      window.removeEventListener(CHAT_CLOSE_EVENT, onClose)
    }
  }, [])

  return (
    <>
      {/* Launcher — fixed so it survives page scrolling and route changes. */}
      {state !== 'open' && (
        <ChatLauncher
          provider={provider}
          muted={!anySelectable}
          title={state === 'minimized' ? 'Continue the conversation' : 'Ask the assistant'}
          onOpen={() => setState('open')}
        />
      )}
      {state !== 'closed' && (
        <ChatWindow
          hidden={state === 'minimized'}
          pendingPrompt={pendingPrompt}
          model={model}
          onSelectModel={setModel}
          provider={provider}
          modelLabel={current?.label ?? 'Assistant'}
          onMinimize={() => setState('minimized')}
          onClose={() => setState('closed')}
        />
      )}
    </>
  )
}

// Watchdog budget: the backend pings every 25s, so 60s of total silence means
// the stream is dead (LB cut it, network dropped), not slow.
const STALL_TIMEOUT_MS = 60_000

function ChatWindow({ hidden, pendingPrompt, model, onSelectModel, provider, modelLabel, onMinimize, onClose }: { hidden: boolean; pendingPrompt: { text?: string; hint?: string; seq: number } | null; model: string; onSelectModel: (m: string) => void; provider: string; modelLabel: string; onMinimize: () => void; onClose: () => void }) {
  const [messages, setMessages] = useState<DisplayMessage[]>([])
  const [input, setInput] = useState('')
  const [attachments, setAttachments] = useState<ChatUpload[]>([])
  // suggest_replies buttons shown above the input when no highlight card
  // is showing them.
  const [replies, setReplies] = useState<string[]>([])
  const [streaming, setStreaming] = useState(false)
  const [noKey, setNoKey] = useState(false)
  // Seconds since the turn started; drives the "Working… Ns" indicator shown
  // when a multi-tool answer runs long.
  const [elapsed, setElapsed] = useState(0)
  const { size, onResizeStart } = useChatPanelSize()
  // Write mode: per-message consent, default read-only. Persisted so
  // a user who works in write mode isn't re-opted-out on every panel open.
  // Stored as a bare `write` / `read`.
  const [mode, setStoredMode] = useStoredState('openloc-chat-mode', stored => (stored === 'write' ? 'write' : 'read'), { raw: true })
  const allowWrites = mode === 'write'
  const setMode = (write: boolean) => setStoredMode(write ? 'write' : 'read')
  const location = useLocation()
  const navigate = useNavigate()
  // The send callback captures state at click time; the ref keeps the path
  // current so a message sent after navigating carries the NEW page.
  const pathRef = useRef(location.pathname)
  pathRef.current = location.pathname
  const queryClient = useQueryClient()
  const { data: appConfig } = useAppConfig()
  const lastAliveRef = useRef(0)
  // The open conversation: a ref for the stream callbacks, state for the
  // history menu, and localStorage so a page refresh reopens it.
  const conversationIdRef = useRef<number | undefined>(undefined)
  const [conversationId, setConversationIdState] = useState<number | undefined>(undefined)
  const setConversation = useCallback((id: number | undefined) => {
    conversationIdRef.current = id
    setConversationIdState(id)
    saveActiveConversation(id)
  }, [])
  const abortRef = useRef<AbortController | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  // Prompt-card prefill (chat-bus). A prompt replaces the draft (the click IS
  // the user choosing it); a hint only greys the placeholder — nothing
  // sendable is inserted. Never auto-sends either way.
  const [hint, setHint] = useState('')
  useEffect(() => {
    if (!pendingPrompt) return
    if (pendingPrompt.text) setInput(pendingPrompt.text)
    if (pendingPrompt.hint) {
      setInput('')
      setHint(pendingPrompt.hint)
    }
    inputRef.current?.focus()
  }, [pendingPrompt])

  // Follow the stream. Property assignment, not scrollTo(): jsdom implements
  // the former but throws on the latter (see testing.md jsdom-gap patterns).
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages])

  // Cancel an in-flight turn when the panel unmounts.
  useEffect(() => () => abortRef.current?.abort(), [])

  const patchLast = useCallback((patch: (m: DisplayMessage) => DisplayMessage) => {
    setMessages(prev => {
      if (prev.length === 0) return prev
      const next = prev.slice()
      next[next.length - 1] = patch(next[next.length - 1])
      return next
    })
  }, [])

  // While streaming: tick the elapsed counter and watch for a dead stream.
  // The backend pings every 25s even when the model is silently generating,
  // so "no bytes for 60s" is a verdict, not impatience — abort and say so
  // rather than leaving the panel frozen.
  useEffect(() => {
    if (!streaming) return
    const startedAt = Date.now()
    lastAliveRef.current = startedAt
    setElapsed(0)
    const timer = setInterval(() => {
      setElapsed(Math.floor((Date.now() - startedAt) / 1000))
      if (Date.now() - lastAliveRef.current > STALL_TIMEOUT_MS) {
        patchLast(m => ({
          ...m,
          error: 'The connection went quiet and was closed. Try again.',
          runningTool: undefined,
        }))
        abortRef.current?.abort()
      }
    }, 1000)
    return () => clearInterval(timer)
  }, [streaming, patchLast])

  // text overrides the input box (an example question from the empty state,
  // the answer to an ask); a screenshot the user approved goes with it.
  // Resolves to whether the assistant answered.
  const send = useCallback(async (text?: string, extra?: { screenshotUploadId?: string }): Promise<boolean> => {
    const typed = (text ?? input).trim()
    if (!typed || streaming) return false
    let answered = false
    const message = [typed, ...attachments.map(attachmentNote)].join('\n\n')
    setInput('')
    setAttachments([])
    setReplies([])
    setStreaming(true)
    setMessages(prev => [
      ...prev,
      { role: 'user', content: message, toolCalls: [], charts: [], confirmations: [] },
      { role: 'assistant', content: '', toolCalls: [], charts: [], confirmations: [] },
    ])

    const controller = new AbortController()
    abortRef.current = controller
    try {
      await sendChatMessage(
        {
          conversation_id: conversationIdRef.current, message, model: model || undefined, allow_writes: allowWrites || undefined,
          page_path: pathRef.current, visible_anchors: visibleAnchors(), screenshot_upload_id: extra?.screenshotUploadId,
        },
        {
          onText: delta => patchLast(m => ({ ...m, content: m.content + delta })),
          onToolStart: tool => patchLast(m => ({ ...m, runningTool: tool })),
          onToolEnd: (tool, ok) => {
            patchLast(m => ({
              ...m,
              runningTool: undefined,
              toolCalls: [...m.toolCalls, { name: tool, ok }],
            }))
            // A successful create_chart changed the dashboards world: refresh
            // lists, detail grids AND the prompt-card suggestions, so the
            // cards advance to the next unused prompt without a page hop.
            if (ok && tool === 'openloc_create_chart') {
              void queryClient.invalidateQueries({ queryKey: queryKeys.dashboards.all })
            }
          },
          onDone: info => {
            answered = true
            setConversation(info.conversation_id)
            void queryClient.invalidateQueries({ queryKey: queryKeys.chat.conversations })
          },
          onError: error => patchLast(m => ({ ...m, error, runningTool: undefined })),
          onAlive: () => {
            lastAliveRef.current = Date.now()
          },
          onConfirmRequired: request =>
            patchLast(m => ({ ...m, confirmations: [...m.confirmations, { request, state: 'pending' }] })),
          onClientAction: (action, payload) =>
            handleClientAction(action, payload, {
              navigate,
              docsLink: path => siteLink(appConfig, path),
              queryClient,
              minimize: onMinimize,
              patchLast,
              send: text => void sendRef.current(text),
              showReplies: setReplies,
            }),
        },
        controller.signal,
      )
    } catch (err) {
      if (err instanceof NoClaudeKeyError) {
        setNoKey(true)
        // Drop the placeholder pair — nothing was answered.
        setMessages(prev => prev.slice(0, -2))
      } else if ((err as Error).name !== 'AbortError') {
        patchLast(m => ({ ...m, error: (err as Error).message, runningTool: undefined }))
      }
    } finally {
      setStreaming(false)
      abortRef.current = null
    }
    return answered
  }, [input, attachments, streaming, model, allowWrites, navigate, patchLast, onMinimize, setConversation, queryClient, appConfig])

  // Reply buttons outlive the render that created them (the highlight card
  // calls back later), so they go through the current send.
  const sendRef = useRef(send)
  sendRef.current = send

  const stop = useCallback(() => abortRef.current?.abort(), [])

  const getConversationId = useCallback(() => conversationIdRef.current, [])
  const setConfirmState = useCallback((msgIndex: number, confirmIndex: number, state: ConfirmState) => {
    setMessages(prev =>
      prev.map((m, i) =>
        i !== msgIndex
          ? m
          : { ...m, confirmations: m.confirmations.map((c, j) => (j === confirmIndex ? { ...c, state } : c)) },
      ),
    )
  }, [])

  // A recording belongs to the conversation that asked for it: another
  // conversation's report never takes it.
  const reset = useCallback(() => {
    abortRef.current?.abort()
    clearHeldRecording()
    setConversation(undefined)
    setMessages([])
  }, [setConversation])

  const openConversation = useCallback(async (id: number) => {
    abortRef.current?.abort()
    clearHeldRecording()
    try {
      const rows = await listChatMessages(id)
      setConversation(id)
      setMessages(toDisplayMessages(rows))
    } catch (err) {
      // Deleted, or saved by another account on this browser: start fresh.
      if (err instanceof ApiError && err.status === 404) {
        setConversation(undefined)
        return
      }
      setMessages([{
        role: 'assistant', content: '', toolCalls: [], charts: [], confirmations: [],
        error: 'The conversation could not be loaded. Try again.',
      }])
    }
  }, [setConversation])

  // Reopen the conversation that was open before a page refresh.
  useEffect(() => {
    const saved = loadActiveConversation()
    if (saved !== undefined) void openConversation(saved)
  }, [openConversation])

  const close = useCallback(() => {
    clearHeldRecording()
    setConversation(undefined)
    onClose()
  }, [setConversation, onClose])

  return (
    <div
      data-testid="chat-panel"
      // Minimized: display:none INLINE, not a class — an inline style can't
      // lose a specificity fight with the `flex` class, and jsdom (which loads
      // no Tailwind CSS) can still see it in tests.
      style={{ width: size.w, height: size.h, display: hidden ? 'none' : undefined }}
      className={chatWindowClassName}
      // A screenshot for the assistant shows the page, not the conversation,
      // and the trail and a recording keep nothing typed or clicked in it.
      data-screenshot="exclude"
      data-private
    >
      {/* Resize grip — top-left corner, the only corner not pinned to the
          viewport edge. Generous hit area, visible affordance on hover. */}
      <div
        data-testid="chat-resize-handle"
        onPointerDown={onResizeStart}
        title="Drag to resize"
        className="group absolute left-0 top-0 z-10 h-4 w-4 cursor-nwse-resize"
      >
        <svg viewBox="0 0 8 8" className="h-2.5 w-2.5 translate-x-0.5 translate-y-0.5 text-gray-300 group-hover:text-gray-500 dark:text-gray-600 dark:group-hover:text-gray-400">
          <path d="M0 0 L8 0 L0 8 Z" fill="currentColor" />
        </svg>
      </div>
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-gray-200 px-3 dark:border-gray-800">
        <ProviderLogo provider={provider} className="h-4 w-4" />
        <span data-testid="chat-header-model" className="text-sm font-medium text-gray-900 dark:text-gray-100">{modelLabel}</span>
        <ChatModeBadge allowWrites={allowWrites} />
        <div className="ml-auto flex items-center gap-0.5">
          <Button variant="ghost" size="icon-xs" onClick={onMinimize} title="Minimize — keeps the conversation" data-testid="chat-minimize">
            <Minus className="h-4 w-4" />
          </Button>
          <ChatHistoryMenu activeId={conversationId} onOpen={id => void openConversation(id)} />
          <Button variant="ghost" size="icon-xs" onClick={reset} title="New conversation" data-testid="chat-new">
            <Plus className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={close} title="Close" data-testid="chat-close">
            <X className="h-4 w-4" />
          </Button>
        </div>
      </header>

      <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto p-3" data-testid="chat-messages" data-guide="chat.messages">
        {noKey ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center text-sm text-gray-500 dark:text-gray-400">
            <p>No AI provider is connected for your organization.</p>
            <TextLink to="/ai-settings" onClick={onClose}>
              Connect one in AI settings → Providers.
            </TextLink>
          </div>
        ) : messages.length === 0 ? (
          <ChatEmptyState provider={provider} onAsk={text => void send(text)} />
        ) : (
          messages.map((m, i) => (
            <MessageBubble
              key={i}
              message={m}
              working={streaming && i === messages.length - 1}
              elapsed={elapsed}
              getConversationId={getConversationId}
              onConfirmState={(ci, state) => setConfirmState(i, ci, state)}
              onSend={(text, extra) => sendRef.current(text, extra)}
            />
          ))
        )}
      </div>

      <footer className="shrink-0 border-t border-gray-200 p-2 dark:border-gray-800">
        {!streaming && <ChatReplies replies={replies} onPick={r => void send(r)} />}
        <ChatAttachmentChips
          attachments={attachments}
          onRemove={id => setAttachments(prev => prev.filter(a => a.id !== id))}
        />
        <Textarea
          ref={inputRef}
          data-testid="chat-input"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              void send()
            }
          }}
          placeholder={hint || 'Ask something…'}
          rows={1}
          disabled={noKey}
          className="px-1.5"
          inputClassName="min-h-5 max-h-28 resize-none px-1"
          action={
            <>
              {/* Model first, then mode — mirroring the header (logo+model, then
                  the mode badge) so the two rows read in the same order. */}
              <ChatAttachButton onAdded={u => setAttachments(prev => [...prev, u])} disabled={streaming || noKey} />
              <ModelPicker model={model} onSelect={onSelectModel} disabled={streaming} />
              <ModePicker allowWrites={allowWrites} onChange={setMode} disabled={streaming} />
              {streaming ? (
                <Button variant="ghost" size="icon-sm" color="muted" hoverColor="muted"
                  onClick={stop}
                  title="Stop"
                  data-testid="chat-stop"
                >
                  <Square className="size-3.5" />
                </Button>
              ) : (
                // Empty box: a plain grey icon like its neighbours, not a faded
                // primary button (which reads as broken). send() ignores an empty
                // box, so the button stays clickable and says so via aria-disabled.
                <Button
                  size="icon-sm"
                  variant={input.trim() ? 'default' : 'ghost'}
                  color={input.trim() ? undefined : 'muted'}
                  onClick={() => void send()}
                  disabled={noKey}
                  aria-disabled={!input.trim() || noKey}
                  title={input.trim() ? 'Send' : 'Type a message to send'}
                  data-testid="chat-send"
                >
                  <Send className="size-3.5" />
                </Button>
              )}
            </>
          }
        />
      </footer>
    </div>
  )
}


