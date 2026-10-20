import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { APITokensSection } from './APITokensSection'
import type { APIToken, CreatedAPIToken } from '../../api/tokens'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAPITokens = vi.fn<() => Promise<APIToken[]>>()
const createAPIToken = vi.fn<() => Promise<CreatedAPIToken>>()
const revokeAPIToken = vi.fn<(id: number) => Promise<void>>()

vi.mock('../../api', () => ({
  listAPITokens: (...args: unknown[]) => listAPITokens(...(args as [])),
  createAPIToken: (...args: unknown[]) => createAPIToken(...(args as [])),
  revokeAPIToken: (id: number) => revokeAPIToken(id),
}))

function token(overrides: Partial<APIToken> = {}): APIToken {
  return {
    id: 1,
    name: 'Claude Desktop',
    prefix: 'eltm_pat_abc123',
    capability: 'read',
    last_used_at: null,
    expires_at: '2027-01-01T00:00:00Z',
    revoked_at: null,
    created_at: '2026-08-01T00:00:00Z',
    ...overrides,
  }
}

// The section is collapsed by default, so every test opens it first — these
// cover the body, not the collapsed state (AISettingsPage.test.tsx asserts that
// it starts closed). Clicking the real header rather than forcing a prop keeps
// the tests honest about how the body is reached.
async function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const result = render(
    <QueryClientProvider client={client}>
      <APITokensSection />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: /^Access keys About access/ }))
  return result
}

beforeEach(() => {
  vi.clearAllMocks()
  listAPITokens.mockResolvedValue([])
  revokeAPIToken.mockResolvedValue(undefined)
})

describe('APITokensSection', () => {
  it('shows the empty state when there are no tokens', async () => {
    await renderSection()
    expect(await screen.findByText('No MCP/API access keys created yet.')).toBeInTheDocument()
  })

  it('lists existing tokens with their prefix, never a secret', async () => {
    listAPITokens.mockResolvedValue([token({ last_used_at: '2026-08-02T00:00:00Z' })])
    await renderSection()

    expect(await screen.findByText('Claude Desktop')).toBeInTheDocument()
    // Scoped to the row: "Read only" is also an <option> in the create form, and
    // a global getByText would match both.
    const row = screen.getByTestId('token-row-1')
    expect(within(row).getByText('Active')).toBeInTheDocument()
    expect(within(row).getByText('Read only')).toBeInTheDocument()
    // Only the display fragment appears — the listing endpoint has no secret to
    // leak, and this pins that the UI never expects one.
    expect(within(row).getByText(/eltm_pat_abc123/)).toBeInTheDocument()
  })

  it('reveals the plaintext token exactly once after creation', async () => {
    const user = userEvent.setup()
    createAPIToken.mockResolvedValue({
      token: 'eltm_pat_supersecretvalue',
      api_token: token({ id: 2, name: 'My Agent' }),
    })
    await renderSection()

    await user.type(await screen.findByTestId('token-name-input'), 'My Agent')
    await user.click(screen.getByTestId('create-token-btn'))

    // The secret is shown with an explicit warning, because it is genuinely
    // unrecoverable after dismissal.
    const revealed = await screen.findByTestId('revealed-token')
    expect(revealed).toHaveTextContent('eltm_pat_supersecretvalue')
    expect(revealed).toHaveTextContent(/won't be shown again/i)

    // Dismissing removes it from the DOM — there is no path back to the secret.
    await user.click(screen.getByTestId('dismiss-token-btn'))
    await waitFor(() => {
      expect(screen.queryByTestId('revealed-token')).not.toBeInTheDocument()
    })
    expect(screen.queryByText('eltm_pat_supersecretvalue')).not.toBeInTheDocument()
  })

  it('sends the chosen expiry and capability', async () => {
    const user = userEvent.setup()
    createAPIToken.mockResolvedValue({ token: 'eltm_pat_x', api_token: token() })
    await renderSection()

    await user.type(await screen.findByTestId('token-name-input'), 'Write agent')
    // Comboboxes, not native selects: open the popover
    // and pick the option, the same way every other picker in the app is driven.
    await user.click(screen.getByTestId('token-expiry-select'))
    await user.click(await screen.findByRole('option', { name: '30 days' }))
    await user.click(screen.getByTestId('token-capability-select'))
    await user.click(await screen.findByRole('option', { name: 'Read and write' }))
    await user.click(screen.getByTestId('create-token-btn'))

    await waitFor(() => expect(createAPIToken).toHaveBeenCalled())
    expect(createAPIToken).toHaveBeenCalledWith({
      name: 'Write agent',
      expires_in_days: 30,
      capability: 'write',
    })
  })

  it('will not submit a blank name', async () => {
    await renderSection()
    // Guarded in the UI as well as the backend — the button stays disabled, so a
    // pointless 400 is never issued.
    expect(await screen.findByTestId('create-token-btn')).toBeDisabled()
    expect(createAPIToken).not.toHaveBeenCalled()
  })

  it('revokes a token', async () => {
    const user = userEvent.setup()
    listAPITokens.mockResolvedValue([token({ id: 7 })])
    await renderSection()

    await user.click(await screen.findByTestId('revoke-token-7'))
    await waitFor(() => expect(revokeAPIToken).toHaveBeenCalledWith(7))
  })

  it('marks revoked and expired tokens distinctly and hides their revoke action', async () => {
    listAPITokens.mockResolvedValue([
      token({ id: 1, name: 'Revoked one', revoked_at: '2026-08-03T00:00:00Z' }),
      token({ id: 2, name: 'Expired one', expires_at: '2026-01-01T00:00:00Z' }),
    ])
    await renderSection()

    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    // An expired token is materially different from a revoked one — it can't be
    // used either, but nobody cut it off, so the distinction is worth showing.
    expect(screen.getByText('Expired')).toBeInTheDocument()
    // Revoking a revoked token is meaningless, so the action is absent.
    expect(screen.queryByTestId('revoke-token-1')).not.toBeInTheDocument()
    expect(screen.getByTestId('revoke-token-2')).toBeInTheDocument()
  })
})

// A failed load must say so: "No access keys created yet" for a server error
// reads as "your keys are gone".
describe('APITokensSection when loading fails', () => {
  it('shows the error with a Retry, not the empty state', async () => {
    listAPITokens.mockRejectedValueOnce(new Error('server unavailable'))
    listAPITokens.mockResolvedValue([token()])
    await renderSection()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the access keys: server unavailable")
    expect(screen.queryByText('No MCP/API access keys created yet.')).not.toBeInTheDocument()
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('token-row-1')).toBeInTheDocument()
  })
})
