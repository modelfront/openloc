import { useEffect, useRef, forwardRef } from 'react'
import { Extension } from '@tiptap/core'
import RichTextCellEditor, { type RichTextCellEditorHandle } from './RichTextCellEditor'
import type { SpellFixActions } from '../lib/spellcheck-scope'
import type { RefResolver } from '../lib/xliff-tag-label'

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
  onTextChange: (plainText: string) => void
  onSelectionChange?: (from: number, to: number) => void
  onConfirmAndAdvance: () => void
  onNavigate: (dir: 'next' | 'prev') => void
  onDeactivate: () => void
  onCopySource: () => void
  // F8: insert next missing source tag at cursor.
  // Alt-F8: insert all remaining missing source tags at cursor.
  onInsertNextSourceTag?: () => void
  onInsertAllSourceTags?: () => void
  // Mod-Shift-Backspace: clear the translation.
  onClearTranslation?: () => void
}

// CAT-only keyboard shortcuts: Mod-Enter, Tab/Shift-Tab, Mod-Shift-s,
// Escape, F8 (next source tag), Alt-F8 (all source tags), and plain Enter as
// a no-op.
function createSegmentKeymap(props: {
  onConfirmAndAdvance: () => void
  onNavigate: (dir: 'next' | 'prev') => void
  onDeactivate: () => void
  onCopySource: () => void
  onInsertNextSourceTag: () => void
  onInsertAllSourceTags: () => void
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
          if (!props.readOnly) props.onInsertNextSourceTag()
          return true
        },
        'Alt-F8': () => {
          if (!props.readOnly) props.onInsertAllSourceTags()
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
    onTextChange,
    onSelectionChange,
    onConfirmAndAdvance,
    onNavigate,
    onDeactivate,
    onCopySource,
    onInsertNextSourceTag,
    onInsertAllSourceTags,
    onClearTranslation,
  } = props

  // Refs keep the keymap closure pointing at the latest callbacks without
  // re-creating the TipTap extension (which can't be hot-swapped).
  const onConfirmRef = useRef(onConfirmAndAdvance)
  const onNavRef = useRef(onNavigate)
  const onDeactivateRef = useRef(onDeactivate)
  const onCopySourceRef = useRef(onCopySource)
  const onInsertNextRef = useRef(onInsertNextSourceTag)
  const onInsertAllRef = useRef(onInsertAllSourceTags)
  const onClearRef = useRef(onClearTranslation)
  const readOnlyRef = useRef(readOnly)

  useEffect(() => { onConfirmRef.current = onConfirmAndAdvance }, [onConfirmAndAdvance])
  useEffect(() => { onNavRef.current = onNavigate }, [onNavigate])
  useEffect(() => { onDeactivateRef.current = onDeactivate }, [onDeactivate])
  useEffect(() => { onCopySourceRef.current = onCopySource }, [onCopySource])
  useEffect(() => { onInsertNextRef.current = onInsertNextSourceTag }, [onInsertNextSourceTag])
  useEffect(() => { onInsertAllRef.current = onInsertAllSourceTags }, [onInsertAllSourceTags])
  useEffect(() => { onClearRef.current = onClearTranslation }, [onClearTranslation])
  useEffect(() => { readOnlyRef.current = readOnly }, [readOnly])

  const keymapExt = useRef(
    createSegmentKeymap({
      onConfirmAndAdvance: () => onConfirmRef.current(),
      onNavigate: (dir) => onNavRef.current(dir),
      onDeactivate: () => onDeactivateRef.current(),
      onCopySource: () => onCopySourceRef.current(),
      onInsertNextSourceTag: () => onInsertNextRef.current?.(),
      onInsertAllSourceTags: () => onInsertAllRef.current?.(),
      onClearTranslation: () => onClearRef.current?.(),
      get readOnly() { return readOnlyRef.current },
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
      extraExtensions={[keymapExt.current]}
      onTextChange={onTextChange}
      onSelectionChange={onSelectionChange}
    />
  )
})

export default SegmentEditor
