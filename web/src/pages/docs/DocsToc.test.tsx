import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DocsToc } from './DocsToc'

const md = '## Open the editor\n\ntext\n\n## Confirm a segment\n\ntext\n\n### Tags\n\ntext'

// Renders the TOC inside a fake scroll container with the headings it points
// at, each heading reporting its own top edge relative to the container.
function renderWithHeadings(tops: Record<string, number>) {
  const utils = render(
    <div data-testid="docs-content">
      {Object.keys(tops).map(id => <h2 key={id} id={id}>{id}</h2>)}
      <DocsToc markdown={md} />
    </div>,
  )
  const container = screen.getByTestId('docs-content')
  vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ top: 0 } as DOMRect)
  for (const [id, top] of Object.entries(tops)) {
    vi.spyOn(document.getElementById(id)!, 'getBoundingClientRect').mockReturnValue({ top } as DOMRect)
  }
  return { ...utils, container }
}

afterEach(() => vi.restoreAllMocks())

describe('DocsToc', () => {
  it('lists h2 and h3 headings with anchor links', () => {
    renderWithHeadings({ 'open-the-editor': 10, 'confirm-a-segment': 500, tags: 900 })
    const nav = screen.getByRole('navigation', { name: 'On this page' })
    expect(nav).toHaveTextContent('Open the editor')
    expect(screen.getByRole('link', { name: 'Tags' })).toHaveAttribute('href', '#tags')
  })

  it('highlights the last heading that has scrolled past the top of the content pane', async () => {
    const { container } = renderWithHeadings({ 'open-the-editor': -300, 'confirm-a-segment': 40, tags: 700 })
    act(() => {
      container.dispatchEvent(new Event('scroll'))
    })
    // "Confirm a segment" sits 40px below the pane's top — within the
    // activation band — so it is current; "Tags" is still far below. The
    // update runs on the next animation frame.
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Confirm a segment' })).toHaveAttribute('aria-current', 'location'),
    )
    expect(screen.getByRole('link', { name: 'Open the editor' })).not.toHaveAttribute('aria-current')
    expect(screen.getByRole('link', { name: 'Tags' })).not.toHaveAttribute('aria-current')
  })

  it('keeps a clicked entry active through the jump, until the reader scrolls by hand', async () => {
    // "Tags" sits too low to ever reach the activation band.
    const { container } = renderWithHeadings({ 'open-the-editor': 10, 'confirm-a-segment': 500, tags: 900 })
    const tags = screen.getByRole('link', { name: 'Tags' })
    act(() => tags.click())
    expect(tags).toHaveAttribute('aria-current', 'location')
    // The jump the click causes fires plain scroll events: the pin holds.
    act(() => {
      container.dispatchEvent(new Event('scroll'))
    })
    await new Promise(r => requestAnimationFrame(r))
    expect(tags).toHaveAttribute('aria-current', 'location')
    // A wheel turn is the reader scrolling: the position decides again.
    act(() => {
      container.dispatchEvent(new Event('wheel'))
    })
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Open the editor' })).toHaveAttribute('aria-current', 'location'),
    )
  })

  it('releases a clicked entry when the reader grabs the scrollbar or presses a key anywhere', async () => {
    const { container } = renderWithHeadings({ 'open-the-editor': 10, 'confirm-a-segment': 500, tags: 900 })
    const tags = screen.getByRole('link', { name: 'Tags' })
    const first = screen.getByRole('link', { name: 'Open the editor' })

    act(() => tags.click())
    act(() => {
      container.dispatchEvent(new Event('pointerdown'))
    })
    await waitFor(() => expect(first).toHaveAttribute('aria-current', 'location'))

    act(() => tags.click())
    act(() => {
      document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'PageDown', bubbles: true }))
    })
    await waitFor(() => expect(first).toHaveAttribute('aria-current', 'location'))
  })

  it('makes the last heading active at the bottom of the pane', async () => {
    const { container } = renderWithHeadings({ 'open-the-editor': -300, 'confirm-a-segment': 40, tags: 700 })
    Object.defineProperties(container, {
      scrollTop: { configurable: true, value: 600 },
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1000 },
    })
    act(() => {
      container.dispatchEvent(new Event('scroll'))
    })
    await waitFor(() => expect(screen.getByRole('link', { name: 'Tags' })).toHaveAttribute('aria-current', 'location'))
  })

  it('starts on the heading named in the URL hash', () => {
    window.history.replaceState(null, '', '#tags')
    renderWithHeadings({ 'open-the-editor': 10, 'confirm-a-segment': 500, tags: 900 })
    expect(screen.getByRole('link', { name: 'Tags' })).toHaveAttribute('aria-current', 'location')
    window.history.replaceState(null, '', window.location.pathname)
  })

  it('falls back to the first heading before any scrolling', () => {
    renderWithHeadings({ 'open-the-editor': 200, 'confirm-a-segment': 600, tags: 900 })
    expect(screen.getByRole('link', { name: 'Open the editor' })).toHaveAttribute('aria-current', 'location')
  })

  it('shows a page with a single heading', () => {
    render(<DocsToc markdown={'## Only one'} />)
    expect(screen.getByRole('link', { name: 'Only one' })).toHaveAttribute('href', '#only-one')
  })

  it('renders nothing for a page without headings', () => {
    render(<DocsToc markdown={'Just text.'} />)
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })
})
