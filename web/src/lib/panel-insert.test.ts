import { describe, it, expect } from 'vitest'
import { panelInsertText } from './panel-insert'

// A tagged source segment and a TMX match whose tags use another markup:
// a rich-text file gets the match re-anchored onto the source's tags, a
// plain-text file gets the plain target.
const sourceRich = 'Keep H<g id="1" ctype="x-html-sub">2</g>O cold.'
const match = {
  target_text: 'Halten Sie H2O kalt.',
  target_rich_text: 'Halten Sie H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O kalt.',
}

describe('panelInsertText', () => {
  it('re-anchors a TM match onto the source tags', () => {
    expect(panelInsertText({ kind: 'tm', match }, sourceRich))
      .toBe('Halten Sie H<g id="1" ctype="x-html-sub">2</g>O kalt.')
  })

  it('gives a plain-text file the plain target of a tagged TM match', () => {
    expect(panelInsertText({ kind: 'tm', match }, sourceRich, true)).toBe('Halten Sie H2O kalt.')
  })

  it('inserts an MT suggestion as it came', () => {
    expect(panelInsertText({ kind: 'mt', text: 'Halten Sie H2O kalt.' }, sourceRich, true)).toBe('Halten Sie H2O kalt.')
  })
})
