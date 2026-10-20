import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
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

function renderList() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TMProfilesList canDelete={false} createOpen={false} onCreateClose={vi.fn()} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

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
