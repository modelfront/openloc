import { describe, it, expect, vi } from 'vitest'
import type { ReactNode } from 'react'
import type { ComboboxProps } from '@/components/ui/combobox'
import { render, screen, fireEvent } from '@testing-library/react'
import { TMLookupOrderField } from './TMLookupOrderField'

vi.mock('@/components/ui/tooltip', () => ({
  Tooltip: ({ children }: { children?: ReactNode }) => children,
  TooltipTrigger: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
  TooltipContent: ({ children }: { children?: ReactNode }) => children,
  TooltipProvider: ({ children }: { children?: ReactNode }) => children,
}))

// The real Combobox is a cmdk popover; a native select keeps the value and
// options observable.
vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, disabled, 'data-testid': testId, 'aria-label': ariaLabel }: ComboboxProps) => (
    <select data-testid={testId} aria-label={ariaLabel} value={value} disabled={disabled} onChange={e => onChange(e.target.value)}>
      {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  ),
}))

const select = () => screen.getByTestId('tm-lookup-order-select')

describe('TMLookupOrderField', () => {
  it('shows the stored order and commits the other one', () => {
    const onCommit = vi.fn()
    render(<TMLookupOrderField value="match_percent" tmCount={2} onCommit={onCommit} />)
    expect(select()).toHaveValue('match_percent')
    expect(screen.getByRole('option', { name: 'By match percentage' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'By TM order' })).toBeInTheDocument()
    fireEvent.change(select(), { target: { value: 'tm_order' } })
    expect(onCommit).toHaveBeenCalledWith({ tm_lookup_order: 'tm_order' })
  })

  it('re-picking the current order commits nothing', () => {
    const onCommit = vi.fn()
    render(<TMLookupOrderField value="tm_order" tmCount={2} onCommit={onCommit} />)
    fireEvent.change(select(), { target: { value: 'tm_order' } })
    expect(onCommit).not.toHaveBeenCalled()
  })

  it.each([0, 1])('with %i TM(s) the field is not shown', count => {
    render(<TMLookupOrderField value="match_percent" tmCount={count} onCommit={vi.fn()} />)
    expect(screen.queryByTestId('tm-lookup-order-field')).toBeNull()
    expect(screen.queryByTestId('tm-lookup-order-select')).toBeNull()
  })

  it('appears at two TMs and shows the chosen order\'s hint', () => {
    render(<TMLookupOrderField value="tm_order" tmCount={2} onCommit={vi.fn()} />)
    expect(select()).not.toBeDisabled()
    expect(screen.getByText('A fuzzy match from a higher TM wins over an exact match from a lower TM.')).toBeInTheDocument()
  })

  it('readOnly (a linked profile) locks the dropdown even with several TMs', () => {
    render(<TMLookupOrderField value="match_percent" tmCount={3} readOnly onCommit={vi.fn()} />)
    expect(select()).toBeDisabled()
  })
})
