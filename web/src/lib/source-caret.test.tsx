import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { SegmentText } from '../components/SegmentRow.shared'
import { caretOffsetAt, sourceOffsetAt, sourcePointAt } from './source-caret'

/** The text node holding `text`, and the box SegmentText renders into. */
function setup(props: Parameters<typeof SegmentText>[0], text: string) {
  const { container } = render(<div data-testid="box"><SegmentText {...props} /></div>)
  const box = container.firstElementChild as HTMLElement
  const walker = document.createTreeWalker(box, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n.textContent === text) return { box, node: n }
  }
  throw new Error(`no text node ${JSON.stringify(text)} in ${box.innerHTML}`)
}

describe('sourceOffsetAt', () => {
  it('counts plain text', () => {
    const { box, node } = setup({ text: 'Hello world' }, 'Hello world')
    expect(sourceOffsetAt(box, node, 5)).toBe(5)
  })

  it('skips a tag chip, which is not source text', () => {
    const { box, node } = setup({ text: 'Click here now', richText: 'Click <g id="1">here</g> now' }, 'here')
    expect(sourceOffsetAt(box, node, 2)).toBe('Click he'.length)
  })

  it('counts a line break once, not its ¶ mark', () => {
    const { box, node } = setup({ text: 'One\nTwo', showWhitespace: true }, 'T')
    expect(sourceOffsetAt(box, node, 0)).toBe('One\n'.length)
  })

  it('counts a whitespace marker that hides its character', () => {
    const { box, node } = setup({ text: 'a​b', showWhitespace: true }, 'b')
    expect(sourceOffsetAt(box, node, 0)).toBe(2)
  })

  it('is null outside the box', () => {
    const { box } = setup({ text: 'Hello' }, 'Hello')
    expect(sourceOffsetAt(box, document.createTextNode('x'), 0)).toBeNull()
  })
})

describe('sourcePointAt', () => {
  const cases: [string, Parameters<typeof SegmentText>[0], string][] = [
    ['plain text', { text: 'Hello world' }, 'Hello world'],
    ['a tag chip', { text: 'Click here now', richText: 'Click <g id="1">here</g> now' }, 'here'],
    ['a line break', { text: 'One\nTwo', showWhitespace: true }, 'T'],
  ]
  it.each(cases)('is the inverse of sourceOffsetAt across %s', (_, props, text) => {
    const { box } = setup(props, text)
    for (let at = 0; at <= props.text.length; at++) {
      const point = sourcePointAt(box, at)
      expect(point, `offset ${at}`).not.toBeNull()
      expect(sourceOffsetAt(box, point!.node, point!.offset), `offset ${at}`).toBe(at)
    }
  })

  it('is null past the end', () => {
    const { box } = setup({ text: 'Hello' }, 'Hello')
    expect(sourcePointAt(box, 6)).toBeNull()
  })
})

describe('caretOffsetAt', () => {
  const rich = { text: 'Click here now', richText: 'Click <g id="1">here</g> now' }

  it('counts each tag chip as one caret stop', () => {
    // "Click <1>here</1> now": the caret before "e" in "here" has passed
    // "Click " (6) and the opening chip (1), then "h" (1).
    const { box, node } = setup(rich, 'here')
    expect(caretOffsetAt(box, node, 1)).toBe(8)
  })

  it('puts a point inside a chip on its nearer side', () => {
    const { box } = setup(rich, 'here')
    const chip = box.querySelector('.xliff-tag')!
    const label = chip.firstChild!
    const len = label.textContent!.length
    expect(caretOffsetAt(box, label, 0)).toBe(6) // before the chip
    expect(caretOffsetAt(box, label, len)).toBe(7) // after it
  })

  it('counts a line break once, not its ¶ mark', () => {
    const { box, node } = setup({ text: 'One\nTwo', showWhitespace: true }, 'T')
    expect(caretOffsetAt(box, node, 0)).toBe('One\n'.length)
  })

  it('is null outside the box', () => {
    const { box } = setup({ text: 'Hello' }, 'Hello')
    expect(caretOffsetAt(box, document.createTextNode('x'), 0)).toBeNull()
  })
})
