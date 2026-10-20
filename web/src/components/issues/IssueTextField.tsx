import { useRef, useState, type ClipboardEvent, type ComponentProps, type DragEvent } from 'react'
import { Textarea } from '@/components/ui/textarea'
import { AttachmentsField } from './AttachmentsField'
import { addAttachments, dropUploadRef, insertUploadRefs, isInlineImage } from './attachments'

type TextareaProps = Omit<ComponentProps<typeof Textarea>, 'value' | 'onChange' | 'onPaste' | 'onDrop' | 'onDragOver' | 'ref'>

/**
 * The text of an issue or a reply with its files. An image pasted, dropped or
 * picked goes into the text where the cursor is, as a reference the page
 * shows as the image (`IssueText`), so it sits next to the sentence it
 * explains; any other file joins the list under the field. Removing a file
 * removes its reference.
 */
export function IssueTextField({
  value, onChange, files, onFilesChange, disabled, filesTestId, hint, ...textarea
}: TextareaProps & {
  value: string
  onChange: (text: string) => void
  files: File[]
  onFilesChange: (files: File[]) => void
  filesTestId: string
  /** Small print under the files. */
  hint?: string
}) {
  const ref = useRef<HTMLTextAreaElement>(null)
  // Where the cursor was when the field lost focus: the Attach button takes
  // the focus, and its images still go where the user was typing.
  const caret = useRef<number | null>(null)
  const [dragging, setDragging] = useState(false)

  const add = (added: File[]) => {
    const kept = addAttachments(files, added)
    const el = ref.current
    const at = el && document.activeElement === el ? el.selectionStart : (caret.current ?? value.length)
    const images = kept.slice(files.length).map((f, i) => ({ file: f, index: files.length + i })).filter(x => isInlineImage(x.file))
    const { text, cursor } = insertUploadRefs(value, Math.min(at, value.length), images.map(x => ({ name: x.file.name, index: x.index })))
    onFilesChange(kept)
    if (text === value) return
    onChange(text)
    caret.current = cursor
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(cursor, cursor)
    })
  }
  const remove = (index: number) => {
    onFilesChange(files.filter((_, j) => j !== index))
    onChange(dropUploadRef(value, index))
  }
  const onPaste = (e: ClipboardEvent) => {
    const pasted = Array.from(e.clipboardData.files)
    if (pasted.length === 0) return
    e.preventDefault()
    add(pasted)
  }
  const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer.types).includes('Files')
  const onDragOver = (e: DragEvent) => {
    if (!hasFiles(e) || disabled) return
    e.preventDefault()
    setDragging(true)
  }
  const onDrop = (e: DragEvent) => {
    setDragging(false)
    if (!hasFiles(e) || disabled) return
    e.preventDefault()
    add(Array.from(e.dataTransfer.files))
  }

  return (
    <div className="space-y-2">
      <Textarea
        {...textarea}
        ref={ref}
        value={value}
        disabled={disabled}
        onChange={e => onChange(e.target.value)}
        onBlur={e => { caret.current = e.target.selectionStart; textarea.onBlur?.(e) }}
        onPaste={onPaste}
        onDragOver={onDragOver}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        dropTarget={dragging}
      />
      <div>
        <AttachmentsField value={files} onAdd={add} onRemove={remove} disabled={disabled} testId={filesTestId} />
        {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
      </div>
    </div>
  )
}
