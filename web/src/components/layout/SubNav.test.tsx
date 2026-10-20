import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { SubNav } from './SubNav'

const items = [
  { to: '/workflows', label: 'Workflows', end: true },
  { to: '/workflows/steps', label: 'Steps' },
]

describe('SubNav', () => {
  it('marks the current route active and the index route only on exact match', () => {
    render(<MemoryRouter initialEntries={['/workflows/steps']}><SubNav items={items} /></MemoryRouter>)
    expect(screen.getByRole('link', { name: 'Steps' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Workflows' })).not.toHaveAttribute('aria-current')
  })
  it('links carry their routes', () => {
    render(<MemoryRouter initialEntries={['/workflows']}><SubNav items={items} /></MemoryRouter>)
    expect(screen.getByRole('link', { name: 'Workflows' })).toHaveAttribute('href', '/workflows')
    expect(screen.getByRole('link', { name: 'Workflows' })).toHaveAttribute('aria-current', 'page')
  })
  it('a disabled item has no link and cannot be followed', async () => {
    const withDisabled = [...items, { to: '/workflows/runs', label: 'Runs', disabled: true }]
    function Where() { return <span data-testid="where">{useLocation().pathname}</span> }
    render(
      <MemoryRouter initialEntries={['/workflows']}>
        <SubNav items={withDisabled} />
        <Routes><Route path="*" element={<Where />} /></Routes>
      </MemoryRouter>,
    )
    const runs = screen.getByText('Runs')
    expect(runs).toHaveAttribute('aria-disabled', 'true')
    expect(runs).not.toHaveAttribute('href')
    await userEvent.click(runs)
    expect(screen.getByTestId('where')).toHaveTextContent('/workflows')
    // Without a reason there is nothing to explain, so it takes no focus.
    expect(runs).not.toHaveAttribute('tabindex')
  })
  it('a disabled item with a reason is focusable and explains itself', async () => {
    const withReason = [...items, { to: '/workflows/runs', label: 'Runs', disabled: true, disabledReason: 'Run a workflow first.' }]
    render(<MemoryRouter initialEntries={['/workflows']}><SubNav items={withReason} /></MemoryRouter>)
    const runs = screen.getByText('Runs')
    expect(runs).toHaveAttribute('tabindex', '0')
    await userEvent.hover(runs)
    expect(await screen.findByText('Run a workflow first.')).toBeInTheDocument()
  })
  it('renders actions in the row after the items', () => {
    render(
      <MemoryRouter initialEntries={['/workflows']}>
        <SubNav items={items} actions={<button type="button">Reset</button>} />
      </MemoryRouter>,
    )
    const nav = screen.getByRole('navigation', { name: 'Section' })
    const reset = within(nav).getByRole('button', { name: 'Reset' })
    expect(within(nav).getByRole('link', { name: 'Steps' }).compareDocumentPosition(reset))
      .toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  })
})
