import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LinkedText, splitLinks } from './linked-text'

function links() {
  return screen.queryAllByRole('link').map(a => a.getAttribute('href'))
}

describe('LinkedText', () => {
  it('makes web addresses links that open in a new tab and keeps the text around them', () => {
    const text = 'see https://example.com/a?b=1, and (http://example.com/x).'
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toEqual(['https://example.com/a?b=1', 'http://example.com/x'])
    for (const a of screen.getAllByRole('link')) {
      expect(a).toHaveAttribute('target', '_blank')
      expect(a).toHaveAttribute('rel', 'noreferrer')
    }
  })

  it.each([
    ['Chinese', '請看https://example.com/a。謝謝'],
    ['Chinese with no punctuation', 'https://example.com/a謝謝'],
    ['Korean', 'https://example.com/a한국어'],
    ['Hiragana', 'https://example.com/aです'],
    ['Katakana', 'https://example.com/aカタカナ'],
    ['the long vowel mark', 'https://example.com/aー'],
    ['Thai', 'ดูที่https://example.com/aครับ'],
    ['Lao', 'https://example.com/aສະບາຍດີ'],
    ['Khmer', 'https://example.com/aសួស្តី'],
    ['Myanmar', 'https://example.com/aမင်္ဂလာ'],
    ['a full-width comma', 'https://example.com/a，次'],
    ['a full-width bracket', '（https://example.com/a）'],
    ['a sentence end before the text', 'https://example.com/a.謝謝'],
  ])('ends an address at %s', (_name, text) => {
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toEqual(['https://example.com/a'])
  })

  it('finds an address in the text cut off after another one', () => {
    const { container } = render(<LinkedText text="https://example.com/a謝謝https://example.com/b" />)
    expect(container.textContent).toBe('https://example.com/a謝謝https://example.com/b')
    expect(links()).toEqual(['https://example.com/a', 'https://example.com/b'])
  })

  it('leaves an address whose host is written in those scripts as text', () => {
    const text = 'see https://例子.com/a, http://ไทย.th and https://ﾃｽﾄ then https://example.com'
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toEqual(['https://example.com'])
  })

  it('links every address in a long text that runs addresses into Chinese', () => {
    const text = 'https://a.co/謝'.repeat(700)
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toHaveLength(700)
  })

  it('finds the addresses in a very long text in one pass', () => {
    const parts = splitLinks('https://a.co/謝'.repeat(5000))
    expect(parts.filter(p => typeof p !== 'string')).toHaveLength(5000)
    expect(parts.filter(p => p === '謝')).toHaveLength(5000)
  })

  it('keeps letters with accents and other alphabets in an address', () => {
    render(<LinkedText text="https://example.com/Straße, https://münchen.de/ and https://ru.example.com/Москва." />)
    expect(links()).toEqual(['https://example.com/Straße', 'https://münchen.de/', 'https://ru.example.com/Москва'])
  })

  it('keeps a closing bracket that belongs to the address and drops one that does not', () => {
    const text = 'see https://en.wikipedia.org/wiki/Foo_(bar), (https://example.com/x_(y)). [https://example.com/a] https://example.com/[b]'
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toEqual([
      'https://en.wikipedia.org/wiki/Foo_(bar)',
      'https://example.com/x_(y)',
      'https://example.com/a',
      'https://example.com/[b]',
    ])
  })

  it('leaves an address with nothing after the scheme as text', () => {
    const { container } = render(<LinkedText text="type https://... or http://" />)
    expect(container.textContent).toBe('type https://... or http://')
    expect(links()).toEqual([])
  })

  it('links an address followed by a long run of closing brackets', () => {
    const text = 'https://example.com' + ')'.repeat(9990)
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(links()).toEqual(['https://example.com'])
  })

  it('leaves other schemes, bare domains and markup as text', () => {
    const text = 'javascript:alert(1) example.com [x](https://example.com) <a href="x">y</a>'
    const { container } = render(<LinkedText text={text} />)
    expect(container.textContent).toBe(text)
    expect(container.querySelector('a[href="x"]')).toBeNull()
    expect(links()).toEqual(['https://example.com'])
  })
})
