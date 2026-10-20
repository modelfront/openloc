import { Node, mergeAttributes, InputRule } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import type { Transaction, EditorState } from '@tiptap/pm/state'
import { NodeSelection } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { chipLabel, chipTitle, toLabelAttrs, type TagLabelMode, type RefResolver } from './xliff-tag-label'

/** What the chips need in order to render: how to label, and how to resolve a
 *  cross-reference to its term. Both are display-only. */
interface ChipRenderState {
  mode: TagLabelMode
  resolveRef?: RefResolver
}

const tagLabelPluginKey = new PluginKey<ChipRenderState>('xliffTagLabelMode')

/** The render state carried by a node's decorations, else the configured default. */
function renderStateOf(decorations: readonly Decoration[], fallback: ChipRenderState): ChipRenderState {
  for (const d of decorations) {
    const st = (d.spec as { chipState?: ChipRenderState } | undefined)?.chipState
    if (st) return st
  }
  return fallback
}

/**
 * Custom TipTap node for XLIFF inline tags (bpt, ept, ph, x, g, it).
 * `atom: true` — treated as a single unit; arrow keys skip over it.
 * `draggable: true` — allows repositioning by drag.
 *
 * Includes a keydown plugin that intercepts ArrowLeft to create a NodeSelection
 * when the cursor is about to move into an atom node. Without this, the browser
 * skips over the atom in the native selection (no visual feedback). ArrowRight
 * already triggers NodeSelection natively in ProseMirror.
 *
 * When a tag is selected (NodeSelection), the .ProseMirror-selectednode CSS
 * class provides a visible outline so the user sees which tag is selected.
 *
 * ## Labels are rendered, never stored
 *
 * The visible text comes from `chipLabel(attrs, mode)` at render time, where
 * mode follows the editor's `¶` toggle. It is deliberately NOT a node
 * attribute: rewriting attrs on every toggle would dirty the document and
 * pollute undo history, which can trigger a save. Instead a
 * plugin holds the mode and emits node decorations, and the node view
 * re-renders when those change — no document mutation at all.
 */
