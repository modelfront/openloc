import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import CustomersPage from './CustomersPage'
import { listCustomers } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  listCustomers: vi.fn(),
}))

type Customers = Awaited<ReturnType<typeof listCustomers>>
const acme = { id: 1, name: 'Acme Corp', created_at: '2026-01-01T00:00:00Z' } as unknown as Customers[number]

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <CustomersPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => { vi.clearAllMocks() })

// The page stands in for every list built on useCRUDList: they all render the
// hook's `loadFailed` the same way.
describe('CustomersPage list load', () => {
  it('a failed list request shows the error with a Retry that reloads the list', async () => {
    vi.mocked(listCustomers)
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue([acme])
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the customers: server unavailable")
    // Not "no customers yet": the list is unknown, not empty.
    expect(screen.queryByText(/No customers yet/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('Acme Corp')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(listCustomers).toHaveBeenCalledTimes(2)
  })

  it('an empty list shows the empty state and no error', async () => {
    vi.mocked(listCustomers).mockResolvedValue([])
    renderPage()

    expect(await screen.findByText(/No customers yet/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
