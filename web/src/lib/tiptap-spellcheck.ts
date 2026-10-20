import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet, type EditorView } from '@tiptap/pm/view'
import { plainOffsetToPm } from './pm-offset'
import { checkSpelling } from '../api/spellcheck'

const spellcheckKey = new PluginKey('spellcheck')

interface SpellMatch {
  message: string
  offset: number
  length: number
  replacements: { value: string }[]
}

/** One underlined error held in plugin state, in editor coordinates. */
interface SpellEntry {
  from: number
  to: number
  message: string
  suggestions: string[]
}

/** A single underlined error, resolved to live editor positions. */
export interface SpellFixRequest {
  message: string
  suggestions: string[]
  /** ProseMirror range of the flagged text, already mapped through edits. */
  from: number
  to: number
  /** The flagged text itself. */
  word: string
  /** Viewport rect of the clicked underline, for positioning a menu. */
  rect: { top: number; left: number; bottom: number; right: number }
}

/**
 * Flip spell check on an already-mounted editor.
 *
 * TipTap can't hot-swap the extension list, so `SpellCheck.configure({enabled})`
 * only takes effect at editor construction, and a segment editor is created once
 * per activation. Mutating the live extension's options makes the toggle take
 * effect on the open segment; mirrors `toggleWhitespace`.
 */
export function toggleSpellCheck(editor: { extensionManager: { extensions: { name: string; options: Record<string, unknown> }[] } }, enabled: boolean) {
  const ext = editor.extensionManager.extensions.find(e => e.name === 'spellCheck')
  if (ext) ext.options.enabled = enabled
}

/**
 * Drop every underline whose flagged text equals `word` — what "Ignore in this
 * project" calls once the dictionary write succeeds, so the squiggle doesn't
 * outlive the decision. Case-sensitive, matching the dictionary. Later checks
 * stay clean because the server filters by the job's dictionary.
 */
export function clearSpellWord(editor: { view: EditorView }, word: string) {
  editor.view.dispatch(editor.view.state.tr.setMeta(spellcheckKey, { clearWord: word }))
}

/**
 * TipTap extension for spell checking via the LanguageTool proxy API.
 * Shows red wavy underlines on spelling/grammar errors.
 * Debounces API calls (800ms after last keystroke).
 */
