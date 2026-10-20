import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { highlightText, HIGHLIGHT_TINT } from './text-highlight'

describe('highlightText', () => {
  it('returns plain string when term is empty', () => {
    const result = highlightText('Hello world', '')
    expect(result).toBe('Hello world')
  })

  it('wraps matching text in <mark> tags', () => {
    const { container } = render(<>{highlightText('Hello world', 'world')}</>)
    const marks = container.querySelectorAll('mark')
    expect(marks).toHaveLength(1)
    expect(marks[0].textContent).toBe('world')
  })

  it('is case-insensitive', () => {
    const { container } = render(<>{highlightText('Hello WORLD', 'world')}</>)
    const marks = container.querySelectorAll('mark')
    expect(marks).toHaveLength(1)
    expect(marks[0].textContent).toBe('WORLD')
  })

  it('highlights multiple occurrences', () => {
    const { container } = render(<>{highlightText('foo bar foo baz foo', 'foo')}</>)
    const marks = container.querySelectorAll('mark')
    expect(marks).toHaveLength(3)
  })

  it('escapes regex special characters', () => {
    const { container } = render(<>{highlightText('Price: $49.99', '$49.99')}</>)
    const marks = container.querySelectorAll('mark')
    expect(marks).toHaveLength(1)
    expect(marks[0].textContent).toBe('$49.99')
  })

  it('returns original text when no match', () => {
    const { container } = render(<>{highlightText('Hello world', 'xyz')}</>)
    expect(container.querySelectorAll('mark')).toHaveLength(0)
    expect(container.textContent).toBe('Hello world')
  })

  it('has yellow background class on mark', () => {
    const { container } = render(<>{highlightText('Hello world', 'world')}</>)
    const mark = container.querySelector('mark')
    expect(mark?.className).toContain(HIGHLIGHT_TINT)
  })
})
