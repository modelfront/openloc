import { useState } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TMProfilesList } from './TMProfilesList'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listTMProfiles = vi.fn()
vi.mock('../../api', () => ({
  listTMProfiles: (...a: unknown[]) => listTMProfiles(...a),
  createTMProfile: vi.fn(),
  deleteTMProfile: vi.fn(),
}))

// The page owns the search text; this stands in for it.
function Page({ initialSearch }: { initialSearch: string }) {
  const [search, setSearch] = useState(initialSearch)
  return <TMProfilesList search={search} onSearchChange={setSearch} canDelete={false} createOpen={false} onCreateClose={vi.fn()} />
}

function renderList(initialSearch = '') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Page initialSearch={initialSearch} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const profile = (id: number, name: string) =>
  ({ id, name, description: '', tm_count: 1, fuzzy_threshold: 0.75, usage_count: 0 })

const shownIds = () => screen.queryAllByTestId(/^tm-profile-row-/).map(el => el.getAttribute('data-testid'))

beforeEach(() => {
  vi.clearAllMocks()
})

describe('TMProfilesList', () => {
  it('a failed load shows the error with a Retry, not "No TM profiles yet"', async () => {
    const user = userEvent.setup()
    listTMProfiles.mockRejectedValueOnce(new Error('server unavailable'))
    renderList()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the TM profiles: server unavailable")
    expect(screen.queryByText(/No TM profiles yet/)).not.toBeInTheDocument()

    listTMProfiles.mockResolvedValueOnce([
      { id: 4, name: 'Legal', description: '', tm_count: 2, fuzzy_threshold: 0.75, usage_count: 0 },
    ])
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('tm-profile-row-4')).toHaveTextContent('Legal')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('TMProfilesList name search', () => {
  beforeEach(() => {
    listTMProfiles.mockResolvedValue([profile(1, 'Legal default'), profile(2, 'Marketing')])
  })

  it('typing narrows the profiles by name', async () => {
    renderList()
    await screen.findByText('Marketing')

    fireEvent.change(screen.getByTestId('tm-profile-list-search'), { target: { value: 'legal' } })

    expect(shownIds()).toEqual(['tm-profile-row-1'])
  })

  it('a search with no match says so instead of "no profiles yet"', async () => {
    renderList('nothing')

    expect(await screen.findByText('No TM profiles match the current filter.')).toBeInTheDocument()
    expect(screen.queryByText(/No TM profiles yet/)).not.toBeInTheDocument()
  })
})
