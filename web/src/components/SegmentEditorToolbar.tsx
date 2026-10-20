import type { Editor } from '@tiptap/react'

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
    <div className="flex items-center gap-0.5">
      {buttons.map((btn) => (
        // eslint-disable-next-line no-restricted-syntax -- formatting toggle in the editor toolbar, not an action button
        <button
          key={btn.label}
          type="button"
          title={btn.title}
          onClick={btn.onClick}
          className={`h-4 px-1.5 py-0 text-xs leading-none rounded border cursor-pointer transition-colors ${
            btn.active
              ? 'bg-primary/10 border-primary text-primary'
              : 'bg-card border-border text-gray-600 dark:text-gray-400 hover:bg-muted'
          } ${btn.className}`}
        >
          {btn.label}
        </button>
      ))}
    </div>
  )
}
