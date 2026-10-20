import { useEffect, useRef, useState, useCallback, useImperativeHandle, forwardRef } from 'react'
import { createPortal } from 'react-dom'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import Superscript from '@tiptap/extension-superscript'
import Subscript from '@tiptap/extension-subscript'
import type { Extension } from '@tiptap/core'
import { XliffTag, setTagLabelMode, setChipRefResolver } from '../lib/tiptap-xliff-tag'
import { SpellCheck, toggleSpellCheck, clearSpellWord, type SpellFixRequest } from '../lib/tiptap-spellcheck'
import { WhitespaceMarkers, toggleWhitespace } from '../lib/tiptap-whitespace'
import { labelModeFor, type RefResolver } from '../lib/xliff-tag-label'
import { richTextToTipTapContent, tipTapToRichText, hasXliffTags } from '../lib/xliff-tiptap'
import { plainOffsetToPm, pmToPlainOffset } from '../lib/pm-offset'
import { count } from '../lib/plural'
import { positionFixMenu, FIX_MENU_WIDTH } from '../lib/spellcheck-menu-position'
import type { SpellFixActions, SpellFixScope } from '../lib/spellcheck-scope'
import SpellFixFields from './SpellFixFields'
import { Button } from '@/components/ui/button'

// --- Imperative handle exposed to parent ---

export interface RichTextCellEditorHandle {
  focus: () => void
  getText: () => string
  getHTML: () => string
  // Returns XLIFF rich text if doc contains xliffTag nodes, null otherwise.
  getRichText: () => string | null
  setText: (text: string) => void
  setHTML: (html: string) => void
  getSelection: () => { from: number; to: number }
  setSelection: (from: number, to: number) => void
  insertText: (text: string) => void
  replaceRange: (from: number, to: number, text: string) => void
  // --- Plain-text-offset variants ---
  // Callers working from outside offsets (LanguageTool matches, F&R hits,
  // terminology lookups) should use these rather than doing their own position
  // arithmetic: tag chips and hard breaks make `pos = offset + 1` wrong. The
  // editor owns its document, so the conversion belongs here.
  posToOffset: (pos: number) => number
  selectPlainRange: (from: number, to: number) => void
  getEditor: () => ReturnType<typeof useEditor>
  destroy: () => void
}

// --- Props ---

interface RichTextCellEditorProps {
  initialText: string
  readOnly: boolean
  placeholder?: string
  className?: string
  dataTestId?: string
  innerTestId?: string
  language?: string // for spell check (e.g. "es", "de")
  spellCheck?: boolean
  // Job the segment belongs to; scopes the live check to its project dictionary.
  spellJobId?: number
  showWhitespace?: boolean
  /** Resolves cross-reference chips to their term. */
  resolveRef?: RefResolver
  /** The text is plain: tags and HTML in it load as typed, never as chips or formatting. */
  plainText?: boolean
  // Word-level fix actions for the inline spell menu. Omitted → the menu offers
  // only "replace in this segment", since nothing above it can reach the file.
  spellFixActions?: SpellFixActions
  // Source text of this segment, shown in the fix panel for context.
  spellSourceText?: string
  // Captured at mount; subsequent changes are ignored (TipTap extensions can't be hot-swapped).
  extraExtensions?: Extension[]
  onTextChange: (plainText: string) => void
  onSelectionChange?: (from: number, to: number) => void
  onBlur?: () => void
}

// --- Component ---

