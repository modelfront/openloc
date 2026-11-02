import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useEffect } from 'react'
import { act, render, screen } from '@testing-library/react'
import { SegmentText } from '../components/SegmentRow.shared'
import { SOURCE_SELECTION_HIGHLIGHT, useSourceSelection } from './useSourceSelection'

let read: () => string = () => ''
let clear: () => void = () => {}

// jsdom has no CSS Custom Highlight API: a registry and a set-like Highlight
// stand in, so the tests see which Range the hook paints.
class FakeHighlight extends Set<AbstractRange> {
  constructor(...ranges: AbstractRange[]) { super(ranges) }
}
let highlights: Map<string, FakeHighlight>

beforeEach(() => {
  highlights = new Map()
  vi.stubGlobal('CSS', { highlights })
  vi.stubGlobal('Highlight', FakeHighlight)
})
afterEach(() => { vi.unstubAllGlobals() })

/** The text of the range painted as the remembered source selection. */
function marked(): string[] {
  return [...(highlights.get(SOURCE_SELECTION_HIGHLIGHT) ?? [])].map(r => (r as Range).toString())
}

/** The open row's source, as SegmentText renders it, beside a translation. */
function Row({ text, richText, enabled = true }: { text: string; richText?: string; enabled?: boolean }) {
  const { fieldRef, getText, clear: clearSelection } = useSourceSelection(enabled)
  useEffect(() => { read = getText; clear = clearSelection })
  return (
    <div>
      <div data-testid="source" ref={el => { fieldRef.current = el }}>
        <SegmentText text={text} richText={richText} />
      </div>
      <div data-testid="target">Übersetzung</div>
    </div>
  )
}

function textNode(root: HTMLElement, text: string): Node {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n.textContent?.includes(text)) return n
  }
  throw new Error(`no text node with ${JSON.stringify(text)}`)
}

function select(startNode: Node, start: number, endNode: Node, end: number) {
  act(() => {
    document.getSelection()!.setBaseAndExtent(startNode, start, endNode, end)
    document.dispatchEvent(new Event('selectionchange'))
  })
}

describe('useSourceSelection', () => {
  it('reads the selection as plain source text, without tag chips', () => {
    render(<Row text="Click here now" richText={'Click <g id="1">here</g> now'} />)
    const source = screen.getByTestId('source')
    select(textNode(source, 'Click'), 0, textNode(source, 'now'), ' now'.length)
    expect(read()).toBe('Click here now')
  })

  it('keeps the source selection when the user selects in the translation', () => {
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    const at = 'Reset your '.length
    select(node, at, node, at + 'password'.length)
    const target = textNode(screen.getByTestId('target'), 'Übersetzung')
    select(target, 0, target, 4)
    expect(read()).toBe('password')
  })

  it('drops it when the user clicks elsewhere in the source', () => {
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    expect(read()).toBe('Reset')
    select(node, 2, node, 2)
    expect(read()).toBe('')
  })

  it('remembers nothing while the row is closed', () => {
    render(<Row text="Reset your password now" enabled={false} />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    expect(read()).toBe('')
  })

  it('marks the source selection while the user selects in the translation', () => {
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    const at = 'Reset your '.length
    select(node, at, node, at + 'password'.length)
    expect(marked()).toEqual(['password'])
    const target = textNode(screen.getByTestId('target'), 'Übersetzung')
    select(target, 0, target, 4)
    expect(marked()).toEqual(['password'])
  })

  it('removes the mark when the user clicks elsewhere in the source', () => {
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    expect(marked()).toEqual(['Reset'])
    select(node, 2, node, 2)
    expect(highlights.has(SOURCE_SELECTION_HIGHLIGHT)).toBe(false)
  })

  it('removes the mark and the text when cleared, as the quick-add dialog does on close', () => {
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    act(() => clear())
    expect(highlights.has(SOURCE_SELECTION_HIGHLIGHT)).toBe(false)
    expect(read()).toBe('')
  })

  it('removes the mark when the row closes', () => {
    const { rerender } = render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    expect(marked()).toEqual(['Reset'])
    rerender(<Row text="Reset your password now" enabled={false} />)
    expect(highlights.has(SOURCE_SELECTION_HIGHLIGHT)).toBe(false)
  })

  it('still remembers the text where the browser has no highlight API', () => {
    vi.stubGlobal('CSS', {})
    render(<Row text="Reset your password now" />)
    const node = textNode(screen.getByTestId('source'), 'password')
    select(node, 0, node, 5)
    expect(read()).toBe('Reset')
    act(() => clear())
    expect(read()).toBe('')
  })
})
