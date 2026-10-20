import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../../api')>()),
  listTaskSteps: () => Promise.resolve(['Review', 'Translation']),
}))

import { TaskFilterBar } from './TaskFilterBar'

function Where() {
  const loc = useLocation()
  return <p data-testid="where">{loc.search}</p>
}

function renderBar(url = '/tasks') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <TaskFilterBar />
        <Where />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('TaskFilterBar', () => {
  it('picking a step writes ?step=', async () => {
    renderBar()
    fireEvent.click(screen.getByTestId('filter-step'))
    fireEvent.click(await screen.findByText('Review'))
    expect(screen.getByTestId('where')).toHaveTextContent('?step=Review')
    expect(screen.getByTestId('filter-step')).toHaveTextContent('Step:Review')
  })

  it('picking a deadline bucket writes ?deadline=, and All removes it', async () => {
    renderBar('/tasks?step=Review')
    fireEvent.click(screen.getByTestId('filter-deadline'))
    fireEvent.click(await screen.findByText('Due within 24h'))
    expect(screen.getByTestId('where')).toHaveTextContent('?step=Review&deadline=24h')

    fireEvent.click(screen.getByTestId('filter-deadline'))
    fireEvent.click(await screen.findByText('All'))
    expect(screen.getByTestId('where')).toHaveTextContent(/^\?step=Review$/)
  })

  it('shows no Clear button without filters', () => {
    renderBar()
    expect(screen.queryByTestId('filter-clear-all')).not.toBeInTheDocument()
  })

  it('Clear filters removes every filter, the search included', async () => {
    renderBar('/tasks?q=manual&step=Review&deadline=overdue')
    expect(screen.getByTestId('filter-clear-all')).toHaveTextContent('Clear filters (3)')

    fireEvent.click(screen.getByTestId('filter-clear-all'))
    // Past the search debounce, so a stale search can't come back.
    await new Promise(r => setTimeout(r, 400))

    expect(screen.getByTestId('where')).toBeEmptyDOMElement()
    expect(screen.getByTestId('task-search')).toHaveValue('')
    await waitFor(() => expect(screen.queryByTestId('filter-clear-all')).not.toBeInTheDocument())
  })
})
