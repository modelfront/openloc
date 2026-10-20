import { useEffect, useImperativeHandle, type Ref } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { cn } from '@/lib/utils'
import { controlFrameClasses, controlTextClasses } from '@/components/ui/control-box'
import {
  LanguageTokenNode, TOKEN_SERIALIZERS, toDoc, type LanguageToken,
} from '@/lib/tiptap-language-token'

export interface TokenTextFieldHandle {
  /** Inserts a token where the caret is, then puts focus back. */
  insertToken: (token: LanguageToken) => void
}

/**
 * A plain-text field whose `<source_lang>` / `<target_lang>` tokens render as
 * chips while you type.
 *
 * Its value is a string in and a string out — the tokens are literal text
 * either side of this component. Only the editing surface knows about chips;
 * the value is plain text.
 *
 * A `<textarea>` cannot draw an element inside its own value, and painting one
 * behind a transparent textarea means keeping two boxes in pixel lockstep
 * through every wrap and scroll. This uses TipTap, already here for the CAT
 * editor, whose tag chips are the same idea (`tiptap-xliff-tag.tsx`).
 *
 * Deliberately no rich text: every mark and block StarterKit offers is off, so
 * the surface holds one paragraph of text, hard breaks and token chips. A
 * system prompt has no bold, and an input rule that turns "1." into a list
 * would rewrite what someone typed.
 */
export function TokenTextField({
  value, onChange, placeholder, disabled, className, minHeight = 'min-h-44',
  handleRef, 'data-testid': testId,
}: {
  value: string
  onChange: (next: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
  /** Tailwind min-height for the editing surface. */
  minHeight?: string
  handleRef?: Ref<TokenTextFieldHandle>
  'data-testid'?: string
}) {
  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        bold: false, italic: false, strike: false, code: false, underline: false,
        link: false, heading: false, blockquote: false, bulletList: false,
        orderedList: false, listItem: false, codeBlock: false, horizontalRule: false,
      }),
      LanguageTokenNode,
      Placeholder.configure({ placeholder: placeholder ?? '' }),
    ],
    content: toDoc(value),
    editable: !disabled,
    editorProps: {
      attributes: {
        class: cn('outline-none whitespace-pre-wrap font-mono', controlTextClasses, minHeight),
        ...(testId ? { 'data-testid': testId } : {}),
      },
    },
    onUpdate: ({ editor: ed }) => onChange(ed.getText({ textSerializers: TOKEN_SERIALIZERS })),
  })

  // Follow an outside change (the mode picker rewrites the starter template),
  // but never echo the user's own typing back into the document — that would
  // reset the caret to the start on every keystroke.
  useEffect(() => {
    if (editor && value !== editor.getText({ textSerializers: TOKEN_SERIALIZERS })) {
      editor.commands.setContent(toDoc(value), { emitUpdate: false })
    }
  }, [editor, value])

  useEffect(() => {
    editor?.setEditable(!disabled)
  }, [editor, disabled])

  useImperativeHandle(handleRef, () => ({
    insertToken: (token: LanguageToken) => {
      editor?.chain().focus().insertContent({ type: 'languageToken', attrs: { token } }).run()
    },
  }), [editor])

  return (
    <EditorContent
      editor={editor}
      // controlFrameClasses, not the parts: it carries the disabled and
      // invalid states too, which is what stops a frame looking editable
      // around a control that is not (control-box.ts).
      className={cn('token-field', controlFrameClasses, 'h-auto py-2 [&_.ProseMirror]:outline-none', className)}
    />
  )
}
