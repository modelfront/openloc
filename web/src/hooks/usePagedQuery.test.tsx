import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { usePagedQuery, type ListPage } from './usePagedQuery'

const ROWS = Array.from({ length: 23 }, (_, i) => `row ${i + 1}`)
const list = vi.fn<(offset: number, limit: number) => Promise<ListPage<string>>>()

function Probe() {
  const paged = usePagedQuery({
    pageKey: 'xpage',
    queryKey: page => ['probe', page],
    queryFn: list,
  })
  const { search } = useLocation()
  return (
    <>
      <output data-testid="search">{search}</output>
      <output data-testid="state">{`page ${paged.page} of ${paged.pageCount}`}</output>
      <ul>{paged.rows.map(r => <li key={r}>{r}</li>)}</ul>
      <button type="button" onClick={() => paged.goToPage(3)}>page 3</button>
      <button type="button" onClick={() => paged.goToPage(1)}>page 1</button>
    </>
  )
}

function renderAt(url: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}><Probe /></MemoryRouter>
    </QueryClientProvider>,
  )
}

const search = () => screen.getByTestId('search').textContent

beforeEach(() => {
  list.mockReset()
  list.mockImplementation((offset, limit) => {
    const data = ROWS.slice(offset, offset + limit)
    // Like the server: no total on a page past the end.
    return Promise.resolve(data.length > 0 ? { data, total: ROWS.length } : { data })
  })
})

describe('usePagedQuery', () => {
  it('reads the page from the URL and asks for its rows, ten a page', async () => {
    renderAt('/x?q=a&xpage=2')
    expect(await screen.findByText('row 11')).toBeInTheDocument()
    expect(list).toHaveBeenCalledWith(10, 10)
    expect(screen.getByTestId('state')).toHaveTextContent('page 2 of 3')
  })

  it('treats a missing or broken page number as page 1', async () => {
    renderAt('/x?xpage=abc')
    expect(await screen.findByText('row 1')).toBeInTheDocument()
    expect(list).toHaveBeenCalledWith(0, 10)
  })

  it('moves between pages in the URL, and page 1 drops the param', async () => {
    renderAt('/x?q=a')
    await screen.findByText('row 1')
    fireEvent.click(screen.getByText('page 3'))
    expect(await screen.findByText('row 21')).toBeInTheDocument()
    expect(search()).toBe('?q=a&xpage=3')
    fireEvent.click(screen.getByText('page 1'))
    expect(await screen.findByText('row 1')).toBeInTheDocument()
    expect(search()).toBe('?q=a')
  })

  it('goes back to page 1 from a page past the end', async () => {
    renderAt('/x?q=a&xpage=9')
    expect(await screen.findByText('row 1')).toBeInTheDocument()
    await waitFor(() => expect(search()).toBe('?q=a'))
    expect(list).toHaveBeenCalledWith(80, 10)
  })
})
