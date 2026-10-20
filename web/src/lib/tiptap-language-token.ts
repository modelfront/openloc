import { Node, mergeAttributes } from '@tiptap/core'
import { chipVariants } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import type { JSONContent } from '@tiptap/core'

/**
 * The `<source_lang>` / `<target_lang>` tokens a system prompt may carry,
 * rendered as chips inside the editable instruction field.
 *
 * An atom node rather than styled text: a token is one thing. Arrow keys step
 * over it, Backspace removes the whole token, and no edit can leave half of
 * one behind — `<source_lan>` would reach the model as literal text and be read
 * as the name of a language.
 *
 * The field's value stays a plain string: `toDoc` parses tokens out of stored
 * text and `TOKEN_SERIALIZERS` writes them back, so everything either side of
 * the editor — the form, the API, the Go runtime — still sees
 * `<source_lang>` and never knows a chip existed.
 */

/** The tokens the runtime substitutes. Mirrors `ai.TokenSourceLang` / `TokenTargetLang`. */
export const LANGUAGE_TOKENS = ['<source_lang>', '<target_lang>'] as const

export type LanguageToken = (typeof LANGUAGE_TOKENS)[number]

const TOKEN_SPLIT = new RegExp(`(${LANGUAGE_TOKENS.join('|')})`, 'g')

export function isLanguageToken(s: string): s is LanguageToken {
  return (LANGUAGE_TOKENS as readonly string[]).includes(s)
}

export const LanguageTokenNode = Node.create({
  name: 'languageToken',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      token: {
        default: LANGUAGE_TOKENS[0],
        parseHTML: el => el.getAttribute('data-language-token'),
        renderHTML: attrs => ({ 'data-language-token': attrs.token as string }),
      },
    }
  },

  parseHTML() {
    return [{ tag: 'span[data-language-token]' }]
  },

  renderHTML({ HTMLAttributes, node }) {
    // ProseMirror builds DOM from a spec, with no React to render <Chip> into
    // — so the chip's classes come from chipVariants, which returns a plain
    // string. Copying them would be a second Chip.
    return [
      'span',
      mergeAttributes(HTMLAttributes, {
        class: cn(chipVariants({ color: 'blue', size: 'small' }), 'align-baseline'),
      }),
      node.attrs.token as string,
    ]
  },
})

/**
 * Serializers for `editor.getText()`. A token emits its literal and a hard
 * break its newline, so the text that comes out is exactly what went in.
 */
export const TOKEN_SERIALIZERS = {
  languageToken: ({ node }: { node: { attrs: Record<string, unknown> } }) => node.attrs.token as string,
  hardBreak: () => '\n',
}

/**
 * Plain text → a one-paragraph document whose tokens are chips.
 *
 * One paragraph with hard breaks rather than a paragraph per line: a prompt's
 * blank lines are meaningful, and paragraph-per-line would collapse a run of
 * them on the way back out.
 */
export function toDoc(text: string): JSONContent {
  const content: JSONContent[] = []
  text.split('\n').forEach((line, i) => {
    if (i > 0) {
      content.push({ type: 'hardBreak' })
    }
    for (const part of line.split(TOKEN_SPLIT)) {
      if (part === '') {
        continue
      }
      content.push(
        isLanguageToken(part)
          ? { type: 'languageToken', attrs: { token: part } }
          : { type: 'text', text: part },
      )
    }
  })
  return { type: 'doc', content: [{ type: 'paragraph', content }] }
}
