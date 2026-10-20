import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectedAppsSection } from './ConnectedAppsSection'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listOAuthConnections = vi.fn()
vi.mock('../../api', () => ({
  listOAuthConnections: (...a: unknown[]) => listOAuthConnections(...a),
  revokeOAuthConnection: vi.fn(),
}))

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ConnectedAppsSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ConnectedAppsSection', () => {
  it('a failed load shows the error with a Retry, not "No apps connected"', async () => {
    const user = userEvent.setup()
    listOAuthConnections.mockRejectedValueOnce(new Error('server unavailable'))
    renderSection()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the connected apps: server unavailable")
    expect(screen.queryByTestId('connected-apps-empty')).not.toBeInTheDocument()

    listOAuthConnections.mockResolvedValueOnce([
      { family_id: 'fam-1', client_name: 'Claude', scope: 'read', created_at: '2026-10-01T10:00:00Z', last_used_at: null },
    ])
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('connected-app-row')).toHaveTextContent('Claude')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
