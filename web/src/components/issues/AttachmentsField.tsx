import { useRef } from 'react'
import { Paperclip, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { formatFileSize } from '@/lib/file-size'
import { MAX_ATTACHMENTS } from './attachments'

/**
 * Files attached to a form: pick any number of files of any type. Each file
 * shows as a chip with a remove button. `onAdd` gets the picked files, to
 * check against the limits (`addAttachments`); `IssueTextField` also places
 * picked images in its text.
 */
export function AttachmentsField({
  value, onAdd, onRemove, disabled, testId = 'attachments-field',
}: {
  value: File[]
  onAdd: (added: File[]) => void
  onRemove: (index: number) => void
  disabled?: boolean
  testId?: string
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const pick = (files: FileList | null) => {
    if (files) onAdd(Array.from(files))
    if (inputRef.current) inputRef.current.value = ''
  }
  const full = value.length >= MAX_ATTACHMENTS
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5" data-testid={testId}>
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        data-testid={`${testId}-input`}
        onChange={e => pick(e.target.files)}
      />
      <Button variant="outline" size="sm" onClick={() => inputRef.current?.click()} disabled={disabled || full}
        disabledReason={full ? `At most ${MAX_ATTACHMENTS} files.` : undefined}>
        <Paperclip />
        Attach files
      </Button>
      {value.map((f, i) => (
        <span key={`${f.name}-${i}`} className="inline-flex items-center gap-0.5" data-testid={`${testId}-file`}>
          <Chip color="grey" icon={<Paperclip />} title={f.name}>
            <span className="max-w-48 truncate">{f.name}</span>
            <span className="text-muted-foreground">{formatFileSize(f.size)}</span>
          </Chip>
          <Button variant="ghost" size="icon-xs" disabled={disabled} onClick={() => onRemove(i)}
            aria-label={`Remove ${f.name}`} title={`Remove ${f.name}`}>
            <X />
          </Button>
        </span>
      ))}
    </div>
  )
}
