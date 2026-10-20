import { describe, it, expect, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { PageHeader } from './PageHeader'

describe('PageHeader', () => {
  it('renders the title as an h1 with the canonical list-page typography', () => {
    const { getByRole } = render(<PageHeader title="Translation memories" />)
    const heading = getByRole('heading', { level: 1 })
    expect(heading).toHaveTextContent('Translation memories')
    // Page titles use a quiet regular weight while retaining their size and color.
    expect(heading.className).toContain('text-2xl')
    expect(heading.className).toContain('font-display')
    expect(heading.className).toContain('font-normal')
    expect(heading.className).toContain('text-gray-900')
    expect(heading.className).toContain('dark:text-gray-100')
  })

  it('renders an action slot on the right when provided', () => {
    const { getByRole } = render(
      <PageHeader title="Projects" actions={<button>New project</button>} />,
    )
    expect(getByRole('button', { name: 'New project' })).toBeInTheDocument()
  })

  it('does not render a subtitle when none is provided', () => {
    const { container } = render(<PageHeader title="Templates" />)
    expect(container.querySelector('p')).toBeNull()
  })

  it('renders a subtitle under the title when provided (used by ProjectDetail)', () => {
    const { getByText } = render(
      <PageHeader title="Demo Project" subtitle="en → es, fr, de" />,
    )
    const sub = getByText('en → es, fr, de')
    expect(sub).toBeInTheDocument()
    // Pins the subtitle classes `text-sm text-gray-500 dark:text-gray-400 mt-1`
    expect(sub.className).toContain('text-sm')
    expect(sub.className).toContain('text-gray-500')
    expect(sub.className).toContain('mt-1')
  })

  it('uses the `flex items-center justify-between mb-6` shell so pages keep their vertical rhythm', () => {
    const { container } = render(<PageHeader title="X" />)
    const shell = container.firstChild as HTMLElement
    expect(shell.className).toContain('flex')
    expect(shell.className).toContain('items-center')
    expect(shell.className).toContain('justify-between')
    expect(shell.className).toContain('mb-6')
  })

  it('renders an h1 by default', () => {
    const { getByText } = render(<PageHeader title="Default level" />)
    expect(getByText('Default level').tagName).toBe('H1')
  })

  it('renders an h2 when as="h2" (sub-section usage)', () => {
    const { getByText } = render(<PageHeader title="Sub section" as="h2" />)
    expect(getByText('Sub section').tagName).toBe('H2')
  })

  it('renders an h3 when as="h3" (deeper nesting)', () => {
    const { getByText } = render(<PageHeader title="Deeper" as="h3" />)
    expect(getByText('Deeper').tagName).toBe('H3')
  })

  it('keeps the same typography classes regardless of heading level (so visual rhythm is preserved)', () => {
    const { getByText } = render(<PageHeader title="Sub section" as="h2" />)
    const heading = getByText('Sub section')
    expect(heading.className).toContain('text-2xl')
    expect(heading.className).toContain('font-display')
    expect(heading.className).toContain('font-normal')
  })

  it('renders subtitle as a ReactNode (allowing rich content like arrows)', () => {
    const { container, getByText } = render(
      <PageHeader
        title="Project"
        subtitle={<span>en <strong>→</strong> es</span>}
      />,
    )
    // Arrow is its own <strong> element — can be matched directly
    expect(getByText('→')).toBeInTheDocument()
    // "en" and "es" are text nodes inside a span; assert via normalized text
    const subtitle = container.querySelector('p')
    expect(subtitle?.textContent).toBe('en → es')
  })

  describe('browser tab title', () => {
    beforeEach(() => {
      document.title = 'OpenLoc'
    })

    it('uses a string h1 title as the tab title', () => {
      render(<PageHeader title="Translation memories" />)
      expect(document.title).toBe('Translation memories · OpenLoc')
    })

    it('does not set the tab title from a sub-section header', () => {
      render(<PageHeader title="Members" as="h2" />)
      expect(document.title).toBe('OpenLoc')
    })

    it('uses documentTitle when the title is JSX', () => {
      render(<PageHeader title={<span>New project</span>} documentTitle="New project" />)
      expect(document.title).toBe('New project · OpenLoc')
    })
  })
})
