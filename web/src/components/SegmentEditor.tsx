import { useEffect, useRef, forwardRef } from 'react'
import { Extension } from '@tiptap/core'
import RichTextCellEditor, { type RichTextCellEditorHandle } from './RichTextCellEditor'
import type { SpellFixActions } from '../lib/spellcheck-scope'
import type { RefResolver } from '../lib/xliff-tag-label'
import type { LineCaret } from '../lib/line-caret'
import type { EntryCaret } from '../lib/entry-caret'
import { createLineMoves } from '../lib/tiptap-line-moves'

export type SegmentEditorHandle = RichTextCellEditorHandle

// PM position ↔ plain-text offset conversion lives on the editor handle
// (`posToOffset` / `selectPlainRange`), backed by `lib/pm-offset.ts`.

interface SegmentEditorProps {
  initialText: string
  readOnly: boolean
  placeholder?: string
  className?: string
  language?: string
  spellCheck?: boolean
  spellJobId?: number
  spellFixActions?: SpellFixActions
  spellSourceText?: string
  showWhitespace?: boolean
  /** Resolves cross-reference chips to their term. */
  resolveRef?: RefResolver
  /** The file's strings are plain text: load and save them as typed. */
  plainText?: boolean
  /** Track changes: the text to mark changes against; null shows none. */
  trackChangesFrom?: string | null
  onTextChange: (plainText: string) => void
  onSelectionChange?: (from: number, to: number) => void
  onConfirmAndAdvance: () => void
  /** `caret` is where the next or previous segment's caret goes: set when an
   *  arrow key leaves this segment from its last or first line. */
  onNavigate: (dir: 'next' | 'prev', caret?: LineCaret) => void
  /** Where the caret goes when the editor mounts; the end of the text when
   *  absent. */
  initialCaret?: EntryCaret
  onDeactivate: () => void
  onCopySource: () => void
  // F8: insert the next missing source tag, placeable or term at cursor.
  // Alt-F8: insert all remaining missing tags, placeables and terms at cursor.
  onInsertNextFromSource?: () => void
  onInsertAllFromSource?: () => void
  // Mod-Shift-Backspace: clear the translation.
  onClearTranslation?: () => void
}

// CAT-only keyboard shortcuts: Mod-Enter, Tab/Shift-Tab, Mod-Shift-s,
// Escape, F8 (next source tag, placeable or term), Alt-F8 (all of them), and plain Enter as
// a no-op. Up/Down are createLineMoves'.
function createSegmentKeymap(props: {
  onConfirmAndAdvance: () => void
  onNavigate: (dir: 'next' | 'prev', caret?: LineCaret) => void
  onDeactivate: () => void
  onCopySource: () => void
  onInsertNextFromSource: () => void
  onInsertAllFromSource: () => void
  onClearTranslation: () => void
  readOnly: boolean
}) {
  return Extension.create({
    name: 'segmentKeymap',
    addKeyboardShortcuts() {
      return {
        'Mod-Enter': () => {
          if (!props.readOnly) props.onConfirmAndAdvance()
          return true
        },
        // A segment is one paragraph: plain Enter
        // does nothing. Shift+Enter is the line break.
        'Enter': () => true,
        'Tab': () => {
          props.onNavigate('next')
          return true
        },
        'Shift-Tab': () => {
          props.onNavigate('prev')
          return true
        },
        'Mod-Shift-s': () => {
          if (!props.readOnly) props.onCopySource()
          return true
        },
        'Escape': () => {
          props.onDeactivate()
          return true
        },
        'F8': () => {
          if (!props.readOnly) props.onInsertNextFromSource()
          return true
        },
        'Alt-F8': () => {
          if (!props.readOnly) props.onInsertAllFromSource()
          return true
        },
        'Mod-Shift-Backspace': () => {
          props.onClearTranslation()
          return true
        },
      }
    },
  })
}

const SegmentEditor = forwardRef<SegmentEditorHandle, SegmentEditorProps>(function SegmentEditor(props, ref) {
  const {
    initialText,
    readOnly,
    placeholder = 'Click to translate',
    className = '',
    language,
    spellCheck,
    spellJobId,
    spellFixActions,
    spellSourceText,
    showWhitespace,
    resolveRef,
    plainText,
    trackChangesFrom,
    onTextChange,
    onSelectionChange,
    onConfirmAndAdvance,
    onNavigate,
    initialCaret,
    onDeactivate,
    onCopySource,
    onInsertNextFromSource,
    onInsertAllFromSource,
    onClearTranslation,
  } = props

  // Refs keep the keymap closure pointing at the latest callbacks without
  // re-creating the TipTap extension (which can't be hot-swapped).
  const onConfirmRef = useRef(onConfirmAndAdvance)
  const onNavRef = useRef(onNavigate)
  const onDeactivateRef = useRef(onDeactivate)
  const onCopySourceRef = useRef(onCopySource)
  const onInsertNextRef = useRef(onInsertNextFromSource)
  const onInsertAllRef = useRef(onInsertAllFromSource)
  const onClearRef = useRef(onClearTranslation)
  const readOnlyRef = useRef(readOnly)

  useEffect(() => { onConfirmRef.current = onConfirmAndAdvance }, [onConfirmAndAdvance])
  useEffect(() => { onNavRef.current = onNavigate }, [onNavigate])
  useEffect(() => { onDeactivateRef.current = onDeactivate }, [onDeactivate])
  useEffect(() => { onCopySourceRef.current = onCopySource }, [onCopySource])
  useEffect(() => { onInsertNextRef.current = onInsertNextFromSource }, [onInsertNextFromSource])
  useEffect(() => { onInsertAllRef.current = onInsertAllFromSource }, [onInsertAllFromSource])
  useEffect(() => { onClearRef.current = onClearTranslation }, [onClearTranslation])
  useEffect(() => { readOnlyRef.current = readOnly }, [readOnly])
  const initialCaretRef = useRef(initialCaret)
  useEffect(() => { initialCaretRef.current = initialCaret }, [initialCaret])

  const keymapExt = useRef(
    createSegmentKeymap({
      onConfirmAndAdvance: () => onConfirmRef.current(),
      onNavigate: (dir, caret) => onNavRef.current(dir, caret),
      onDeactivate: () => onDeactivateRef.current(),
      onCopySource: () => onCopySourceRef.current(),
      onInsertNextFromSource: () => onInsertNextRef.current?.(),
      onInsertAllFromSource: () => onInsertAllRef.current?.(),
      onClearTranslation: () => onClearRef.current?.(),
      get readOnly() { return readOnlyRef.current },
    })
  )
  const lineMovesExt = useRef(
    createLineMoves({
      onLeave: (dir, caret) => onNavRef.current(dir, caret),
      entryCaret: () => initialCaretRef.current,
    })
  )

  return (
    <RichTextCellEditor
      ref={ref}
      initialText={initialText}
      readOnly={readOnly}
      placeholder={placeholder}
      className={`segment-editor ${className}`}
      dataTestId="segment-editor"
      innerTestId="segment-editor-inner"
      language={language}
      spellCheck={spellCheck}
      spellJobId={spellJobId}
      spellFixActions={spellFixActions}
      spellSourceText={spellSourceText}
      showWhitespace={showWhitespace}
      resolveRef={resolveRef}
      plainText={plainText}
      trackChangesFrom={trackChangesFrom}
      extraExtensions={[keymapExt.current, lineMovesExt.current]}
      initialCaret={initialCaret}
      onTextChange={onTextChange}
      onSelectionChange={onSelectionChange}
    />
  )
})

export default SegmentEditor
