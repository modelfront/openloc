import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { EmptyState } from './EmptyState'

describe('EmptyState', () => {
  it('renders the message', () => {
    const { getByText } = render(<EmptyState message="No projects yet." />)
    expect(getByText('No projects yet.')).toBeInTheDocument()
  })

  it('uses the canonical muted + centered + spacing classes', () => {
    const { getByText } = render(<EmptyState message="No TMs yet." />)
    const el = getByText('No TMs yet.')
    // Pins the empty-state classes `text-gray-500 dark:text-gray-400 text-center py-8`
    expect(el.className).toContain('text-gray-500')
    expect(el.className).toContain('dark:text-gray-400')
    expect(el.className).toContain('text-center')
    expect(el.className).toContain('py-8')
  })

  it('renders as a <p> so it drops into inline text flow', () => {
    const { getByText } = render(<EmptyState message="Nothing here." />)
    expect(getByText('Nothing here.').tagName).toBe('P')
  })

  it('allows callers to append extra classes', () => {
    const { getByText } = render(
      <EmptyState message="Nothing here." className="mt-4" />,
    )
    const el = getByText('Nothing here.')
    expect(el.className).toContain('mt-4')
    expect(el.className).toContain('text-center')
  })

  it('renders rich message content (e.g. bolded action word)', () => {
    const { getByText, container } = render(
      <EmptyState message={<>No files yet. <strong>Upload one.</strong></>} />,
    )
    expect(getByText('Upload one.').tagName).toBe('STRONG')
    expect(container.querySelector('p')?.textContent).toBe('No files yet. Upload one.')
  })
})
