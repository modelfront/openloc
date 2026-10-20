import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'

vi.mock('../api/spellcheck', () => ({
  checkSpelling: vi.fn(),
}))

import { checkSpelling } from '../api/spellcheck'
import { SpellCheck, clearSpellWord } from './tiptap-spellcheck'

const checkSpellingMock = vi.mocked(checkSpelling)

const TEXT = 'die adresse und die adresse und die stadt'

function flag(word: string): { message: string; offset: number; length: number; replacements: { value: string }[]; rule: { id: string; description: string; category: { id: string; name: string } } }[] {
  const out = []
  let idx = TEXT.indexOf(word)
  while (idx >= 0) {
    out.push({
      message: 'Possible spelling mistake found.',
      offset: idx,
      length: word.length,
      replacements: [{ value: word.toUpperCase() }],
      rule: { id: 'GERMAN_SPELLER_RULE', description: 'Typo', category: { id: 'TYPOS', name: 'Possible Typo' } },
    })
    idx = TEXT.indexOf(word, idx + 1)
  }
  return out
}

const underlinedWords = (editor: Editor) =>
  Array.from(editor.view.dom.querySelectorAll('.spellcheck-error')).map(el => el.textContent)

describe('SpellCheck extension', () => {
  let editor: Editor

  beforeEach(() => {
    vi.useFakeTimers()
    checkSpellingMock.mockReset()
    checkSpellingMock.mockResolvedValue([...flag('adresse'), ...flag('stadt')])
    editor = new Editor({
      element: document.createElement('div'),
      extensions: [StarterKit, SpellCheck.configure({ language: 'de', enabled: true, jobId: 42 })],
      content: `<p>${TEXT}</p>`,
    })
  })

  afterEach(() => {
    editor.destroy()
    vi.useRealTimers()
  })

  // Runs the debounced live check to completion: a transaction to trigger the
  // plugin's update hook, the 800ms debounce, then the resolved promise.
  async function runLiveCheck() {
    editor.commands.setTextSelection(1)
    await vi.advanceTimersByTimeAsync(900)
  }

  it('sends the job id with the live check so the server can apply the project dictionary', async () => {
    await runLiveCheck()
    expect(checkSpellingMock).toHaveBeenCalledWith(TEXT, 'de', 42)
    expect(underlinedWords(editor)).toEqual(['adresse', 'adresse', 'stadt'])
  })

  it('clearSpellWord drops every underline for that word and leaves the rest', async () => {
    await runLiveCheck()
    clearSpellWord(editor, 'adresse')
    expect(underlinedWords(editor)).toEqual(['stadt'])
  })

  it('clearSpellWord is case-sensitive, matching the dictionary', async () => {
    await runLiveCheck()
    clearSpellWord(editor, 'Adresse')
    expect(underlinedWords(editor)).toEqual(['adresse', 'adresse', 'stadt'])
  })
})
