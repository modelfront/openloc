import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import TMProfileDetail from './TMProfileDetail'
import { ApiError, getTMProfile, updateTMProfile } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  getTMProfile: vi.fn(),
  updateTMProfile: vi.fn(),
  setTMProfileTMs: vi.fn(),
  listTMCollections: vi.fn().mockResolvedValue([]),
}))
vi.mock('../components/settings/useInlineCreateResources', () => ({
  useInlineCreateResources: () => ({ handleCreateNewTM: vi.fn(), dialogs: null }),
}))

const profile = {
  id: 7, name: 'Acme', description: '', usage_count: 1,
  fuzzy_threshold: 0.75, sublanguage_penalty_pct: 1, near_exact_penalty_pct: 1,
  tm_lookup_order: 'priority', tm_assignments: [],
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/memories/profiles/7']}>
        <Routes><Route path="/memories/profiles/:profileId" element={<TMProfileDetail />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getTMProfile).mockResolvedValue(profile as unknown as Awaited<ReturnType<typeof getTMProfile>>)
})

describe('TMProfileDetail scoring fields', () => {
  it('a refused save puts the stored value back, and the same value can be saved again', async () => {
    vi.mocked(updateTMProfile)
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue({} as Awaited<ReturnType<typeof updateTMProfile>>)
    renderPage()
    const field = await screen.findByLabelText('Near-exact penalty')

    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(updateTMProfile).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(field).toHaveValue(1))

    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(updateTMProfile).toHaveBeenCalledTimes(2))
    expect(updateTMProfile).toHaveBeenLastCalledWith(7, { near_exact_penalty_pct: 3 })
  })
})

describe('TMProfileDetail load', () => {
  it('a failed load shows the error with a Retry that loads the profile', async () => {
    vi.mocked(getTMProfile)
      .mockRejectedValueOnce(new ApiError(500, 'server unavailable'))
      .mockResolvedValue(profile as unknown as Awaited<ReturnType<typeof getTMProfile>>)
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the TM profile: server unavailable")
    expect(screen.queryByText('TM profile not found.')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByLabelText('Near-exact penalty')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a 404 says the profile was not found, with no Retry', async () => {
    vi.mocked(getTMProfile).mockRejectedValue(new ApiError(404, 'not found'))
    renderPage()

    expect(await screen.findByText('TM profile not found.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
