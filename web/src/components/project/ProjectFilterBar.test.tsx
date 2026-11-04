import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { useEffect } from 'react'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../../api')>()),
  listLabels: () => Promise.resolve([]),
  listLanguages: () => Promise.resolve([{ code: 'en', name: 'English' }, { code: 'fr', name: 'French' }]),
  listProjectCreators: () => Promise.resolve([]),
}))

import { ProjectFilterBar } from './ProjectFilterBar'

// Another component leaves the page in the same render, the way a guided tour
// started from ?tour= does.
function LeaveAtOnce() {
  const navigate = useNavigate()
  useEffect(() => { navigate('/ai/prompts') }, [navigate])
  return null
}

function Where() {
  const loc = useLocation()
  return <p data-testid="where">{loc.pathname + loc.search}</p>
}

describe('ProjectFilterBar', () => {
  it('does not write the URL on mount, so it never pulls the page back', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects?tour=tour-ai-prompts']}>
          <Routes>
            <Route path="/projects" element={<><LeaveAtOnce /><ProjectFilterBar myUserId={1} /></>} />
            <Route path="/ai/prompts" element={<Where />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('where')).toBeInTheDocument())
    // Past the search debounce.
    await new Promise(r => setTimeout(r, 400))
    expect(screen.getByTestId('where')).toHaveTextContent('/ai/prompts')
  })

  it('Clear filters clears the search too, and it stays cleared', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects?q=acme&status=draft']}>
          <Routes>
            <Route path="/projects" element={<><ProjectFilterBar myUserId={1} /><Where /></>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByTestId('project-search')).toHaveValue('acme')

    fireEvent.click(screen.getByTestId('filter-clear-all'))
    // Past the search debounce.
    await new Promise(r => setTimeout(r, 400))

    expect(screen.getByTestId('where')).toHaveTextContent(/^\/projects$/)
    expect(screen.getByTestId('project-search')).toHaveValue('')
  })

  it('Deadline takes several bands, keeps them in filter order, and All clears them', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects']}>
          <Routes>
            <Route path="/projects" element={<><ProjectFilterBar myUserId={1} /><Where /></>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    fireEvent.click(screen.getByTestId('filter-deadline'))
    fireEvent.click(await screen.findByTestId('filter-deadline-approaching'))
    // The popover stays open for the next pick.
    fireEvent.click(screen.getByTestId('filter-deadline-overdue'))
    expect(screen.getByTestId('where')).toHaveTextContent('/projects?deadline=overdue%2Capproaching')
    expect(screen.getByTestId('filter-deadline')).toHaveTextContent('Overdue, Due within 24h')
    expect(screen.getByTestId('filter-deadline-overdue')).toHaveAttribute('aria-pressed', 'true')

    // Unticking one keeps the other.
    fireEvent.click(screen.getByTestId('filter-deadline-overdue'))
    expect(screen.getByTestId('where')).toHaveTextContent('/projects?deadline=approaching')
    expect(screen.getByTestId('filter-deadline-overdue')).toHaveAttribute('aria-pressed', 'false')

    fireEvent.click(screen.getByTestId('filter-deadline-all'))
    expect(screen.getByTestId('where')).toHaveTextContent(/^\/projects$/)
    expect(screen.getByTestId('filter-deadline')).toHaveTextContent('Deadline:All')
  })

  // Status, Deadline and Created by are always on the bar; Customer, Source
  // and Target wait behind the filter icon and show a chip only while set.
  it('a filter picked from the icon shows as a chip before the icon, and its × takes it off', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects']}>
          <Routes>
            <Route path="/projects" element={<><ProjectFilterBar myUserId={1} /><Where /></>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const bar = screen.getByTestId('project-filter-bar')
    const order = () => [...bar.children].map(c => c.getAttribute('data-testid') ?? c.querySelector('[data-testid]')?.getAttribute('data-testid'))
    expect(order()).toEqual(['project-search', 'filter-status', 'filter-deadline', 'filter-created-by', 'project-filter-menu'])

    fireEvent.click(screen.getByTestId('project-filter-menu'))
    expect(screen.getByTestId('project-filter-menu-option-customer')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('project-filter-menu-option-source'))
    fireEvent.click(await screen.findByRole('button', { name: /French/ }))

    expect(screen.getByTestId('where')).toHaveTextContent('/projects?source=fr')
    expect(screen.getByTestId('filter-source')).toHaveTextContent('Source:FR')
    expect(order()).toEqual([
      'project-search', 'filter-status', 'filter-deadline', 'filter-created-by', 'filter-source', 'project-filter-menu', 'filter-clear-all',
    ])

    fireEvent.click(screen.getByTestId('filter-source-remove'))
    expect(screen.getByTestId('where')).toHaveTextContent(/^\/projects$/)
    expect(screen.queryByTestId('filter-source')).not.toBeInTheDocument()
  })

  it('searches inside a list from data, not inside Status or Deadline', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects']}>
          <Routes>
            <Route path="/projects" element={<ProjectFilterBar myUserId={1} />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    fireEvent.click(screen.getByTestId('filter-status'))
    expect(await screen.findByText('In progress')).toBeInTheDocument()
    expect(screen.queryByTestId('filter-status-search')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('filter-deadline'))
    expect(await screen.findByTestId('filter-deadline-overdue')).toBeInTheDocument()
    expect(screen.queryByTestId('filter-deadline-search')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('filter-created-by'))
    expect(await screen.findByTestId('filter-created-by-search')).toBeInTheDocument()

    // The icon's menu searches the picked filter's options too.
    fireEvent.click(screen.getByTestId('project-filter-menu'))
    fireEvent.click(screen.getByTestId('project-filter-menu-option-target'))
    const box = await screen.findByTestId('project-filter-menu-search')
    fireEvent.change(box, { target: { value: 'eng' } })
    expect(screen.getByRole('button', { name: /English/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /French/ })).not.toBeInTheDocument()
  })
})
