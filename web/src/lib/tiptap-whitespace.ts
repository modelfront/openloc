import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet, type EditorView } from '@tiptap/pm/view'

const wsPluginKey = new PluginKey('whitespaceMarkers')

// Map of characters to their CSS class. Must match the classes in index.css
// and the WS_MARKERS map in CATEditor.tsx WhitespaceText component.
const CHAR_CLASS: Record<string, string> = {
  ' ':      'ws-dot',   // regular space
  '\t':     'ws-tab',   // tab
  '\u00A0': 'ws-nbsp',  // non-breaking space
  '\u200B': 'ws-zw',    // zero-width space
  '\u200C': 'ws-zw',    // zero-width non-joiner
  '\u200D': 'ws-zw',    // zero-width joiner
  '\u2002': 'ws-dot',   // en space
  '\u2003': 'ws-dot',   // em space
  '\u2009': 'ws-dot',   // thin space
  '\u200A': 'ws-dot',   // hair space
  '\u3000': 'ws-dot',   // ideographic space
  '\u00AD': 'ws-shy',   // soft hyphen (¬ traditional symbol)
  '\uFEFF': 'ws-zw',    // BOM / zero-width no-break space
  '\u205F': 'ws-dot',   // medium mathematical space
  '\u2007': 'ws-dot',   // figure space
  '\u2004': 'ws-dot',   // three-per-em space
  '\u2005': 'ws-dot',   // four-per-em space
  '\u2006': 'ws-dot',   // six-per-em space
  '\u2008': 'ws-dot',   // punctuation space
  '\u2028': 'ws-zw',    // line separator
  '\u2029': 'ws-zw',    // paragraph separator
  '\u180E': 'ws-zw',    // Mongolian vowel separator
  '\u000C': 'ws-zw',    // form feed
}

/**
 * TipTap extension that shows whitespace and invisible characters
 * as visible overlays in the editor. Uses the same CSS classes as
 * WhitespaceText component — proven to not break layout.
 *
 * Toggled via ProseMirror transaction meta (reactive, no stale closures).
 */
export const WhitespaceMarkers = Extension.create({
  name: 'whitespaceMarkers',

  addOptions() {
    return { enabled: false }
  },

  addProseMirrorPlugins() {
    const initialEnabled = this.options.enabled

    return [
      new Plugin({
        key: wsPluginKey,
        state: {
          init: () => initialEnabled,
          apply: (tr, prev) => {
            const toggled = tr.getMeta(wsPluginKey)
            return toggled !== undefined ? toggled : prev
          },
        },
        props: {
          decorations(state) {
            const enabled = wsPluginKey.getState(state)
            if (!enabled) return DecorationSet.empty

            const decorations: Decoration[] = []
            const docSize = state.doc.content.size

            state.doc.descendants((node, pos) => {
              // Inline whitespace chars (space, tab, NBSP, ZW, soft hyphen…)
              if (node.isText && node.text) {
                for (let i = 0; i < node.text.length; i++) {
                  const cls = CHAR_CLASS[node.text[i]]
                  if (cls) {
                    decorations.push(
                      Decoration.inline(pos + i, pos + i + 1, { class: cls })
                    )
                  }
                }
                return
              }

              // Paragraph break (Enter). In ProseMirror, Enter splits
              // the doc into multiple paragraph nodes — there's no \n char to
              // decorate inline. We render ¶ as a widget at the end of every
              // paragraph except the last (matching the read-only WhitespaceText
              // rule that puts ¶ before each \n but not after the final char).
              if (node.type.name === 'paragraph') {
                const isLast = pos + node.nodeSize >= docSize
                if (!isLast) {
                  decorations.push(
                    Decoration.widget(
                      pos + node.nodeSize - 1,
                      () => {
                        const el = document.createElement('span')
                        el.className = 'ws-pilcrow'
                        el.textContent = '¶'
                        el.setAttribute('aria-hidden', 'true')
                        return el
                      },
                      { side: 1, key: `pilcrow-${pos}`, ignoreSelection: true }
                    )
                  )
                }
                return
              }

              // Hard line break (Shift+Enter → hardBreak node).
              // Render as ¶ (same as paragraph break) so edit mode matches
              // read-only at all times. The save path serializes hardBreak
              // to '\n', which read-only also renders as ¶ — emitting a
              // separate ↵ would create a symbol flip across the save
              // boundary.
              if (node.type.name === 'hardBreak') {
                decorations.push(
                  Decoration.widget(
                    pos,
                    () => {
                      const el = document.createElement('span')
                      el.className = 'ws-pilcrow'
                      el.textContent = '¶'
                      el.setAttribute('aria-hidden', 'true')
                      return el
                    },
                    { side: -1, key: `hardbreak-${pos}`, ignoreSelection: true }
                  )
                )
              }
            })

            return DecorationSet.create(state.doc, decorations)
          },
        },
      }),
    ]
  },
})

/** Dispatch a transaction that toggles whitespace visibility */
export function toggleWhitespace(editor: { view: EditorView }, enabled: boolean) {
  const tr = editor.view.state.tr.setMeta(wsPluginKey, enabled)
  editor.view.dispatch(tr)
}
