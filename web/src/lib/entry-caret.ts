import type { Editor } from '@tiptap/core'
import { posOnLine, type LineCaret } from './line-caret'
import { caretOffsetToPm } from './pm-offset'

/** Where a click on a closed segment's text puts the caret, in caret stops
 *  (`caretOffsetAt` in lib/source-caret.ts): `from` = `to` for a plain
 *  click, a range for a dragged selection. */
export interface TextCaret {
  from: number
  to: number
}

/** Where the caret starts when a segment opens: from the segment above or
 *  below (arrow keys), or from a click on its text. */
export type EntryCaret = LineCaret | TextCaret

export function isLineCaret(caret: EntryCaret): caret is LineCaret {
  return 'line' in caret
}

/** Focuses the editor with the caret at `caret`, or at the end without one. */
export function focusAtEntry(editor: Editor, caret: EntryCaret | undefined): void {
  if (!caret) {
    editor.commands.focus('end')
  } else if (isLineCaret(caret)) {
    editor.commands.focus(posOnLine(editor.view, caret))
  } else {
    const { doc } = editor.state
    const from = caretOffsetToPm(doc, caret.from)
    const to = caretOffsetToPm(doc, caret.to)
    // The clicked text is on screen already: no scroll.
    editor.chain().focus(from, { scrollIntoView: false }).setTextSelection({ from, to }).run()
  }
}
