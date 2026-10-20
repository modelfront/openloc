// The assistant asked for a screenshot (ask_for_screenshot). The user takes
// it, sees it, and sends or skips it; nothing is captured or sent before
// their click. The answer goes back as their next message; when no answer
// comes back, the card offers Send again with the same upload.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Camera } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { uploadChatFile } from '../../api'
import { capturePage } from '../../lib/screenshot'
import { errorText } from '../../lib/error-text'
import type { ChatSend } from './chat-ask'
import { ChatAskCard, WAIT_FOR_ANSWER } from './ChatCard'

/** The messages the panel sends for the user's answer. */
const SCREENSHOT_SENT = 'Here is the screenshot.'
const SCREENSHOT_SKIPPED = "I'll skip the screenshot."

type State = 'idle' | 'capturing' | 'review' | 'sending' | 'sent' | 'skipped'

const STATUS: Partial<Record<State, string>> = { sent: 'Screenshot sent.', skipped: 'Skipped.' }

export function ChatScreenshotRequest({ reason, disabled, onSend }: { reason: string; disabled?: boolean; onSend: ChatSend }) {
  const [state, setState] = useState<State>('idle')
  const stateRef = useRef(state)
  useEffect(() => { stateRef.current = state }, [state])
  const [file, setFile] = useState<File | null>(null)
  // Kept so a send that got no answer goes again without a second upload.
  const uploadId = useRef<string | undefined>(undefined)
  const preview = useMemo(() => (file ? URL.createObjectURL(file) : undefined), [file])
  useEffect(() => () => { if (preview) URL.revokeObjectURL(preview) }, [preview])

  const capture = async () => {
    setState('capturing')
    try {
      const shot = await capturePage()
      // The user may have skipped while the picture was being taken.
      if (stateRef.current !== 'capturing') return
      setFile(shot)
      setState('review')
    } catch (err) {
      if (stateRef.current !== 'capturing') return
      // The rendering library can reject with a DOM event, which has no message.
      toast.error(`Could not take the screenshot: ${errorText(err, 'the browser could not render the page.')}`)
      setState('idle')
    }
  }
  const send = async () => {
    if (!file) return
    setState('sending')
    try {
      uploadId.current ??= (await uploadChatFile(file)).id
    } catch (err) {
      toast.error(errorText(err, 'The screenshot could not be uploaded.'))
      setState('review')
      return
    }
    setState('sent')
    if (!(await onSend(SCREENSHOT_SENT, { screenshotUploadId: uploadId.current }))) setState('review')
  }
  const skip = () => {
    void onSend(SCREENSHOT_SKIPPED)
    setState('skipped')
  }
  const waitReason = disabled ? WAIT_FOR_ANSWER : undefined

  return (
    <ChatAskCard reason={reason} state={state} status={STATUS[state]} testId="chat-ask-screenshot">
      {preview && (
        <img src={preview} alt="Screenshot of the page" className="max-h-40 rounded border border-border"
          data-testid="chat-screenshot-preview" />
      )}
      {(state === 'idle' || state === 'capturing') && (
        <div className="flex gap-2">
          <Button size="xs" onClick={() => void capture()} loading={state === 'capturing'} disabled={disabled}
            disabledReason={waitReason} data-testid="chat-screenshot-take">
            <Camera />Add screenshot
          </Button>
          <Button size="xs" variant="ghost" onClick={skip} disabled={disabled} disabledReason={waitReason}
            data-testid="chat-screenshot-skip">
            Skip
          </Button>
        </div>
      )}
      {(state === 'review' || state === 'sending') && (
        <div className="flex gap-2">
          <Button size="xs" onClick={() => void send()} loading={state === 'sending'} disabled={disabled}
            disabledReason={waitReason} data-testid="chat-screenshot-send">
            {uploadId.current ? 'Send again' : 'Send'}
          </Button>
          <Button size="xs" variant="ghost" onClick={skip} disabled={disabled || state === 'sending'}
            disabledReason={waitReason ?? (state === 'sending' ? 'The screenshot is being sent.' : undefined)}
            data-testid="chat-screenshot-discard">
            Skip
          </Button>
        </div>
      )}
    </ChatAskCard>
  )
}