export const XliffTag = Node.create({
  name: 'xliffTag',
  group: 'inline',
  inline: true,
  atom: true,
  draggable: true,

  addOptions() {
    return {
      labelMode: 'numeric' as TagLabelMode,
      resolveRef: undefined as RefResolver | undefined,
    }
  },

  addAttributes() {
    return {
      tagType: { default: 'ph' },
      tagId: { default: '' },
      // What the element originally was (OASIS ctype). Drives the full-tags
      // label and the hover tooltip, and forms part of the chip's identity.
      ctype: { default: '' },
      // Fallback ordinal for chips whose markup carries no id.
      tagNum: { default: undefined },
      // True for the closing half of a paired tag.
      closing: { default: false },
      // The source element's start tag, for the hover tooltip.
      originalMarkup: { default: '' },
      // Segment key of the node a cross-reference renders.
      // Must be declared here or ProseMirror drops it: the tokenizer hands the
      // attr to the node spec and anything undeclared silently disappears, so
      // the chip loses its resolver input and falls back to its number the
      // moment the row is activated.
      refKey: { default: '' },
      rawXml: { default: '' },
    }
  },

  renderHTML({ HTMLAttributes, node }) {
    const attrs = toLabelAttrs(node.attrs)
    return ['span', mergeAttributes(HTMLAttributes, {
      class: 'xliff-tag',
      'data-tag-type': attrs.tagType,
      'data-tag-id': attrs.tagId ?? '',
      'data-ctype': attrs.ctype ?? '',
      ...(attrs.refKey ? { 'data-ref-key': attrs.refKey } : {}),
      title: chipTitle(attrs),
      contenteditable: 'false',
      // Static fallback for serialization / copy-paste. The node view
      // overrides this with the mode-aware label while editing.
    }), chipLabel(attrs, 'numeric')]
  },

  parseHTML() {
    return [{
      tag: 'span.xliff-tag',
      getAttrs: (el) => {
        if (typeof el === 'string') return false
        return {
          tagType: el.getAttribute('data-tag-type') || 'ph',
          tagId: el.getAttribute('data-tag-id') || '',
          // Read identity from data attributes, never from the rendered
          // text — that text now varies with the label mode.
          ctype: el.getAttribute('data-ctype') || '',
          originalMarkup: el.getAttribute('data-original-markup') || '',
          refKey: el.getAttribute('data-ref-key') || '',
          closing: el.getAttribute('data-closing') === 'true',
          rawXml: el.getAttribute('data-raw-xml') || '',
        }
      },
    }]
  },

  addNodeView() {
    const configured: ChipRenderState = {
      mode: this.options.labelMode,
      resolveRef: this.options.resolveRef,
    }
    return ({ node, HTMLAttributes, decorations }) => {
      const dom = document.createElement('span')
      const attrs = toLabelAttrs(node.attrs)

      dom.className = 'xliff-tag'
      dom.setAttribute('data-tag-type', attrs.tagType)
      dom.setAttribute('data-tag-id', attrs.tagId ?? '')
      dom.setAttribute('data-ctype', attrs.ctype ?? '')
      dom.setAttribute('data-closing', String(Boolean(attrs.closing)))
      if (attrs.originalMarkup) {
        dom.setAttribute('data-original-markup', attrs.originalMarkup)
      }
      if (attrs.refKey) {
        dom.setAttribute('data-ref-key', attrs.refKey)
      }
      if (HTMLAttributes['data-raw-xml']) {
        dom.setAttribute('data-raw-xml', String(HTMLAttributes['data-raw-xml']))
      }
      dom.setAttribute('contenteditable', 'false')
      dom.title = chipTitle(attrs)

      // The decoration carries the current mode. ProseMirror re-runs update()
      // whenever a node's decorations change, which is what makes the toggle
      // reach an atom node without touching the document.
      const render = (st: ChipRenderState) => {
        dom.textContent = chipLabel(attrs, st.mode, st.resolveRef)
        dom.setAttribute('data-label-mode', st.mode)
      }
      // Read the mode we were HANDED rather than assuming numeric. A chip
      // created while the toggle is already on — open a segment after
      // switching it on — gets its decorations at construction and never
      // receives an update(), so hardcoding a default here left the editor
      // showing <1> next to a source column showing <sub>.
      render(renderStateOf(decorations, configured))

      return {
        dom,
        update: (updated, decos) => {
          if (updated.type.name !== 'xliffTag') return false
          if (updated.attrs.tagId !== node.attrs.tagId) return false
          if (updated.attrs.ctype !== node.attrs.ctype) return false

          render(renderStateOf(decos, configured))
          return true
        },
        ignoreMutation: () => true,
      }
    }
  },

  // Convert typed self-closing XLIFF inline tags to chip nodes the
  // moment the user finishes typing the closing `/>`. Without this, `<ph id="1"/>`
  // sits as plain text — not highlighted, not atomic, and QA compares it
  // against source's chip-form tags as if it were a tag-less translation.
  //
  // Scope: self-closing forms only — `<ph id="X" .../>` and `<x id="X" .../>`.
  // Paired forms (`<bpt>...</bpt>`, `<g>...</g>`) are not converted — they are
  // structural and not typed by hand.
  //
  // Why a hand-rolled `InputRule` instead of `nodeInputRule`: TipTap's
  // `nodeInputRule` reinterprets `match[1]` as the inner range to replace
  // (intended for markup like `**bold**` where capture[1] is the content).
  // For our case the WHOLE match (the literal `<ph .../>`) must be replaced
  // with the atom node, so we drive the transaction directly.
  addInputRules() {
    const type = this.type
    return [
      // Self-closing: `<ph id="X"/>`, `<x id="X"/>`.
      new InputRule({
        find: /<(ph|x)\s+id="([^"]+)"[^>]*\/>$/,
        handler: ({ state, range, match }) => {
          const tagType = match[1]
          const tagId = match[2]
          const node = type.create({
            tagType,
            tagId,
            ctype: /ctype="([^"]*)"/.exec(match[0])?.[1] ?? '',
            rawXml: match[0],
          })
          state.tr.replaceWith(range.from, range.to, node)
        },
      }),
      // Paired bpt: `<bpt id="X">…</bpt>`. Display `<X>` per the
      // import-side convention in `xliff-tiptap.ts`. `[\s\S]*?` instead of
      // `.*?` so newlines inside the pair (rare but possible from copy-paste)
      // also match.
      new InputRule({
        find: /<bpt\s+id="([^"]+)"[^>]*>[\s\S]*?<\/bpt>$/,
        handler: ({ state, range, match }) => {
          const tagId = match[1]
          const node = type.create({
            tagType: 'bpt',
            tagId,
            rawXml: match[0],
          })
          state.tr.replaceWith(range.from, range.to, node)
        },
      }),
      // Paired ept: `<ept id="X">…</ept>`. Display `</X>`.
      new InputRule({
        find: /<ept\s+id="([^"]+)"[^>]*>[\s\S]*?<\/ept>$/,
        handler: ({ state, range, match }) => {
          const tagId = match[1]
          const node = type.create({
            tagType: 'ept',
            tagId,
            closing: true,
            rawXml: match[0],
          })
          state.tr.replaceWith(range.from, range.to, node)
        },
      }),
    ]
  },

  addProseMirrorPlugins() {
    const initialState: ChipRenderState = {
      mode: this.options.labelMode,
      resolveRef: this.options.resolveRef,
    }

    return [
      // Holds the label mode and republishes it as node decorations. Same
      // shape as the whitespace-marker plugin in tiptap-whitespace.ts:
      // transaction meta, so the toggle is reactive with no stale closures
      // and — critically — no change to the document itself.
      new Plugin({
        key: tagLabelPluginKey,
        state: {
          init: () => initialState,
          apply: (tr, prev) => {
            const next = tr.getMeta(tagLabelPluginKey) as Partial<ChipRenderState> | undefined
            return next ? { ...prev, ...next } : prev
          },
        },
        props: {
          decorations(state) {
            const chipState = tagLabelPluginKey.getState(state) ?? initialState
            const decorations: Decoration[] = []
            state.doc.descendants((node, pos) => {
              if (node.type.name !== 'xliffTag') return
              decorations.push(
                Decoration.node(pos, pos + node.nodeSize, {}, { chipState })
              )
            })
            return DecorationSet.create(state.doc, decorations)
          },
        },
      }),
      new Plugin({
        props: {
          handleDOMEvents: {
            keydown(view, event) {
              if (event.key !== 'ArrowLeft') return false

              const { state } = view
              const { selection } = state
              if (!selection.empty) return false

              const from = selection.from
              const $pos = state.doc.resolve(from)
              const nodeBefore = $pos.nodeBefore

              // Direct check: node immediately before cursor is an xliffTag
              if (nodeBefore?.type.name === 'xliffTag') {
                event.preventDefault()
                const nodePos = from - nodeBefore.nodeSize
                view.dispatch(state.tr.setSelection(NodeSelection.create(state.doc, nodePos)))
                return true
              }

              // Scan check: the browser may skip over the atom when moving left,
              // landing in the text node before it. Check if there's an xliffTag
              // at from-1 or from-2 (atom node size is 1 for inline atoms).
              for (let offset = 1; offset <= 2; offset++) {
                const checkPos = from - offset
                if (checkPos < 1) break
                const node = state.doc.nodeAt(checkPos)
                if (node?.type.name === 'xliffTag') {
                  event.preventDefault()
                  view.dispatch(state.tr.setSelection(NodeSelection.create(state.doc, checkPos)))
                  return true
                }
              }

              return false
            },
          },
        },
      }),
    ]
  },
})

/**
 * Switch how inline tag chips are labelled — `<1>` / `{1}` when 'numeric',
 * `<sub>` / `{br}` when 'full'. Mirrors `toggleWhitespace` in
 * tiptap-whitespace.ts; both are driven by the editor's `¶` toggle.
 *
 * Dispatches plugin meta only. The document is untouched, so this never marks
 * the segment dirty or adds an undo step.
 */
export function setTagLabelMode(
  editor: { view: { dispatch: (tr: Transaction) => void; state: EditorState } },
  mode: TagLabelMode
) {
  editor.view.dispatch(editor.view.state.tr.setMeta(tagLabelPluginKey, { mode }))
}

/**
 * Update how cross-reference chips resolve to their term, so the
 * editor shows "{Feucht}" as soon as the defining segment is translated.
 * Plugin meta only — the document is untouched.
 */
export function setChipRefResolver(
  editor: { view: { dispatch: (tr: Transaction) => void; state: EditorState } },
  resolveRef: RefResolver | undefined
) {
  editor.view.dispatch(editor.view.state.tr.setMeta(tagLabelPluginKey, { resolveRef }))
}
