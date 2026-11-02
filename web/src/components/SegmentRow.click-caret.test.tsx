// A click on a closed row is measured in caret stops, which the editor turns
// into its own position (`caretOffsetToPm`). A formatted translation with
// several paragraphs must count each paragraph boundary as one stop on both
// sides, or a click below the first line opens the row with the caret early.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, screen } from '@testing-library/react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import { renderSegmentRow, rowTranslation } from '../test/segment-row'
import { caretOffsetAt } from '../lib/source-caret'

function textNode(box: HTMLElement, text: string): Node {
  const walker = document.createTreeWalker(box, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n.textContent === text) return n
  }
  throw new Error(`no text node ${JSON.stringify(text)} in ${box.innerHTML}`)
}

describe('a closed formatted translation with several paragraphs', () => {
  afterEach(cleanup)

  it('counts the paragraph boundary as one caret stop, as the editor does', () => {
    // Stored as the editor saves it: the outer <p> stripped.
    renderSegmentRow({ isActive: false, translation: rowTranslation({ active_text: '<strong>One</strong></p><p>Two' }) })
    const box = screen.getByTestId('segment-target-box')
    expect(caretOffsetAt(box, textNode(box, 'Two'), 0)).toBe(4) // "One" + the boundary
    expect(caretOffsetAt(box, textNode(box, 'Two'), 2)).toBe(6)
  })

  it('counts an empty paragraph between them as one more stop', () => {
    renderSegmentRow({ isActive: false, translation: rowTranslation({ active_text: '<em>One</em></p><p></p><p>Two' }) })
    const box = screen.getByTestId('segment-target-box')
    expect(caretOffsetAt(box, textNode(box, 'Two'), 0)).toBe(5)
  })
})
