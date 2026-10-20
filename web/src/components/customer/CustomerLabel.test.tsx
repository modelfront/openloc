// CustomerLabel.
// Renders 3 states based on `id` + the cached customers list:
//   - id null/undefined        → italic muted "None"
//   - id matches a live customer → name (gray)
//   - id set, customers loaded but no match → "(deleted)" (rose)
//
// Tests exercise observable text + className signals so a future change to
// the deleted-FK pattern (color, copy) gets caught.

import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CustomerLabel } from './CustomerLabel'
import type { Customer } from '../../api'

afterEach(() => cleanup())

function setup(opts: { customers: Customer[] | undefined; id: number | null | undefined }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  // Pre-seed the customers cache. Setting undefined leaves it loading.
  if (opts.customers !== undefined) {
    qc.setQueryData(['customers'], opts.customers)
  }
  return render(
    <QueryClientProvider client={qc}>
      <CustomerLabel id={opts.id} />
    </QueryClientProvider>,
  )
}

const ACME: Customer = {
  id: 1, tenant_id: 1, name: 'Acme Corp', created_at: '2026-01-01', updated_at: '2026-01-01', deleted_at: null,
}

describe('CustomerLabel', () => {
  it('renders italic "None" when id is null', () => {
    setup({ customers: [ACME], id: null })
    const el = screen.getByText('None')
    expect(el).toBeInTheDocument()
    expect(el.className).toMatch(/italic/)
    expect(el.className).toMatch(/text-gray/)
  })

  it('renders italic "None" when id is undefined', () => {
    setup({ customers: [ACME], id: undefined })
    expect(screen.getByText('None')).toBeInTheDocument()
  })

  it('renders the customer name when id matches a live customer', () => {
    setup({ customers: [ACME], id: 1 })
    const el = screen.getByTestId('customer-label-1')
    expect(el).toHaveTextContent('Acme Corp')
    // Color: muted gray, NOT italic, NOT rose
    expect(el.className).not.toMatch(/italic/)
    expect(el.className).not.toMatch(/rose/)
  })

  it('renders italic rose "(deleted)" when id is set but customers list excludes it', () => {
    // The backend's listCustomers query already filters out soft-deleted
    // rows, so a project's customer_id pointing at a row not in the list
    // means the customer was tombstoned after the project linked.
    setup({ customers: [ACME], id: 999 })
    const el = screen.getByTestId('customer-label-deleted-999')
    expect(el).toHaveTextContent('(deleted)')
    expect(el.className).toMatch(/italic/)
    expect(el.className).toMatch(/rose/)
    // Tooltip steers the admin toward the fix path.
    expect(el).toHaveAttribute('title', expect.stringMatching(/deleted/i))
  })

  it('renders a quiet placeholder ("…") while customers query is still loading', () => {
    // While loading, we don't know whether the id is "live" or "deleted" —
    // showing "(deleted)" prematurely would scare the admin into re-picking
    // a customer that's actually fine. Hide that state behind a placeholder.
    setup({ customers: undefined, id: 5 })
    expect(screen.getByText('…')).toBeInTheDocument()
    // Should NOT show the "(deleted)" indicator during loading.
    expect(screen.queryByText('(deleted)')).not.toBeInTheDocument()
  })

  it('respects the `emptyText` prop override', () => {
    setup({ customers: [ACME], id: null })
    cleanup()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(['customers'], [ACME])
    render(
      <QueryClientProvider client={qc}>
        <CustomerLabel id={null} emptyText="—" />
      </QueryClientProvider>,
    )
    expect(screen.getByText('—')).toBeInTheDocument()
  })
})
