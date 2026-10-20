import type { IssueAttachment, IssueScope } from '@/api'
import { IssueAttachments, IssueImage } from './IssueAttachments'
import { ATTACHMENT_REF } from './attachments'

type Part = { text: string } | { image: IssueAttachment }

/**
 * `text` cut at its image references. A reference becomes the image only when
 * it names one of `files` that is an image; any other stays text. Nothing is
 * read as markup. An image shows as a block, so the line break on each side
 * of its reference is dropped.
 */
export function splitIssueText(text: string, files: IssueAttachment[]): Part[] {
  const images = new Map(files.filter(f => f.is_image).map(f => [f.id, f]))
  const parts: Part[] = []
  let last = 0
  for (const m of text.matchAll(ATTACHMENT_REF)) {
    const image = images.get(Number(m[2]))
    if (!image) continue
    const before = text.slice(last, m.index).replace(/\n$/, '')
    if (before) parts.push({ text: before })
    parts.push({ image })
    last = m.index + m[0].length
    if (text[last] === '\n') last++
  }
  if (last < text.length) parts.push({ text: text.slice(last) })
  return parts
}

/**
 * The text of an issue or a comment with its files: each image the text
 * places shows where it is placed, and the files it does not place follow
 * below. Images load with the viewer's own session (`scope`).
 */
export function IssueText({ text, files, scope, issueId, testId }: {
  text: string | null | undefined
  files: IssueAttachment[]
  scope: IssueScope
  issueId: number
  testId?: string
}) {
  const parts = text ? splitIssueText(text, files) : []
  const placed = new Set(parts.flatMap(p => ('image' in p ? [p.image.id] : [])))
  const rest = files.filter(f => !placed.has(f.id))
  return (
    <>
      {parts.length > 0 && (
        <div className="whitespace-pre-wrap text-sm" data-testid={testId}>
          {parts.map((p, i) => ('image' in p
            ? <span key={i} className="my-1 block"><IssueImage scope={scope} issueId={issueId} file={p.image} /></span>
            : <span key={i}>{p.text}</span>))}
        </div>
      )}
      {rest.length > 0 && <IssueAttachments scope={scope} issueId={issueId} files={rest} />}
    </>
  )
}
