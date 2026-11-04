import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet, type EditorView } from '@tiptap/pm/view'
import type { Node as PMNode } from '@tiptap/pm/model'
import { plainOffsetToPm, pmPlainText } from './pm-offset'
import { trackChangeRanges } from './track-changes'

const trackChangesKey = new PluginKey<string | null>('trackChanges')

/** Turn the marks on (a baseline) or off (null) on a mounted editor. */
export function setTrackChangesBaseline(editor: { view: EditorView }, baseline: string | null) {
  editor.view.dispatch(editor.view.state.tr.setMeta(trackChangesKey, { baseline }))
}

function removedText(text: string, className: string, title: string | undefined): () => HTMLElement {
  return () => {
    const span = document.createElement('span')
    span.setAttribute('contenteditable', 'false')
    if (className) span.className = className
    if (title) span.title = title
    span.textContent = text
    return span
  }
}

function decorations(doc: PMNode, baseline: string | null): DecorationSet {
  if (!baseline) return DecorationSet.empty
  const ranges = trackChangeRanges(pmPlainText(doc), baseline)
  if (ranges.length === 0) return DecorationSet.empty
  const decos = ranges.map((r, i) => {
    const from = plainOffsetToPm(doc, r.start)
    if (r.ghost !== undefined) {
      // side -1: the removed words sit before a caret at this position, so
      // they read before the words that replaced them.
      return Decoration.widget(from, removedText(r.ghost, r.className, r.title), {
        side: -1, ignoreSelection: true, key: `${i}:${r.className}:${r.ghost}`,
      })
    }
    return Decoration.inline(from, plainOffsetToPm(doc, r.end), { class: r.className, ...(r.title ? { title: r.title } : {}) })
  })
  return DecorationSet.create(doc, decos)
}

/**
 * Track changes in the open segment: the marks the closed row shows
 * (lib/track-changes.ts), recomputed as the text changes. Added words are
 * highlighted; removed words are widgets, outside the document, so they are
 * never edited, saved, copied or spell checked.
 */
export const TrackChanges = Extension.create<{ baseline: string | null }>({
  name: 'trackChanges',

  addOptions() {
    return { baseline: null }
  },

  addProseMirrorPlugins() {
    const initial = this.options.baseline
    return [
      new Plugin<string | null>({
        key: trackChangesKey,
        state: {
          init: () => initial,
          apply: (tr, baseline) => {
            const meta = tr.getMeta(trackChangesKey) as { baseline: string | null } | undefined
            return meta ? meta.baseline : baseline
          },
        },
        props: {
          decorations(state) {
            return decorations(state.doc, trackChangesKey.getState(state) ?? null)
          },
        },
      }),
    ]
  },
})
