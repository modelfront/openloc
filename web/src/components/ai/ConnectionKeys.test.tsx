import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectionKeys } from './ConnectionKeys'
import type { AIVendorToken } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const updateVendorToken = vi.fn()
vi.mock('../../api', () => ({
  createVendorToken: vi.fn(),
  updateVendorToken: (...args: unknown[]) => updateVendorToken(...args),
  deleteVendorToken: vi.fn(),
  listEnginesUsingKey: () => Promise.resolve([]),
}))

const token: AIVendorToken = {
  id: 7, name: 'Prod', key_last4: 'abcd', connection_id: 1, use_for_chat: false,
  created_at: '2026-08-01T00:00:00Z',
}

function renderKeys() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ConnectionKeys provider="openai" entries={[{ provider: 'openai', token }]} ensureConnectionId={() => Promise.resolve(1)} />
    </QueryClientProvider>,
  )
}

// Re-query each time: the button is remounted when its disabled reason comes or goes.
const update = () => screen.getByRole('button', { name: 'Update' })

describe('ConnectionKeys key editor', () => {
  it('Update is disabled until a field changes', async () => {
    updateVendorToken.mockResolvedValue(token)
    const user = userEvent.setup()
    renderKeys()
    await user.click(screen.getByTestId('ai-token-row-7'))

    expect(update()).toHaveAttribute('aria-disabled', 'true')
    await user.click(update())
    expect(updateVendorToken).not.toHaveBeenCalled()

    await user.type(screen.getByTestId('ai-token-api-key'), 'sk-new')
    expect(update()).not.toHaveAttribute('aria-disabled')
    await user.click(update())
    expect(updateVendorToken).toHaveBeenCalledWith('openai', 7, { name: 'Prod', api_key: 'sk-new' })
  })
})
