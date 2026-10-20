import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { LanguageTokenNode, TOKEN_SERIALIZERS, toDoc, isLanguageToken } from './tiptap-language-token'

// The field's value is a plain string either side of the editor. Everything
// here is the same question asked of different text: does it come back byte
// for byte? A prompt that loses a blank line, or gains one, is a prompt that
// no longer says what its author wrote.

function roundTrip(text: string): string {
  const editor = new Editor({
    extensions: [
      StarterKit.configure({
        bold: false, italic: false, strike: false, code: false, underline: false,
        link: false, heading: false, blockquote: false, bulletList: false,
        orderedList: false, listItem: false, codeBlock: false, horizontalRule: false,
      }),
      LanguageTokenNode,
    ],
    content: toDoc(text),
  })
  const out = editor.getText({ textSerializers: TOKEN_SERIALIZERS })
  editor.destroy()
  return out
}

describe('language tokens survive the editor', () => {
  const cases: [name: string, text: string][] = [
    ['plain text', 'Judge strictly.'],
    ['one token', 'Translate into <target_lang>.'],
    ['both tokens', 'You are a <source_lang>→<target_lang> reviewer.'],
    ['token at the very start', '<source_lang> is the source.'],
    ['token at the very end', 'The target is <target_lang>'],
    ['two tokens with nothing between', '<source_lang><target_lang>'],
    ['the same token twice', 'From <source_lang>. Again: <source_lang>.'],
    ['a blank line, which separates paragraphs in a prompt', 'First line.\n\nSecond line.'],
    ['several blank lines', 'A.\n\n\n\nB.'],
    ['a trailing newline', 'Judge strictly.\n'],
    ['angle brackets that are not tokens', 'Keep <1>tags</1> and <notatoken> intact.'],
    // The real shape: a built-in template.
    ['a full instruction', 'You are a <source_lang>→<target_lang> reviewer.\n\nJudge the <target_lang> translation.\n- Keep tags.\n- Explain in a comment.'],
  ]

  for (const [name, text] of cases) {
    it(name, () => {
      expect(roundTrip(text)).toBe(text)
    })
  }

  // Empty is its own case: the editor always holds one paragraph, so an empty
  // value must not come back as a newline.
  it('empty stays empty', () => {
    expect(roundTrip('')).toBe('')
  })
})

describe('toDoc', () => {
  it('makes a chip node for a token and text for the rest', () => {
    const doc = toDoc('Hi <source_lang>!')
    expect(doc.content?.[0].content).toEqual([
      { type: 'text', text: 'Hi ' },
      { type: 'languageToken', attrs: { token: '<source_lang>' } },
      { type: 'text', text: '!' },
    ])
  })

  // A near-miss must stay text. Turning it into a chip would tell the author
  // it gets substituted, and it does not.
  it('leaves a misspelled token as plain text', () => {
    const doc = toDoc('<source_lan>')
    expect(doc.content?.[0].content).toEqual([{ type: 'text', text: '<source_lan>' }])
  })
})

describe('isLanguageToken', () => {
  it('accepts the two real tokens and nothing else', () => {
    expect(isLanguageToken('<source_lang>')).toBe(true)
    expect(isLanguageToken('<target_lang>')).toBe(true)
    expect(isLanguageToken('<customer>')).toBe(false)
    expect(isLanguageToken('source_lang')).toBe(false)
    expect(isLanguageToken('')).toBe(false)
  })
})
