import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { SafeHTML } from './SafeHTML'

describe('SafeHTML', () => {
  it('renders allowed formatting tags', () => {
    const { container } = render(<SafeHTML html="Hello <strong>bold</strong> <em>italic</em>" />)
    expect(container.querySelector('strong')).not.toBeNull()
    expect(container.querySelector('em')).not.toBeNull()
    expect(container.textContent).toBe('Hello bold italic')
  })

  it('renders superscript and subscript', () => {
    const { container } = render(<SafeHTML html="H<sub>2</sub>O is 10<sup>3</sup>" />)
    expect(container.querySelector('sub')).not.toBeNull()
    expect(container.querySelector('sup')).not.toBeNull()
  })

  it('strips script tags (XSS)', () => {
    const { container } = render(<SafeHTML html='Hello <script>alert("xss")</script> world' />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).toContain('Hello')
    expect(container.textContent).toContain('world')
  })

  it('strips img onerror (XSS)', () => {
    const { container } = render(<SafeHTML html='<img src=x onerror=alert(1)>' />)
    expect(container.querySelector('img')).toBeNull()
  })

  it('strips event handlers from allowed tags', () => {
    const { container } = render(<SafeHTML html='<strong onclick="alert(1)">bold</strong>' />)
    const strong = container.querySelector('strong')
    expect(strong).not.toBeNull()
    expect(strong?.getAttribute('onclick')).toBeNull()
  })

  it('strips iframe (XSS)', () => {
    const { container } = render(<SafeHTML html='<iframe src="evil.com"></iframe>' />)
    expect(container.querySelector('iframe')).toBeNull()
  })

  it('strips style attribute (CSS injection)', () => {
    const { container } = render(<SafeHTML html='<em style="background:url(evil)">text</em>' />)
    const em = container.querySelector('em')
    expect(em).not.toBeNull()
    expect(em?.getAttribute('style')).toBeNull()
  })

  it('renders empty string without error', () => {
    const { container } = render(<SafeHTML html="" />)
    expect(container.textContent).toBe('')
  })

  it('passes through plain text unchanged', () => {
    const { container } = render(<SafeHTML html="Hello world" />)
    expect(container.textContent).toBe('Hello world')
  })
})
