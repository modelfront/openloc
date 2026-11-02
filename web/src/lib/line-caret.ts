import { Selection } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'

/** Where the caret enters a segment from the one above or below it: its
 *  first or last line, at a horizontal place in the viewport. */
export interface LineCaret {
  line: 'first' | 'last'
  x: number
}

type Box = { top: number; bottom: number; left: number }

/** Whether two boxes sit on the same line of text. */
function sameLine(a: Box, b: Box): boolean {
  return Math.abs((a.top + a.bottom) / 2 - (b.top + b.bottom) / 2) <= Math.max(1, (b.bottom - b.top) / 2)
}

/** The document's first or last caret position. */
function edgePos(view: EditorView, line: LineCaret['line']): number {
  const { doc } = view.state
  return line === 'first' ? Selection.atStart(doc).from : Selection.atEnd(doc).to
}

/**
 * The caret position when nothing is selected; null otherwise. Reads the DOM
 * selection, which is what the user sees: right after a click, the editor
 * state can still hold the caret from before it.
 */
export function caretHead(view: EditorView): number | null {
  const sel = view.dom.ownerDocument.getSelection()
  if (!sel?.isCollapsed || !sel.focusNode || !view.dom.contains(sel.focusNode)) return null
  try {
    return view.posAtDOM(sel.focusNode, sel.focusOffset)
  } catch {
    return null
  }
}

/** The caret's horizontal place in the viewport at `pos`. */
export function caretX(view: EditorView, pos: number): number {
  return view.coordsAtPos(pos).left
}

/**
 * The position closest to `x` on the line below (`down`) or above (`up`) the
 * one `head` is on; null when that line is the document's last (first), so
 * the caret has nowhere to go inside it.
 */
export function posOnAdjacentLine(view: EditorView, head: number, dir: 'up' | 'down', x: number): number | null {
  const start = edgePos(view, 'first')
  const end = edgePos(view, 'last')
  const step = dir === 'down' ? 1 : -1
  const here = view.coordsAtPos(head)
  let line: Box | null = null
  let best: number | null = null
  let bestDist = Infinity
  for (let pos = head + step; pos >= start && pos <= end; pos += step) {
    const c = view.coordsAtPos(pos)
    if (!line) {
      if (sameLine(c, here)) continue
      line = c
    } else if (!sameLine(c, line)) {
      break
    }
    const dist = Math.abs(c.left - x)
    if (dist < bestDist) {
      bestDist = dist
      best = pos
    }
  }
  return best
}

/** The position on the document's first or last line closest to `x`. */
export function posOnLine(view: EditorView, { line, x }: LineCaret): number {
  const start = edgePos(view, 'first')
  const end = edgePos(view, 'last')
  const from = line === 'first' ? start : end
  const step = line === 'first' ? 1 : -1
  const edge = view.coordsAtPos(from)
  let best = from
  let bestDist = Infinity
  for (let pos = from; pos >= start && pos <= end; pos += step) {
    const c = view.coordsAtPos(pos)
    if (!sameLine(c, edge)) break
    const dist = Math.abs(c.left - x)
    if (dist < bestDist) {
      bestDist = dist
      best = pos
    }
  }
  return best
}
