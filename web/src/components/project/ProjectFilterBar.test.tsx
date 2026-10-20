import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { useEffect } from 'react'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../../api')>()),
  listLabels: () => Promise.resolve([]),
  listLanguages: () => Promise.resolve([]),
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
})
