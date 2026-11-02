import type { Editor } from '@tiptap/react'
import { SEGMENT_BAR_CELL } from './SegmentRowAction'

interface SegmentEditorToolbarProps {
  editor: Editor | null
}

/**
 * Compact formatting toolbar for the TipTap segment editor.
 * Shows B, I, X², X₂ toggle buttons on the editor's top border.
 */
export default function SegmentEditorToolbar({ editor }: SegmentEditorToolbarProps) {
  if (!editor) return null

  const buttons = [
    {
      label: 'B',
      title: 'Bold (Ctrl+B)',
      active: editor.isActive('bold'),
      onClick: () => editor.chain().focus().toggleBold().run(),
      className: 'font-bold',
    },
    {
      label: 'I',
      title: 'Italic (Ctrl+I)',
      active: editor.isActive('italic'),
      onClick: () => editor.chain().focus().toggleItalic().run(),
      className: 'italic',
    },
    {
      label: 'X²',
      title: 'Superscript',
      active: editor.isActive('superscript'),
      onClick: () => editor.chain().focus().toggleSuperscript().run(),
      className: '',
    },
    {
      label: 'X₂',
      title: 'Subscript',
      active: editor.isActive('subscript'),
      onClick: () => editor.chain().focus().toggleSubscript().run(),
      className: '',
    },
  ]

  return (
    <div className="flex">
      {buttons.map((btn) => (
        // eslint-disable-next-line no-restricted-syntax -- formatting toggle in the editor toolbar, not an action button
        <button
          key={btn.label}
          type="button"
          title={btn.title}
          onClick={btn.onClick}
          className={`${SEGMENT_BAR_CELL} min-w-4 text-[11px] cursor-pointer ${
            btn.active
              ? 'bg-primary/10 text-primary'
              : 'text-gray-600 dark:text-gray-400 hover:bg-muted'
          } ${btn.className}`}
        >
          {btn.label}
        </button>
      ))}
    </div>
  )
}
