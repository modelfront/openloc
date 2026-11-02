// Files attached to the next message. The file uploads
// right away; the message then carries one note per file with its upload_id,
// which the import tools take.
import { useCallback, useRef, useState } from 'react'
import { Paperclip, X } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { uploadChatFile, type ChatUpload } from '../../api'

/** Files one message carries; the batch attach tool takes the same number. */
export const MAX_CHAT_ATTACHMENTS = 10

/**
 * How long one file may take to upload. The message box waits for uploads,
 * so a stuck one must end; 5 minutes carries a 50 MB file at about 1.5 Mbit/s.
 */
export const CHAT_UPLOAD_TIMEOUT_MS = 5 * 60_000

export interface ChatUploadProgress {
  current: number
  total: number
}

/**
 * Uploads picked or dropped files one at a time. `attached` is how many the
 * message already has: files past the limit are left out with a toast, and a
 * file that fails says so in its own toast while the others go on.
 */
export function useChatUploads(attached: number, onAdded: (u: ChatUpload) => void) {
  const [progress, setProgress] = useState<ChatUploadProgress | null>(null)
  const busy = useRef(false)
  const add = useCallback(async (files: File[]) => {
    if (files.length === 0) return
    if (busy.current) {
      toast.error('Wait until the files being uploaded are done')
      return
    }
    const room = Math.max(MAX_CHAT_ATTACHMENTS - attached, 0)
    if (files.length > room) toast.error(`Up to ${MAX_CHAT_ATTACHMENTS} files per message`)
    const batch = files.slice(0, room)
    if (batch.length === 0) return
    busy.current = true
    try {
      for (const [i, file] of batch.entries()) {
        setProgress({ current: i + 1, total: batch.length })
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), CHAT_UPLOAD_TIMEOUT_MS)
        try {
          onAdded(await uploadChatFile(file, controller.signal))
        } catch (err) {
          const reason = controller.signal.aborted ? 'The upload took too long. Try again.' : (err as Error).message
          toast.error(`${file.name}: ${reason}`)
        } finally {
          clearTimeout(timer)
        }
      }
    } finally {
      busy.current = false
      setProgress(null)
    }
  }, [attached, onAdded])
  return { add, progress }
}

export function ChatAttachButton({ onPick, progress, full, disabled }: {
  onPick: (files: File[]) => void
  progress: ChatUploadProgress | null
  /** The message has as many files as it can take. */
  full: boolean
  disabled?: boolean
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const title = progress
    ? `Uploading ${progress.current} of ${progress.total}…`
    : full
      ? `Up to ${MAX_CHAT_ATTACHMENTS} files per message`
      : 'Attach a file (TMX, terms, a document to translate)'
  return (
    <>
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        data-testid="chat-attach-input"
        onChange={e => {
          onPick(Array.from(e.target.files ?? []))
          e.target.value = ''
        }}
      />
      <Button
        variant="ghost"
        size="icon-sm"
        color="muted"
        onClick={() => inputRef.current?.click()}
        disabled={disabled || full}
        loading={progress !== null}
        title={title}
        data-testid="chat-attach"
      >
        <Paperclip className="size-3.5" />
      </Button>
    </>
  )
}

export function ChatAttachmentChips({ attachments, onRemove }: { attachments: ChatUpload[]; onRemove: (id: string) => void }) {
  if (attachments.length === 0) return null
  return (
    <div className="mb-1.5 flex flex-wrap gap-1" data-testid="chat-attachments">
      {attachments.map(a => (
        <span key={a.id} className="inline-flex items-center gap-0.5">
          <Chip color="grey" icon={<Paperclip />}>{a.filename}</Chip>
          <Button variant="ghost" size="icon-xs" onClick={() => onRemove(a.id)} title={`Remove ${a.filename}`}>
            <X className="h-3 w-3" />
          </Button>
        </span>
      ))}
    </div>
  )
}
