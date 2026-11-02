import type { TextCaret } from './entry-caret'

/**
 * Where a point in the rendered source text falls in the segment's plain
 * source, in UTF-16 code units (what a split cuts at). The rendering draws
 * things that are not source text: tag chips and diff ghosts are skipped, a
 * line break counts as "\n" and its ¶ mark does not, and a whitespace marker
 * that hides its character counts the character it names in data-plain.
 * Null when the point is outside `box`.
 */
export function sourceOffsetAt(box: HTMLElement, node: Node, offset: number): number | null {
  if (!box.contains(node)) return null
  return lengthUpTo(box, node, offset, 0)
}

/**
 * Where a point in a rendered segment falls as a caret stop: counted like
 * sourceOffsetAt, but a tag chip is one stop, as it is for the editor's caret
 * (`caretOffsetToPm` in lib/pm-offset.ts). A point inside a chip goes to the
 * chip's nearer side. Null when the point is outside `box`.
 */
export function caretOffsetAt(box: HTMLElement, node: Node, offset: number): number | null {
  if (!box.contains(node)) return null
  const chip = (node instanceof Element ? node : node.parentElement)?.closest(CHIP)
  if (chip && chip !== box && box.contains(chip) && chip.parentNode) {
    const into = chip.ownerDocument.createRange()
    into.selectNodeContents(chip)
    into.setEnd(node, offset)
    const after = into.toString().length * 2 > (chip.textContent?.length ?? 0)
    const at = Array.prototype.indexOf.call(chip.parentNode.childNodes, chip) + (after ? 1 : 0)
    return lengthUpTo(box, chip.parentNode, at, 1)
  }
  return lengthUpTo(box, node, offset, 1)
}

/**
 * The caret a click at viewport point (x, y) puts in the segment rendered in
 * `box`, in caret stops (caretOffsetAt); the selection instead when the user
 * dragged one inside `box`. Null when neither lands in `box`.
 */
export function textCaretAt(box: HTMLElement, x: number, y: number): TextCaret | null {
  const doc = box.ownerDocument
  const sel = doc.getSelection()
  if (sel && !sel.isCollapsed && sel.anchorNode && sel.focusNode) {
    const a = caretOffsetAt(box, sel.anchorNode, sel.anchorOffset)
    const f = caretOffsetAt(box, sel.focusNode, sel.focusOffset)
    if (a !== null && f !== null) return { from: Math.min(a, f), to: Math.max(a, f) }
  }
  const point = pointAt(doc, x, y)
  const at = point && caretOffsetAt(box, point.node, point.offset)
  return at == null ? null : { from: at, to: at }
}

/** The DOM point a caret would take at viewport point (x, y). */
function pointAt(doc: Document, x: number, y: number): { node: Node; offset: number } | null {
  if (typeof doc.caretPositionFromPoint === 'function') {
    const p = doc.caretPositionFromPoint(x, y)
    return p ? { node: p.offsetNode, offset: p.offset } : null
  }
  const r = typeof doc.caretRangeFromPoint === 'function' ? doc.caretRangeFromPoint(x, y) : null
  return r ? { node: r.startContainer, offset: r.startOffset } : null
}

const CHIP = '.xliff-tag'
const NOT_TEXT = '.ws-pilcrow, [data-ws-mark], [data-ghost]'

/** The length of what `box` renders before (node, offset); each chip counts `chip`. */
function lengthUpTo(box: HTMLElement, node: Node, offset: number, chip: number): number {
  const range = box.ownerDocument.createRange()
  range.selectNodeContents(box)
  range.setEnd(node, offset)
  return plainLength(range.cloneContents(), chip)
}

/** The length of renderedText with `chip` stops per tag chip (0 or 1). */
function plainLength(root: Node, chip: number): number {
  return renderedText(root, chip ? '\uFFFC' : '').length
}

/**
 * The plain source text rendered under `root` (a node or a cloned range): tag
 * chips, ¶ marks and diff ghosts left out, a line break as "\n", a whitespace
 * marker as the character it names.
 */
export function plainTextOf(root: Node): string {
  return renderedText(root, '')
}

/** What `root` renders as text, each tag chip as `chip`. A paragraph after the
 *  first adds "\n", as the boundary does for the editor (a block separator in
 *  its plain text, one caret stop in `caretOffsetToPm`). Stored formatted text
 *  lacks its outer <p> (`a</p><p>b`): the page draws the first paragraph inline
 *  and the stray `</p>` as an empty <p>, which then counts as the first. */
function renderedText(root: Node, chip: string): string {
  let s = ''
  let paragraphs = 0
  root.childNodes.forEach(child => {
    if (child instanceof Element && child.tagName === 'P' && paragraphs++ > 0) s += '\n'
    if (child.nodeType === Node.TEXT_NODE) {
      s += child.textContent ?? ''
      return
    }
    if (!(child instanceof Element) || child.matches(NOT_TEXT)) return
    if (child.matches(CHIP)) {
      s += chip
      return
    }
    if (child.tagName === 'BR') {
      s += '\n'
      return
    }
    const plain = child.getAttribute('data-plain')
    s += plain !== null ? plain : renderedText(child, chip)
  })
  return s
}

/**
 * The DOM point at `offset` in the plain source rendered in `box`: the inverse
 * of sourceOffsetAt. Null past the end.
 */
export function sourcePointAt(box: HTMLElement, offset: number): { node: Node; offset: number } | null {
  let left = offset
  const walk = (parent: Node): { node: Node; offset: number } | null => {
    for (let i = 0; i < parent.childNodes.length; i++) {
      const child = parent.childNodes[i]
      if (child.nodeType === Node.TEXT_NODE) {
        const len = child.textContent?.length ?? 0
        if (left <= len) return { node: child, offset: left }
        left -= len
        continue
      }
      if (!(child instanceof Element) || child.matches(CHIP) || child.matches(NOT_TEXT)) continue
      const plain = child.tagName === 'BR' ? '\n' : child.getAttribute('data-plain')
      if (plain === null) {
        const inside = walk(child)
        if (inside) return inside
        continue
      }
      if (left === 0) return { node: parent, offset: i }
      if (left <= plain.length) return { node: parent, offset: i + 1 }
      left -= plain.length
    }
    return null
  }
  return walk(box) ?? (left === 0 ? { node: box, offset: box.childNodes.length } : null)
}