const RichTextCellEditor = forwardRef<RichTextCellEditorHandle, RichTextCellEditorProps>(function RichTextCellEditor(props, ref) {
  const {
    initialText,
    readOnly,
    placeholder,
    className = '',
    dataTestId,
    innerTestId,
    language = 'en',
    spellCheck: spellCheckEnabled = false,
    spellJobId,
    showWhitespace = false,
    resolveRef,
    plainText = false,
    spellFixActions,
    spellSourceText,
    extraExtensions,
    onTextChange,
    onSelectionChange,
    onBlur,
  } = props

  // Keep callbacks in refs so TipTap event handlers always see the latest version.
  const onTextChangeRef = useRef(onTextChange)
  const onSelChangeRef = useRef(onSelectionChange)
  const onBlurRef = useRef(onBlur)

  useEffect(() => { onTextChangeRef.current = onTextChange }, [onTextChange])
  useEffect(() => { onSelChangeRef.current = onSelectionChange }, [onSelectionChange])
  useEffect(() => { onBlurRef.current = onBlur }, [onBlur])
  const plainTextRef = useRef(plainText)
  useEffect(() => { plainTextRef.current = plainText }, [plainText])

  // Capture extra extensions at mount. TipTap doesn't support hot-swapping the
  // extension list, so changes after mount are intentionally ignored.
  const extraExtRef = useRef(extraExtensions ?? [])

  // Right-click-a-squiggle → inline fix menu. The request is routed through a
  // ref because the extension captures its options at construction; a stable
  // indirection keeps the handler current without rebuilding the editor.
  // Every fix writes text, so a read-only editor opens no menu.
  const [fixMenu, setFixMenu] = useState<SpellFixRequest | null>(null)
  // Prefilled with LanguageTool's top pick so the common case is one click.
  const [fixChoice, setFixChoice] = useState('')
  const [fixBusy, setFixBusy] = useState(false)
  const fixRequestRef = useRef<(r: SpellFixRequest) => void>(() => {})
  fixRequestRef.current = r => {
    if (readOnly) return
    setFixMenu(r)
    setFixChoice(r.suggestions[0] ?? '')
    setFixBusy(false)
  }

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        // Bold (Ctrl+B) and italic (Ctrl+I) come from StarterKit. Block-level
        // features are disabled — segments are single-line.
        heading: false,
        blockquote: false,
        bulletList: false,
        orderedList: false,
        codeBlock: false,
        horizontalRule: false,
        listItem: false,
      }),
      Superscript,
      Subscript,
      XliffTag.configure({ labelMode: labelModeFor(showWhitespace), resolveRef }),
      SpellCheck.configure({
        language,
        enabled: spellCheckEnabled,
        jobId: spellJobId,
        onFixRequest: r => fixRequestRef.current(r),
      }),
      WhitespaceMarkers.configure({ enabled: showWhitespace }),
      ...(placeholder !== undefined ? [Placeholder.configure({ placeholder })] : []),
      ...extraExtRef.current,
    ],
    content: initialText ? textToContent(initialText, plainText) : '',
    editable: !readOnly,
    onUpdate: ({ editor: ed }) => {
      // Use a single '\n' as the block separator so paragraph
      // splits and hardBreaks both emit one newline in plain text.
      // TipTap's default is '\n\n' which round-trips through
      // textToContent (split by '\n') as TWO hardBreaks — every Enter
      // doubles on reload. Unifying both break types as '\n' keeps the
      // round-trip honest.
      onTextChangeRef.current(ed.getText({ blockSeparator: '\n' }))
    },
    onSelectionUpdate: ({ editor: ed }) => {
      const { from, to } = ed.state.selection
      onSelChangeRef.current?.(from, to)
    },
    onBlur: () => {
      onBlurRef.current?.()
    },
    editorProps: {
      attributes: {
        ...(innerTestId ? { 'data-testid': innerTestId } : {}),
        // Turn OFF the browser's own spell checker in this contenteditable.
        //
        // Chrome and Safari spellcheck contenteditables by default, using the
        // *document's* language (`lang="en"`) rather than the segment's target
        // language. On a German segment that underlines nearly every word, and
        // the native squiggles are visually indistinguishable from ours — so a
        // translator right-clicking a red underline gets Chrome's menu (with
        // English suggestions for German text) instead of the LanguageTool fix
        // menu.
        //
        // LanguageTool is the only checker here that knows the target language,
        // the project dictionary, and the tag structure. Two competing checkers
        // in one field is worse than either alone.
        spellcheck: 'false',
        // Still declare the language: screen readers use it for pronunciation,
        // and it keeps the DOM honest about what this text actually is.
        lang: language,
      },
    },
  })

  // Sync readOnly prop
  useEffect(() => {
    if (editor && editor.isEditable === readOnly) {
      editor.setEditable(!readOnly)
    }
  }, [editor, readOnly])

  // Reload content when initialText changes externally (e.g. CAT copy-source).
  const lastInitialText = useRef(initialText)
  useEffect(() => {
    if (editor && initialText !== lastInitialText.current) {
      lastInitialText.current = initialText
      const currentText = editor.getText({ blockSeparator: '\n' })
      if (currentText !== initialText) {
        editor.commands.setContent(initialText ? textToContent(initialText, plainTextRef.current) : '')
      }
    }
  }, [editor, initialText])

  // Focus on mount, caret at the end. An editor that already has focus by
  // then (a busy machine can show the text well before this timer runs) keeps
  // the caret or selection the user put there.
  useEffect(() => {
    if (editor) {
      const t = setTimeout(() => {
        if (!editor.isDestroyed && !editor.view.hasFocus()) editor.commands.focus('end')
      }, 0)
      return () => clearTimeout(t)
    }
  }, [editor])

  // Toggle whitespace markers reactively
  useEffect(() => {
    if (editor && !editor.isDestroyed) {
      toggleWhitespace(editor, showWhitespace)
    }
  }, [showWhitespace, editor])

  // The same toggle switches inline tag chips between numeric labels (<1>)
  // and element names (<sub>) "Chip labels". Dispatches plugin
  // meta only, so flipping it mid-edit leaves the document (and therefore the
  // dirty flag and undo stack) untouched.
  useEffect(() => {
    if (editor && !editor.isDestroyed) {
      setTagLabelMode(editor, labelModeFor(showWhitespace))
    }
  }, [showWhitespace, editor])

  // Cross-reference chips resolve to the defining segment's translation, so the
  // editor updates the moment that term is translated.
  useEffect(() => {
    if (editor && !editor.isDestroyed) {
      setChipRefResolver(editor, resolveRef)
    }
  }, [resolveRef, editor])

  // Reconcile spell check with the prop reactively — `enabled` is captured
  // when the editor is constructed and never updated on its own.
  useEffect(() => {
    if (editor && !editor.isDestroyed) {
      toggleSpellCheck(editor, spellCheckEnabled)
    }
  }, [spellCheckEnabled, editor])

  useImperativeHandle(ref, () => ({
    focus: () => editor?.commands.focus(),
    getText: () => editor?.getText({ blockSeparator: '\n' }) ?? '',
    getHTML: () => editor?.getHTML() ?? '',
    getRichText: () => {
      if (!editor || plainTextRef.current) return null
      let hasTag = false
      editor.state.doc.descendants((node) => {
        if (node.type.name === 'xliffTag') hasTag = true
        return !hasTag
      })
      if (hasTag) {
        return tipTapToRichText(editor.state.doc.toJSON())
      }
      // If the user TYPED tag markup (no xliffTag nodes, but the
      // plain text holds a recognizable XLIFF inline tag like `<ph id="1"/>`),
      // surface that as the rich form so the backend stores it and the next
      // load can render it as a chip via the textToContent → richTextToTipTapContent
      // path. Without this, typing a tag silently round-trips as plain text and
      // the user wonders why their tag never gained the chip styling.
      const plain = editor.getText({ blockSeparator: '\n' })
      if (hasXliffTags(plain)) {
        return plain
      }
      return null
    },
    setText: (text: string) => {
      editor?.commands.setContent(text ? textToContent(text, plainTextRef.current) : '')
    },
    setHTML: (html: string) => {
      editor?.commands.setContent(html)
    },
    getSelection: () => {
      if (!editor) return { from: 1, to: 1 }
      const { from, to } = editor.state.selection
      return { from, to }
    },
    setSelection: (from: number, to: number) => {
      if (!editor) return
      const docSize = editor.state.doc.content.size
      // Clamp to valid range (1..docSize-1 for content inside the wrapping <p>)
      const clampedFrom = Math.max(1, Math.min(from, docSize - 1))
      const clampedTo = Math.max(1, Math.min(to, docSize - 1))
      editor.commands.focus()
      editor.commands.setTextSelection({ from: clampedFrom, to: clampedTo })
    },
    insertText: (text: string) => {
      editor?.commands.insertContent(text)
    },
    replaceRange: (from: number, to: number, text: string) => {
      if (!editor) return
      editor.chain().focus().deleteRange({ from, to }).insertContentAt(from, text).run()
    },
    posToOffset: (pos: number) => (editor ? pmToPlainOffset(editor.state.doc, pos) : Math.max(0, pos - 1)),
    selectPlainRange: (from: number, to: number) => {
      if (!editor) return
      const doc = editor.state.doc
      const pmFrom = plainOffsetToPm(doc, from)
      const pmTo = plainOffsetToPm(doc, to)
      const docSize = doc.content.size
      editor.commands.focus()
      editor.commands.setTextSelection({
        from: Math.max(1, Math.min(pmFrom, docSize - 1)),
        to: Math.max(1, Math.min(pmTo, docSize - 1)),
      })
    },
    getEditor: () => editor,
    destroy: () => { editor?.destroy() },
  }), [editor])

  /** Replace the flagged word in THIS segment, through the open editor. */
  const applyHere = useCallback((menu: SpellFixRequest, replacement: string) => {
    if (!editor) return
    // Positions came from plugin state and were mapped through any edits since
    // the check, so they still point at the flagged characters.
    editor.chain().focus()
      .deleteRange({ from: menu.from, to: menu.to })
      .insertContentAt(menu.from, replacement)
      .run()
  }, [editor])

  /**
   * Apply at the chosen scope. The open segment is always fixed through the
   * editor (so its unsaved draft stays coherent); wider scopes additionally
   * delegate the other segments upward, which writes them via batch-draft.
   */
  const applyScope = useCallback(async (scope: SpellFixScope) => {
    const menu = fixMenu
    const replacement = fixChoice.trim()
    if (!menu || !replacement) return
    setFixBusy(true)
    try {
      applyHere(menu, replacement)
      if (scope !== 'segment') {
        await spellFixActions?.applyScope(menu.word, replacement, scope)
      }
    } finally {
      setFixBusy(false)
      setFixMenu(null)
    }
  }, [fixMenu, fixChoice, applyHere, spellFixActions])

  const ignoreInProject = useCallback(async () => {
    const menu = fixMenu
    if (!menu || !spellFixActions) return
    setFixMenu(null)
    const saved = await spellFixActions.ignoreInProject(menu.word)
    // The editor may have unmounted while the request was in flight.
    if (saved && editor && !editor.isDestroyed) clearSpellWord(editor, menu.word)
  }, [fixMenu, spellFixActions, editor])

  // Dismiss on Escape or any outside pointer press — a menu that outlives its
  // word would apply a fix to text the user has moved on from.
  useEffect(() => {
    if (!fixMenu) return
    // Keys inside the spell check panel keep it open; any other keypress dismisses it.
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { setFixMenu(null); return }
      if (!(e.target as HTMLElement)?.closest?.('[data-spellcheck-menu]')) setFixMenu(null)
    }
    const onDown = (e: MouseEvent) => {
      if (!(e.target as HTMLElement)?.closest?.('[data-spellcheck-menu]')) setFixMenu(null)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onDown)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onDown)
    }
  }, [fixMenu])

  return (
    <div
      data-testid={dataTestId}
      dir="auto"
      className={className}
    >
      <EditorContent editor={editor} />
      {fixMenu && createPortal((
        <div
          data-spellcheck-menu
          data-testid="spellcheck-fix-menu"
          role="dialog"
          aria-label="Spell check"
          // Portalled to <body> deliberately. The segment grid is virtualized, so
          // an ancestor row carries `transform: translateY(...)` — and a
          // transformed ancestor becomes the containing block for
          // `position: fixed`, silently reinterpreting these viewport
          // coordinates as offsets from that row. That put the menu hundreds of
          // pixels below the viewport. The portal removes the
          // transform from the chain so viewport coordinates mean what they say.
          style={{
            position: 'fixed',
            zIndex: 60,
            width: FIX_MENU_WIDTH,
            ...(({ top, left }) => ({ top, left }))(
              positionFixMenu(fixMenu.rect, fixMenu.suggestions.length, {
                width: window.innerWidth,
                height: window.innerHeight,
              }),
            ),
          }}
          className="rounded-xl border border-gray-200 dark:border-gray-700 bg-card shadow-xl text-sm overflow-hidden"
        >
          {(() => {
            const counts = spellFixActions?.counts(fixMenu.word)
            const canWriteLocked = spellFixActions?.canWriteLocked ?? false
            const disabled = fixBusy || !fixChoice.trim()
            const target = editor?.getText({ blockSeparator: '\n' }) ?? ''
            const flagFrom = Math.max(0, fixMenu.from - 1)
            return (
              <>
                <div className="flex items-center justify-between px-3 py-2 border-b border-gray-100 dark:border-gray-800">
                  <span className="font-medium">Spell check</span>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    type="button"
                    aria-label="Close"
                    data-testid="spellfix-close"
                    onClick={() => setFixMenu(null)}
                    className="text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 leading-none"
                  >
                    ✕
                  </Button>
                </div>

                <div className="px-3 py-2">
                  <SpellFixFields
                    sourceText={spellSourceText}
                    targetText={target}
                    offset={flagFrom}
                    length={fixMenu.word.length}
                    message={fixMenu.message}
                    suggestions={fixMenu.suggestions}
                    replacement={fixChoice}
                    onReplacementChange={setFixChoice}
                    disabled={fixBusy}
                  />
                </div>

                <div className="border-t border-gray-100 dark:border-gray-800 px-3 pt-2 pb-1">
                  <p className="pb-1.5 text-[11px] uppercase tracking-wide text-gray-400 dark:text-gray-500">
                    Replace
                  </p>
                  <div className="flex flex-wrap gap-1.5">
                    <Button size="sm" disabled={disabled} onClick={() => applyScope('segment')} data-testid="spellfix-segment">
                      In this segment
                    </Button>
                    {spellFixActions && (
                      <>
                        <Button
                          variant="outline" size="sm"
                          disabled={disabled || (counts?.nonlocked ?? 0) < 2}
                          onClick={() => applyScope('nonlocked')}
                          data-testid="spellfix-nonlocked"
                        >
                          In non-locked segments{counts ? ` (${counts.nonlocked})` : ''}
                        </Button>
                        <Button
                          variant="outline" size="sm"
                          disabled={disabled || !canWriteLocked || (counts?.all ?? 0) < 2}
                          disabledReason={canWriteLocked ? undefined : 'Owners and admins only'}
                          onClick={() => applyScope('all')}
                          data-testid="spellfix-all"
                        >
                          In all segments{counts ? ` (${counts.all})` : ''}
                        </Button>
                      </>
                    )}
                  </div>
                </div>

                <div className="px-3 pt-2 pb-3">
                  <div className="flex flex-wrap gap-1.5">
                    {spellFixActions && (
                      <Button variant="outline" size="sm" disabled={fixBusy} onClick={ignoreInProject} data-testid="spellfix-ignore-project">
                        Ignore in this project
                      </Button>
                    )}
                  </div>
                  {counts && (counts.locked > 0 || !canWriteLocked) && (
                    // Also carries the disabled reason of "In all segments": its
                    // tooltip opens under this menu.
                    <p className="pt-2 text-[11px] text-amber-700 dark:text-amber-500">
                      {canWriteLocked
                        ? `“In all segments” also changes the ${count(counts.locked, 'locked segment')}.`
                        : counts.locked > 0
                          ? `Only an owner or admin can change the ${count(counts.locked, 'locked segment')}.`
                          : 'Only an owner or admin can change locked segments.'}
                    </p>
                  )}
                </div>
              </>
            )
          })()}
        </div>
      ), document.body)}
    </div>
  )
})


