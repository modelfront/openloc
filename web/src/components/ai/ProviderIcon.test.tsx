import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ProviderIcon } from './ProviderIcon'

describe('ProviderIcon for a custom provider', () => {
  it('shows the puzzle piece until the icon loads, then the icon', () => {
    const { container } = render(<ProviderIcon provider="ext:acme-qe" />)
    const badge = screen.getByTestId('extension-icon-acme-qe')
    const img = badge.querySelector('img')!
    expect(img).toHaveAttribute('src', '/api/ai/extensions/acme-qe/icon')
    expect(img).toHaveClass('hidden')
    expect(container.querySelector('svg')).not.toBeNull()

    fireEvent.load(img)
    expect(img).not.toHaveClass('hidden')
    expect(container.querySelector('svg')).toBeNull()
  })

  it('keeps the puzzle piece when the extension has no icon', () => {
    const { container } = render(<ProviderIcon provider="ext:acme-qe" />)
    const img = screen.getByTestId('extension-icon-acme-qe').querySelector('img')!
    fireEvent.error(img)
    expect(img).toHaveClass('hidden')
    expect(container.querySelector('svg')).not.toBeNull()
  })
})
