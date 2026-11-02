import type { Node as PMNode } from '@tiptap/pm/model'

/**
 * Plain-text offset ↔ ProseMirror position mapping.
 *
 * Everything that talks to the outside world (LanguageTool matches, Find &
 * Replace hits, terminology lookups) works in plain-text character offsets.
 * The editor works in ProseMirror positions. The two are NOT related by a
 * constant: a segment's document can hold nodes that cost a position but
 * contribute no characters.
 *
 *   node                   plain chars   PM positions
 *   ────────────────────   ───────────   ────────────
 *   text                        n             n
 *   hardBreak                   1 ('\n')      1
 *   xliffTag (inline atom)      0             1     ← the drift
 *   paragraph boundary          1 ('\n')      2     ← and here
 *
 * `pos = offset + 1` holds only for a single paragraph of tag-free text; tag
 * chips and block boundaries break it. A wrong offset misplaces a squiggle, and
 * as the input to a replacement it writes into the wrong characters.
 *
 * `BLOCK_SEPARATOR` must stay in sync with the `blockSeparator` that
 * `RichTextCellEditor` passes to `getText()` (currently '\n') —
 * that call is what produces the plain text these offsets are measured
 * against.
 */
const BLOCK_SEPARATOR = '\n'

interface Chunk {
  plainStart: number
  plainLen: number
  pmStart: number
  pmLen: number
  /** The plain text this chunk contributes (`plainLen` characters). */
  text: string
  /** Only text chunks can be indexed into; everything else is all-or-nothing. */
  isText: boolean
}

/**
 * Plain text a leaf node contributes. TipTap turns an extension's
 * `renderText()` into `spec.toText` and `getText()` applies it, so reading the
 * same field here keeps us consistent with the string LanguageTool actually
 * received (hardBreak → '\n', xliffTag → nothing).
 */
function leafText(node: PMNode, pos: number, parent: PMNode | null, index: number): string {
  const toText = node.type.spec.toText as
    | ((props: { node: PMNode; pos: number; parent: PMNode | null; index: number }) => string)
    | undefined
  if (!toText) return ''
  return toText({ node, pos, parent, index }) ?? ''
}

/**
 * Flatten the document into ordered chunks carrying both coordinate systems.
 * Mirrors the traversal in TipTap's `getTextBetween` so the plain offsets here
 * line up with `editor.getText()` exactly.
 */
function chunksOf(doc: PMNode): Chunk[] {
  const chunks: Chunk[] = []
  let plain = 0

  doc.nodesBetween(0, doc.content.size, (node, pos, parent, index) => {
    if (node.isBlock && pos > 0) {
      // A block boundary emits the separator into the plain text but consumes
      // two positions (close + open) that belong to no chunk — anchoring a
      // zero-width chunk at `pos` keeps offsets inside the separator from
      // resolving into the middle of that structural gap.
      chunks.push({
        plainStart: plain, plainLen: BLOCK_SEPARATOR.length,
        pmStart: pos, pmLen: 0, text: BLOCK_SEPARATOR, isText: false,
      })
      plain += BLOCK_SEPARATOR.length
    }

    if (node.isText) {
      const text = node.text ?? ''
      const len = text.length
      chunks.push({ plainStart: plain, plainLen: len, pmStart: pos, pmLen: len, text, isText: true })
      plain += len
      return false
    }

    if (node.isLeaf) {
      const t = leafText(node, pos, parent, index)
      chunks.push({
        plainStart: plain, plainLen: t.length,
        pmStart: pos, pmLen: node.nodeSize, text: t, isText: false,
      })
      plain += t.length
      return false
    }

    return true
  })

  return chunks
}

/** Position just past the last chunk that occupies any space. */
function endPosition(doc: PMNode, chunks: Chunk[]): number {
  for (let i = chunks.length - 1; i >= 0; i--) {
    if (chunks[i].pmLen > 0) return chunks[i].pmStart + chunks[i].pmLen
  }
  return Math.min(1, doc.content.size)
}

/**
 * The plain text the offsets here are measured against — the same string as
 * `editor.getText({ blockSeparator: '\n' })`. Send this, not `doc.textContent`
 * (which drops hardBreaks and block boundaries), to anything whose offsets come
 * back through `plainOffsetToPm`.
 */
export function pmPlainText(doc: PMNode): string {
  return chunksOf(doc).map(c => c.text).join('')
}

/**
 * Plain-text offset → ProseMirror position. Offsets past the end clamp to the
 * end of the content rather than throwing — callers are mapping third-party
 * offsets (LanguageTool) against text that may have changed underneath them.
 */
export function plainOffsetToPm(doc: PMNode, offset: number): number {
  if (offset <= 0) {
    const first = chunksOf(doc).find(c => c.isText || c.plainLen > 0)
    return first ? first.pmStart : Math.min(1, doc.content.size)
  }
  const chunks = chunksOf(doc)
  for (const chunk of chunks) {
    if (offset < chunk.plainStart + chunk.plainLen) {
      return chunk.isText ? chunk.pmStart + (offset - chunk.plainStart) : chunk.pmStart
    }
  }
  return endPosition(doc, chunks)
}

/**
 * Caret stop → ProseMirror position. Caret stops count what an arrow key
 * steps over: a character, a line break, a tag chip (one stop, though it adds
 * no plain text). A click on a closed segment's text is measured this way
 * (`caretOffsetAt` in lib/source-caret.ts), so the caret can tell the two
 * sides of a chip apart. Past the end clamps to the end.
 */
export function caretOffsetToPm(doc: PMNode, offset: number): number {
  const chunks = chunksOf(doc)
  let stop = 0
  for (const chunk of chunks) {
    const width = chunk.isText || chunk.plainLen > 0 ? chunk.plainLen : Math.min(chunk.pmLen, 1)
    if (offset < stop + width) {
      if (chunk.isText) return chunk.pmStart + Math.max(0, offset - stop)
      // A block boundary has no position of its own: its stop is the end of
      // the block before it.
      return chunk.pmLen === 0 ? chunk.pmStart - 1 : chunk.pmStart
    }
    stop += width
  }
  return endPosition(doc, chunks)
}

/** ProseMirror position → plain-text offset. Inverse of `plainOffsetToPm`. */
export function pmToPlainOffset(doc: PMNode, pos: number): number {
  const chunks = chunksOf(doc)
  let plainEnd = 0
  for (const chunk of chunks) {
    if (pos <= chunk.pmStart) return chunk.plainStart
    if (chunk.isText && pos < chunk.pmStart + chunk.pmLen) {
      return chunk.plainStart + (pos - chunk.pmStart)
    }
    plainEnd = chunk.plainStart + chunk.plainLen
  }
  return plainEnd
}

/**
 * Apply an edit expressed in plain-text offsets to a plain string. Trivial on
 * its own, but named alongside the offset helpers so call sites don't hand-roll
 * slicing. Rich-text edits deliberately do NOT live here — `active_text` and
 * `active_rich_text` don't agree on plain-text length, so rich edits are
 * anchored on the word occurrence instead (see lib/spellcheck-apply.ts).
 */
export function applyPlainEdit(
  text: string,
  offset: number,
  length: number,
  replacement: string,
): string | null {
  if (offset < 0 || length < 0 || offset + length > text.length) return null
  return text.slice(0, offset) + replacement + text.slice(offset + length)
}
