// @vitest-environment node
//
// Pure position/offset arithmetic over ProseMirror documents — no DOM needed.
// Running under `node` rather than the project-default `jsdom` keeps this file
// independent of jsdom's transitive-dep state.
import { describe, it, expect } from 'vitest'
import { getSchema } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Node as PMNode } from '@tiptap/pm/model'
import { XliffTag } from './tiptap-xliff-tag'
import { plainOffsetToPm, pmToPlainOffset } from './pm-offset'

// Schema mirrors RichTextCellEditor's extension list (nodes/marks only — the
// SpellCheck / WhitespaceMarkers / Placeholder extensions add no schema).
const schema = getSchema([
  StarterKit.configure({
    heading: false, blockquote: false, bulletList: false,
    orderedList: false, codeBlock: false, horizontalRule: false, listItem: false,
  }),
  XliffTag,
])

function doc(content: unknown[]): PMNode {
  return PMNode.fromJSON(schema, { type: 'doc', content })
}
function para(content: unknown[]) {
  return { type: 'paragraph', content }
}
function text(t: string) {
  return { type: 'text', text: t }
}
function tag(displayText: string) {
  return { type: 'xliffTag', attrs: { tagType: 'ph', tagId: '1', rawXml: `<ph id="1"/>`, displayText } }
}

// Sanity anchor: these assertions encode the contribution table the mapper is
// built on. A tag chip costs a PM position but zero plain-text characters —
// that asymmetry is the entire bug this module exists to fix.
describe('plain text contribution (documents the invariant)', () => {
  it('xliffTag occupies one PM position and no text', () => {
    const d = doc([para([tag('{1}'), text('abc')])])
    expect(d.textContent).toBe('abc')
    // paragraph content = tag(1) + text(3) = 4; paragraph nodeSize = 4 + 2
    expect(d.content.size).toBe(6)
  })
})

describe('plainOffsetToPm', () => {
  it('maps plain text with no tags (offset + 1)', () => {
    const d = doc([para([text('Wir haben das')])])
    expect(plainOffsetToPm(d, 0)).toBe(1)
    expect(plainOffsetToPm(d, 4)).toBe(5)
    expect(plainOffsetToPm(d, 13)).toBe(14)
  })

  it('skips a leading tag chip', () => {
    // plain text is "Adresse" — the chip contributes nothing
    const d = doc([para([tag('{1}'), text('Adresse')])])
    expect(d.textContent).toBe('Adresse')
    // offset 0 must land AFTER the chip, not before it
    expect(plainOffsetToPm(d, 0)).toBe(2)
    expect(plainOffsetToPm(d, 7)).toBe(9)
  })

  it('accounts for a tag chip in the middle', () => {
    const d = doc([para([text('Sende an '), tag('{1}'), text(' die adresse')])])
    expect(d.textContent).toBe('Sende an  die adresse')
    // "adresse" starts at plain offset 14
    expect(d.textContent.indexOf('adresse')).toBe(14)
    // PM: para=0, text(9) at 1..10, tag at 10..11, text(12) at 11..23
    // plain 14 sits 5 chars into the second text node → 11 + 5 = 16
    expect(plainOffsetToPm(d, 14)).toBe(16)
  })

  it('treats hardBreak as one char and one position', () => {
    const d = doc([para([text('ab'), { type: 'hardBreak' }, text('cd')])])
    // plain is "ab\ncd" — hardBreak renders as \n via renderText
    expect(plainOffsetToPm(d, 0)).toBe(1)
    expect(plainOffsetToPm(d, 3)).toBe(4)
    expect(plainOffsetToPm(d, 4)).toBe(5)
  })

  it('accounts for paragraph boundaries costing two positions but one char', () => {
    const d = doc([para([text('ab')]), para([text('cd')])])
    // plain "ab\ncd" with blockSeparator '\n'
    expect(plainOffsetToPm(d, 1)).toBe(2)
    expect(plainOffsetToPm(d, 3)).toBe(5)
    expect(plainOffsetToPm(d, 4)).toBe(6)
  })

  it('handles several tags before the target', () => {
    const d = doc([para([tag('{1}'), tag('{2}'), text('morgan')])])
    expect(d.textContent).toBe('morgan')
    // two chips = two positions consumed before the text
    expect(plainOffsetToPm(d, 0)).toBe(3)
  })

  it('clamps an out-of-range offset to the document end', () => {
    const d = doc([para([text('abc')])])
    expect(plainOffsetToPm(d, 99)).toBeLessThanOrEqual(d.content.size)
  })
})

describe('pmToPlainOffset', () => {
  it('round-trips through tags', () => {
    const d = doc([para([text('Sende an '), tag('{1}'), text(' die adresse')])])
    for (const offset of [0, 3, 9, 14, 20]) {
      expect(pmToPlainOffset(d, plainOffsetToPm(d, offset))).toBe(offset)
    }
  })

  it('round-trips across paragraphs', () => {
    const d = doc([para([text('ab')]), para([text('cd')])])
    for (const offset of [0, 1, 3, 4]) {
      expect(pmToPlainOffset(d, plainOffsetToPm(d, offset))).toBe(offset)
    }
  })
})
