// The assistant asked the user to repeat the steps (ask_to_repeat). The user
// starts the recording; the panel steps aside and a banner stays on top of the
// page until they press Done, or for 3 minutes at most. The recording is held
// for this conversation's report; the assistant only hears that one is attached.
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Video } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { chipDotClass } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import { closeChat, openChat } from '../../lib/chat-bus'
import { RECORDING_MAX_MS, clearHeldRecording, startRecording } from '../../lib/watch-recording'
import type { ChatSend } from './chat-ask'
import { ChatAskCard, WAIT_FOR_ANSWER } from './ChatCard'

/** The messages the panel sends for the user's answer: never the recording itself. */
export const RECORDING_ATTACHED = 'I repeated the steps. The recording is attached to the report for the support team.'
const RECORDING_SKIPPED = "I'll skip the recording."

type State = 'idle' | 'recording' | 'recorded' | 'skipped'

const STATUS: Partial<Record<State, string>> = {
  recording: 'Recording…',
  recorded: 'Recorded. It goes with the report to the support team.',
  skipped: 'Skipped.',
}

const clock = (ms: number) => {
  const s = Math.max(0, Math.ceil(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

export function ChatRepeatRequest({ reason, disabled, onSend }: { reason: string; disabled?: boolean; onSend: ChatSend }) {
  const [state, setState] = useState<State>('idle')
  const [left, setLeft] = useState(RECORDING_MAX_MS)
  const stopRef = useRef<(() => void) | null>(null)
  const closedRef = useRef(false)

  // The card goes away with its conversation (the panel closed, another
  // conversation opened): a recording in progress stops, nothing is sent, and
  // no report takes it. Set on every mount, since React may mount a component
  // twice in development.
  useEffect(() => {
    closedRef.current = false
    return () => {
      closedRef.current = true
      if (stopRef.current) {
        stopRef.current()
        clearHeldRecording()
      }
    }
  }, [])

  useEffect(() => {
    if (state !== 'recording') return
    const end = Date.now() + RECORDING_MAX_MS
    const tick = setInterval(() => setLeft(end - Date.now()), 1000)
    return () => clearInterval(tick)
  }, [state])

  const start = () => {
    setLeft(RECORDING_MAX_MS)
    setState('recording')
    closeChat()
    stopRef.current = startRecording(() => {
      stopRef.current = null
      if (closedRef.current) return
      setState('recorded')
      openChat()
      void onSend(RECORDING_ATTACHED)
    })
  }
  const skip = () => {
    void onSend(RECORDING_SKIPPED)
    setState('skipped')
  }

  const waitReason = disabled ? WAIT_FOR_ANSWER : undefined

  return (
    <ChatAskCard reason={reason} state={state} status={STATUS[state]} testId="chat-ask-repeat">
      {state === 'idle' && (
        <div className="flex gap-2">
          <Button size="xs" onClick={start} disabled={disabled} disabledReason={waitReason} data-testid="chat-repeat-start">
            <Video />Start recording
          </Button>
          <Button size="xs" variant="ghost" onClick={skip} disabled={disabled} disabledReason={waitReason}
            data-testid="chat-repeat-skip">
            Skip
          </Button>
        </div>
      )}
      {state === 'recording' && createPortal(
        // Kept out of the recording and of any screenshot.
        <div role="status" data-private data-screenshot="exclude" data-testid="chat-recording-banner"
          className="fixed left-1/2 top-3 z-50 flex -translate-x-1/2 items-center gap-3 rounded-full border border-border bg-card px-4 py-2 text-sm shadow-lg">
          <span className={cn('size-2 animate-pulse rounded-full', chipDotClass('red'))} aria-hidden="true" />
          <span>Do it again — I&rsquo;ll record it for the team. 🎬</span>
          <span className="font-mono text-xs text-muted-foreground" data-testid="chat-recording-left">{clock(left)}</span>
          <Button size="xs" onClick={() => stopRef.current?.()} data-testid="chat-recording-done">Done</Button>
        </div>,
        document.body,
      )}
    </ChatAskCard>
  )
}
