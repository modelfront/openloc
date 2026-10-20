import { toast } from 'sonner'

/** What the server accepts on one report or one reply. */
export const MAX_ATTACHMENTS = 10
const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024

/**
 * The files of `current` plus those of `added` that fit the limits. A file
 * that does not fit is left out with a toast saying why.
 */
export function addAttachments(current: File[], added: File[]): File[] {
  const out = [...current]
  for (const f of added) {
    if (out.length >= MAX_ATTACHMENTS) {
      toast.error(`Attach at most ${MAX_ATTACHMENTS} files.`)
      break
    }
    if (f.size > MAX_ATTACHMENT_BYTES) {
      toast.error(`${f.name} is too large (max 25 MB).`)
      continue
    }
    out.push(f)
  }
  return out
}

/**
 * Images shown inside the text. The text keeps `![name](upload:N)` where an
 * image goes, N being the image's place in the form's files; the server
 * stores the files and turns each into `![name](attachment:ID)`.
 */
const UPLOAD_REF = /!\[([^\]\n]{0,255})\]\(upload:(\d{1,3})\)/g
export const ATTACHMENT_REF = /!\[([^\]\n]{0,255})\]\(attachment:(\d{1,19})\)/g

/** The types the server shows as images; any other file stays in the list. */
const INLINE_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])
export const isInlineImage = (f: File) => INLINE_IMAGE_TYPES.has(f.type)

/** The reference for file `index`, its name made safe for the brackets. */
export function uploadRef(name: string, index: number): string {
  return `![${name.replace(/[[\]\n\r]/g, ' ').slice(0, 255)}](upload:${index})`
}

/**
 * `text` with references to `images` put at `at`, each on its own line, and
 * the cursor just after them.
 */
export function insertUploadRefs(text: string, at: number, images: { name: string; index: number }[]) {
  if (images.length === 0) return { text, cursor: at }
  const before = text.slice(0, at), after = text.slice(at)
  const lead = before === '' || before.endsWith('\n') ? '' : '\n'
  const trail = after === '' || after.startsWith('\n') ? '' : '\n'
  const refs = lead + images.map(i => uploadRef(i.name, i.index)).join('\n') + trail
  return { text: before + refs + after, cursor: before.length + refs.length - trail.length }
}

/**
 * `text` once file `removed` leaves the form: its references go, and those of
 * the files after it move down one place.
 */
export function dropUploadRef(text: string, removed: number): string {
  return text.replace(UPLOAD_REF, (ref, name: string, n: string) => {
    const i = Number(n)
    if (i === removed) return ''
    return i > removed ? uploadRef(name, i - 1) : ref
  })
}
