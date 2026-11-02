import { Extension } from '@tiptap/core'
import type { Node } from '@tiptap/pm/model'
import { Selection } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { isLineCaret, type EntryCaret } from './entry-caret'
import { caretHead, caretX, posOnAdjacentLine, posOnLine, type LineCaret } from './line-caret'

interface Options {
  /** Leaves the segment past its last (`next`) or first (`prev`) line; the
   *  goal column is `caret.x`. */
  onLeave: (dir: 'next' | 'prev', caret: LineCaret) => void
  /** The caret the editor was entered with. */
  entryCaret: () => EntryCaret | undefined
}

/**
 * Up and Down by line with a goal column, as in an IDE: a run of presses
 * keeps the column it started in, sits at the end of a shorter line on the
 * way, and carries the column into the next or previous segment past the
 * last or first line. The goal lasts while the caret and the text are where
 * the last press left them; a click, typing or another key starts a new one.
 * Shift+Up/Down (and any selection) stay with the browser.
 */
export function createLineMoves({ onLeave, entryCaret }: Options) {
  let goal: { x: number; head: number; doc: Node } | null = null

  const move = (view: EditorView, dir: 'up' | 'down'): boolean => {
    const head = caretHead(view)
    if (head === null) return false
    const { doc } = view.state
    let x: number
    if (goal && goal.head === head && goal.doc === doc) {
      x = goal.x
    } else {
      const entry = entryCaret()
      x = entry && isLineCaret(entry) && !goal && head === posOnLine(view, entry) ? entry.x : caretX(view, head)
    }
    const target = posOnAdjacentLine(view, head, dir, x)
    if (target === null) {
      goal = null
      onLeave(dir === 'down' ? 'next' : 'prev', { line: dir === 'down' ? 'first' : 'last', x })
      return true
    }
    view.dispatch(view.state.tr.setSelection(Selection.near(doc.resolve(target))).scrollIntoView())
    goal = { x, head: target, doc }
    return true
  }

  return Extension.create({
    name: 'lineMoves',
    addKeyboardShortcuts() {
      return {
        ArrowDown: ({ editor }) => move(editor.view, 'down'),
        ArrowUp: ({ editor }) => move(editor.view, 'up'),
      }
    },
  })
}
