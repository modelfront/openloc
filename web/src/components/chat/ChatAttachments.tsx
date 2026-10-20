// Files attached to the next message. The file uploads
// right away; the message then carries one note per file with its upload_id,
// which the import tools take.
import { useRef, useState } from 'react'
import { Paperclip, X } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { uploadChatFile, type ChatUpload } from '../../api'

export function ChatAttachButton({ onAdded, disabled }: { onAdded: (u: ChatUpload) => void; disabled?: boolean }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const onPick = async (file: File | undefined) => {
    if (!file) return
    setUploading(true)
    try {
      onAdded(await uploadChatFile(file))
    } catch (err) {
      toast.error((err as Error).message)
    } finally {
      setUploading(false)
      if (inputRef.current) inputRef.current.value = ''
    }
  }
  return (
    <>
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        data-testid="chat-attach-input"
        onChange={e => void onPick(e.target.files?.[0])}
      />
      <Button
        variant="ghost"
        size="icon-sm"
        color="muted"
        onClick={() => inputRef.current?.click()}
        disabled={disabled}
        loading={uploading}
        title="Attach a file (TMX, terms, a document to translate)"
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