export default RichTextCellEditor

// --- Utilities ---

/**
 * Convert text to TipTap content. Handles three input shapes:
 *   1. XLIFF rich text with inline tags → TipTap JSON with xliffTag nodes
 *   2. HTML formatting (strong/em/sup/sub/p) → loaded as HTML (string)
 *   3. Plain text → TipTap JSON paragraph with hardBreak nodes for '\n'
 * With plainText set, every string takes path 3: its tags stay text.
 *
 * Plain text is built as TipTap JSON, not parsed as HTML, so tabs and
 * whitespace runs survive verbatim.
 */
export function textToContent(text: string, plainText = false): string | object {
  if (!plainText && hasXliffTags(text)) {
    return richTextToTipTapContent(text)
  }
  if (!plainText && /<(strong|em|sup|sub|p)[\s>/]/.test(text)) {
    return text.startsWith('<p>') ? text : `<p>${text}</p>`
  }
  // Plain text path: build paragraph content as a list of text + hardBreak
  // nodes. ProseMirror text nodes preserve their string verbatim (no
  // whitespace collapse), and hardBreaks round-trip back to '\n' via
  // getText().
  const content: Array<{ type: 'text'; text: string } | { type: 'hardBreak' }> = []
  const lines = text.split('\n')
  for (let i = 0; i < lines.length; i++) {
    if (i > 0) content.push({ type: 'hardBreak' })
    if (lines[i].length > 0) content.push({ type: 'text', text: lines[i] })
  }
  return {
    type: 'doc',
    content: [{
      type: 'paragraph',
      ...(content.length > 0 ? { content } : {}),
    }],
  }
}
