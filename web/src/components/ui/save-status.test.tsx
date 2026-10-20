import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SaveStatus } from './save-status'

describe('SaveStatus', () => {
  it('says Saving... while a save runs, even right after an earlier one landed', () => {
    render(<SaveStatus saving saved />)
    expect(screen.getByRole('status')).toHaveTextContent('Saving...')
  })

  it('says Saved ✓ once the save landed', () => {
    render(<SaveStatus saving={false} saved testId="s" />)
    expect(screen.getByTestId('s')).toHaveTextContent('Saved ✓')
  })

  it('shows nothing otherwise', () => {
    const { container } = render(<SaveStatus saving={false} saved={false} />)
    expect(container).toBeEmptyDOMElement()
  })
})
