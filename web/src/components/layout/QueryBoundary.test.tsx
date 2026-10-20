import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { QueryBoundary } from './QueryBoundary'

afterEach(cleanup)

function Card({ fetchRates }: { fetchRates: () => Promise<string[]> }) {
  const query = useQuery({ queryKey: ['rates'], queryFn: fetchRates })
  return (
    <>
      <button type="button" onClick={() => void query.refetch()}>Background refetch</button>
      <span data-testid="query-status">{query.status}</span>
      <QueryBoundary query={query} what="analysis weights">
        {rates => <ul>{rates.map(r => <li key={r}>{r}</li>)}</ul>}
      </QueryBoundary>
    </>
  )
}

function renderCard(fetchRates: () => Promise<string[]>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Card fetchRates={fetchRates} />
    </QueryClientProvider>,
  )
}

describe('QueryBoundary', () => {
  it('shows the loading placeholder while the first fetch runs, and no content or error', async () => {
    renderCard(() => new Promise<string[]>(() => {}))

    expect(await screen.findByRole('status', {}, { timeout: 2000 })).toHaveTextContent('Loading...')
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('renders children with the data', async () => {
    renderCard(() => Promise.resolve(['en → de', 'en → fr']))

    const items = await screen.findAllByRole('listitem')
    expect(items.map(i => i.textContent)).toEqual(['en → de', 'en → fr'])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a failed load names what failed with the server message, and Retry refetches', async () => {
    const fetchRates = vi.fn<() => Promise<string[]>>()
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue(['en → de'])
    renderCard(fetchRates)

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the analysis weights: server unavailable")
    expect(screen.queryByRole('list')).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByRole('listitem')).toHaveTextContent('en → de')
    expect(fetchRates).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a failed background refetch keeps the loaded content', async () => {
    const fetchRates = vi.fn<() => Promise<string[]>>()
      .mockResolvedValueOnce(['en → de'])
      .mockRejectedValue(new Error('server unavailable'))
    renderCard(fetchRates)
    expect(await screen.findByRole('listitem')).toHaveTextContent('en → de')

    await userEvent.click(screen.getByRole('button', { name: 'Background refetch' }))

    await waitFor(() => expect(screen.getByTestId('query-status')).toHaveTextContent('error'))
    expect(fetchRates).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('listitem')).toHaveTextContent('en → de')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
