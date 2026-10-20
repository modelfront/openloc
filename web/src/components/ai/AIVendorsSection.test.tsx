import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AIVendorsSection } from './AIVendorsSection'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))

const listAIVendors = vi.fn()
const listMTEngineCatalog = vi.fn()
vi.mock('../../api', () => ({
  listAIVendors: () => listAIVendors(),
  listMTEngineCatalog: () => listMTEngineCatalog(),
  createAIConnection: vi.fn(), updateAIConnection: vi.fn(),
  uploadExtension: vi.fn(), deleteExtension: vi.fn(),
  createVendorToken: vi.fn(), updateVendorToken: vi.fn(), deleteVendorToken: vi.fn(),
  listEnginesUsingKey: vi.fn(), listKeylessEngines: vi.fn(), attachKeyToEngines: vi.fn(),
}))

const OPENAI = {
  provider: 'openai', connected: true, models: [],
  connections: [{ id: 5, provider: 'openai', name: 'OpenAI', base_url: '', token_count: 1, created_at: '2026-01-01' }],
  tokens: [{ id: 11, connection_id: 5, name: 'Main', key_last4: 'a1b2', created_at: '2026-01-01' }],
}
const CATALOG = [{ engine_type: 'libretranslate', name: 'LibreTranslate', needs_token: false, params: [] }]

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MemoryRouter><QueryClientProvider client={qc}><AIVendorsSection defaultOpen canWrite /></QueryClientProvider></MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  listAIVendors.mockResolvedValue([OPENAI])
  listMTEngineCatalog.mockResolvedValue(CATALOG)
})

// A failed load must say so. Without the provider list every tile renders as
// unconfigured, which reads as "your keys are gone"; without the catalog the
// built-in providers vanish from the grid.
describe('AIVendorsSection when loading fails', () => {
  it.each([
    ['providers', listAIVendors],
    ['catalog', listMTEngineCatalog],
  ])('a failed %s load shows the error with a Retry, not an unconfigured grid', async (_what, failing) => {
    failing.mockRejectedValueOnce(new Error('server unavailable'))
    renderSection()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the AI vendors: server unavailable")
    expect(screen.queryByTestId('ai-connections')).not.toBeInTheDocument()
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('ai-connection-tile-5')).toHaveTextContent('1 key')
    expect(screen.getByTestId('ai-connection-tile-new-libretranslate')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('AIVendorsSection while loading', () => {
  it('shows no grid until the providers have loaded', async () => {
    listAIVendors.mockReturnValue(new Promise(() => {}))
    renderSection()

    // The catalog has arrived; only the providers are still pending.
    await waitFor(() => expect(listMTEngineCatalog).toHaveBeenCalled())
    await new Promise(r => setTimeout(r, 0))
    expect(screen.queryByTestId('ai-connections')).not.toBeInTheDocument()
  })
})
