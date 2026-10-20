import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TextLink } from './text-link'

describe('TextLink', () => {
  it('is a router link with `to`, an anchor with `href`, a button with `onClick`', () => {
    const onClick = vi.fn()
    render(
      <MemoryRouter>
        <TextLink to="/projects">route</TextLink>
        <TextLink href="https://example.com" target="_blank" rel="noopener noreferrer">outside</TextLink>
        <TextLink onClick={onClick}>action</TextLink>
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: 'route' })).toHaveAttribute('href', '/projects')
    expect(screen.getByRole('link', { name: 'outside' })).toHaveAttribute('href', 'https://example.com')
    const button = screen.getByRole('button', { name: 'action' })
    expect(button).toHaveAttribute('type', 'button')
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it("sets no size or weight of its own, so it takes the surrounding text's", () => {
    render(<TextLink onClick={() => {}}>action</TextLink>)
    const cls = screen.getByRole('button').className
    expect(cls).toMatch(/text-primary/)
    expect(cls).not.toMatch(/text-(xs|sm|base|lg|\[)/)
    expect(cls).not.toMatch(/font-/)
  })

  it('tone swaps the colour and nothing else', () => {
    render(
      <>
        <TextLink tone="muted" onClick={() => {}}>quiet</TextLink>
        <TextLink tone="inherit" onClick={() => {}}>banner</TextLink>
      </>,
    )
    const muted = screen.getByRole('button', { name: 'quiet' }).className
    expect(muted).toMatch(/text-gray-500/)
    expect(muted).not.toMatch(/text-primary/)
    expect(screen.getByRole('button', { name: 'banner' }).className).toMatch(/text-current/)
  })

  it('underlined keeps the underline at rest instead of only on hover', () => {
    render(<TextLink underlined onClick={() => {}}>running text</TextLink>)
    const cls = screen.getByRole('button').className.split(' ')
    expect(cls).toContain('underline')
    expect(cls).not.toContain('hover:underline')
  })

  it('the button form shows the pointer cursor, like the link forms', () => {
    render(<TextLink onClick={() => {}}>Back to Acme</TextLink>)
    expect(screen.getByRole('button').className.split(' ')).toContain('cursor-pointer')
  })
})