export const SpellCheck = Extension.create<{
  language: string
  enabled: boolean
  /** Scopes the live check to a job so the server applies its project dictionary. */
  jobId?: number
  /**
   * Called when the user clicks an underlined error. Receives the match resolved
   * to current editor positions so the caller can offer fixes without re-deriving
   * offsets (which would drift after any edit).
   *
   * Plain left-click is the trigger. The handler does NOT preventDefault, so
   * ProseMirror still places the caret where the user clicked, and the panel it
   * opens never takes focus — so clicking a flagged word to edit it manually
   * still works, and typing dismisses the panel. Right-click fires it too, and
   * additionally suppresses the browser's own context menu on the underline.
   */
  onFixRequest?: (request: SpellFixRequest) => void
}>({
  name: 'spellCheck',

  addOptions() {
    return {
      language: 'en',
      enabled: true,
      jobId: undefined,
      onFixRequest: undefined,
    }
  },

  addProseMirrorPlugins() {
    const options = () => this.options

    /**
     * Resolve a pointer event over an underline into a fix request. Returns
     * false when the event wasn't on a flagged word, so callers can decide
     * whether to consume the event.
     */
    const requestFix = (view: EditorView, event: Event): boolean => {
      const { enabled, onFixRequest } = options()
      if (!enabled || !onFixRequest) return false
      const target = event.target as HTMLElement | null
      const span = target?.closest?.('.spellcheck-error') as HTMLElement | null
      if (!span) return false

      const pos = view.posAtDOM(span, 0)
      const state = spellcheckKey.getState(view.state) as { entries: SpellEntry[] } | undefined
      // `pos` lands at the span's start; match on containment so a click
      // anywhere inside the word resolves to the same entry.
      const entry = state?.entries.find(e => pos >= e.from - 1 && pos <= e.to)
      if (!entry) return false

      const rect = span.getBoundingClientRect()
      onFixRequest({
        message: entry.message,
        suggestions: entry.suggestions,
        from: entry.from,
        to: entry.to,
        word: view.state.doc.textBetween(entry.from, entry.to),
        rect: { top: rect.top, left: rect.left, bottom: rect.bottom, right: rect.right },
      })
      return true
    }

    return [
      new Plugin({
        key: spellcheckKey,
        state: {
          init() {
            return {
              decorations: DecorationSet.empty,
              // Parallel to the decorations: the same errors with their data, so
              // a click can resolve to a message + suggestions. Positions are
              // mapped through edits alongside the decorations, so a fix applied
              // after typing still targets the right characters.
              entries: [] as SpellEntry[],
              timer: null as ReturnType<typeof setTimeout> | null,
            }
          },
          apply(tr, old) {
            // Map decorations through document changes
            if (tr.docChanged) {
              return {
                ...old,
                decorations: old.decorations.map(tr.mapping, tr.doc),
                // mapResult, not map: if the user deletes the flagged word while
                // the panel is open, both ends collapse to the same position and
                // a fix would delete nothing then INSERT the suggestion at a
                // stale spot, duplicating text. A deleted range drops instead.
                entries: old.entries.flatMap(e => {
                  const from = tr.mapping.mapResult(e.from)
                  const to = tr.mapping.mapResult(e.to)
                  if (from.deleted || to.deleted || from.pos >= to.pos) return []
                  return [{ ...e, from: from.pos, to: to.pos }]
                }),
              }
            }
            // Check for spell check results in metadata
            const meta = tr.getMeta(spellcheckKey)
            if (meta?.decorations) {
              return { ...old, decorations: meta.decorations, entries: meta.entries ?? [] }
            }
            if (typeof meta?.clearWord === 'string') {
              let decorations: DecorationSet = old.decorations
              const entries = old.entries.filter((e: SpellEntry) => {
                if (tr.doc.textBetween(e.from, e.to) !== meta.clearWord) return true
                decorations = decorations.remove(decorations.find(e.from, e.to))
                return false
              })
              return { ...old, decorations, entries }
            }
            return old
          },
        },
        props: {
          decorations(state) {
            return this.getState(state)?.decorations ?? DecorationSet.empty
          },
          handleDOMEvents: {
            click(view, event) {
              // Return false: let ProseMirror handle the click as usual so the
              // caret still lands where the user clicked.
              requestFix(view, event)
              return false
            },
            contextmenu(view, event) {
              // Right-click works too. Here we DO consume the event, so the
              // browser's own context menu doesn't cover the panel.
              if (!requestFix(view, event)) return false
              event.preventDefault()
              return true
            },
          },
        },
        view(editorView: any) { // eslint-disable-line @typescript-eslint/no-explicit-any
          let timer: ReturnType<typeof setTimeout> | null = null
          let lastText = ''

          const clearDecorations = () => {
            const tr = editorView.state.tr.setMeta(spellcheckKey, { decorations: DecorationSet.empty, entries: [] })
            editorView.dispatch(tr)
          }

          const check = async () => {
            if (!options().enabled) return

            const text = editorView.state.doc.textContent
            if (text === lastText || text.length < 2) return
            lastText = text

            try {
              const matches: SpellMatch[] = await checkSpelling(text, options().language, options().jobId)

              // Build decorations from matches. LanguageTool reports plain-text
              // offsets against the string we sent (doc.textContent), which
              // excludes tag chips — so the mapping has to walk the document
              // rather than assume `pos = offset + 1`.
              const decorations: Decoration[] = []
              const entries: SpellEntry[] = []
              const doc = editorView.state.doc
              for (const m of matches) {
                const from = plainOffsetToPm(doc, m.offset)
                const to = plainOffsetToPm(doc, m.offset + m.length)
                if (from >= 1 && to <= editorView.state.doc.content.size) {
                  entries.push({
                    from, to,
                    message: m.message,
                    suggestions: (m.replacements ?? []).slice(0, 6).map(r => r.value),
                  })
                  decorations.push(
                    Decoration.inline(from, to, {
                      class: 'spellcheck-error',
                      title: m.message + (m.replacements.length > 0
                        ? ` → ${m.replacements.slice(0, 3).map(r => r.value).join(', ')}`
                        : ''),
                    })
                  )
                }
              }

              const decoSet = DecorationSet.create(editorView.state.doc, decorations)
              const tr = editorView.state.tr.setMeta(spellcheckKey, { decorations: decoSet, entries })
              editorView.dispatch(tr)
            } catch (err) {
              // The live underlines stay best-effort — a transient LanguageTool
              // hiccup shouldn't interrupt typing with a toast. It is logged,
              // not swallowed: a silent failure looks exactly like "no spelling
              // errors". The review dialog surfaces its own failures visibly.
              console.warn('spellcheck: live check failed', err)
              lastText = '' // let the next keystroke retry rather than latching
            }
          }

          return {
            update() {
              if (timer) clearTimeout(timer)
              // Toggled off mid-session: drop the existing underlines instead of
              // leaving stale ones on screen until the row unmounts.
              if (!options().enabled) {
                if (spellcheckKey.getState(editorView.state)?.decorations?.find().length) {
                  clearDecorations()
                }
                lastText = ''
                return
              }
              timer = setTimeout(check, 800)
            },
            destroy() {
              if (timer) clearTimeout(timer)
            },
          }
        },
      }),
    ]
  },
})
