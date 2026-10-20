import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import PlatformOrgs from './PlatformOrgs'
import { listPlatformOrgs, type PlatformOrg } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  listPlatformOrgs: vi.fn(),
}))

const acme = {
  tenant_id: 7,
  name: 'Acme Translations',
  domain: 'example.com',
  owner_name: 'Test Owner',
  owner_email: 'test@test.com',
  status: 'approved',
  is_self: false,
} as unknown as PlatformOrg

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <PlatformOrgs />
    </QueryClientProvider>,
  )
}

beforeEach(() => { vi.clearAllMocks() })

describe('PlatformOrgs list load', () => {
  it('a failed load shows the error with a Retry that reloads the list', async () => {
    vi.mocked(listPlatformOrgs)
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue([acme])
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the organizations: server unavailable")
    expect(screen.queryByText('No organizations yet.')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('Acme Translations')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(listPlatformOrgs).toHaveBeenCalledTimes(2)
  })

  it('an empty list shows the empty state and no error', async () => {
    vi.mocked(listPlatformOrgs).mockResolvedValue([])
    renderPage()

    expect(await screen.findByText('No organizations yet.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
