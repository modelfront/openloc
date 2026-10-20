import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ClearFiltersButton } from './ClearFiltersButton'

describe('ClearFiltersButton', () => {
  it('renders nothing while no filter is active', () => {
    render(<ClearFiltersButton count={0} onClear={() => {}} testId="clear" />)
    expect(screen.queryByTestId('clear')).not.toBeInTheDocument()
  })

  it('shows the count and clears on click', () => {
    const onClear = vi.fn()
    render(<ClearFiltersButton count={2} onClear={onClear} testId="clear" />)
    expect(screen.getByTestId('clear')).toHaveTextContent('Clear filters (2)')
    fireEvent.click(screen.getByTestId('clear'))
    expect(onClear).toHaveBeenCalledTimes(1)
  })
})
