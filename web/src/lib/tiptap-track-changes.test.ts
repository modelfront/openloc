import { describe, it, expect, afterEach } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { XliffTag } from './tiptap-xliff-tag'
import { richTextToTipTapContent } from './xliff-tiptap'
import { HIGHLIGHT_TINT } from './text-highlight'
import { TrackChanges, setTrackChangesBaseline } from './tiptap-track-changes'
import { markTexts } from '../test/text-marks'

let editor: Editor

function mount(content: string | object, baseline: string | null) {
  editor = new Editor({
    element: document.createElement('div'),
    extensions: [StarterKit, XliffTag, TrackChanges.configure({ baseline })],
    content,
  })
  return editor
}

const struck = () => markTexts(editor.view.dom, 'span.line-through')
const added = () => markTexts(editor.view.dom, `span.${HIGHLIGHT_TINT}`)

afterEach(() => editor?.destroy())

describe('TrackChanges extension', () => {
  it('marks what changed since the baseline: removed words struck, added words highlighted', () => {
    mount('<p>Denuncia recibida hoy</p>', 'Denuncia enviada hoy')
    expect(struck()).toEqual(['enviada'])
    expect(added()).toEqual(['recibida'])
    // The removed word reads before its replacement.
    expect(editor.view.dom.textContent).toBe('Denuncia enviadarecibida hoy')
  })

  it('keeps removed words out of the document and out of the editing', () => {
    mount('<p>Denuncia recibida hoy</p>', 'Denuncia enviada hoy')
    expect(editor.getText()).toBe('Denuncia recibida hoy')
    const ghost = editor.view.dom.querySelector('span.line-through')!
    expect(ghost.getAttribute('contenteditable')).toBe('false')
  })

  it('follows typing', () => {
    mount('<p>Denuncia recibida hoy</p>', 'Denuncia enviada hoy')
    editor.commands.insertContentAt(editor.state.doc.content.size - 1, ' mismo')
    expect(added()).toEqual(['recibida', 'mismo'])
    // Typing the baseline back removes every mark.
    editor.commands.setContent('<p>Denuncia enviada hoy</p>')
    expect(struck()).toEqual([])
    expect(added()).toEqual([])
  })

  it('marks nothing without a baseline, and turns on and off on an open editor', () => {
    mount('<p>Denuncia recibida hoy</p>', null)
    expect(struck()).toEqual([])
    expect(added()).toEqual([])

    setTrackChangesBaseline(editor, 'Denuncia enviada hoy')
    expect(struck()).toEqual(['enviada'])

    setTrackChangesBaseline(editor, null)
    expect(struck()).toEqual([])
    expect(added()).toEqual([])
  })

  it('compares the text the chips leave, not the tag markup', () => {
    mount(richTextToTipTapContent('Denuncia <ph id="1"/>recibida hoy'), 'Denuncia enviada hoy')
    expect(struck()).toEqual(['enviada'])
    expect(added()).toEqual(['recibida'])
  })
})
