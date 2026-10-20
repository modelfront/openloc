import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import OrgMembersPage from './OrgMembersPage'
import type { Invite } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listInvites = vi.fn()
const resendInvite = vi.fn()
vi.mock('../../api', () => ({
  getMe: () => Promise.resolve({ id: 1, role: 'owner' }),
  listMembers: () => Promise.resolve([]),
  listInvites: () => listInvites(),
  createInvite: vi.fn(),
  deleteInvite: vi.fn(),
  resendInvite: (id: number) => resendInvite(id),
  updateMemberRole: vi.fn(),
  listUserLanguagePairs: () => Promise.resolve([]),
}))

function pendingInvite(id: number, email: string): Invite {
  return {
    id,
    tenant_id: 1,
    email,
    role: 'project_manager',
    invited_by: 1,
    accepted_at: null,
    expires_at: '2026-12-31T00:00:00Z',
    created_at: '2026-10-01T00:00:00Z',
  }
}

const first = pendingInvite(11, 'first@example.com')
const second = pendingInvite(12, 'second@example.com')

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={['/org']}><OrgMembersPage /></MemoryRouter></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  listInvites.mockResolvedValue([first, second])
})

describe('OrgMembersPage pending invites', () => {
  it('while one Resend is sending, every Resend button waits', async () => {
    let finishResend!: (inv: Invite) => void
    resendInvite.mockReturnValue(new Promise<Invite>(resolve => { finishResend = resolve }))
    const user = userEvent.setup()
    renderPage()

    const firstResend = await screen.findByTestId(`org-invite-resend-${first.id}`)
    const secondResend = screen.getByTestId(`org-invite-resend-${second.id}`)
    expect(firstResend).toBeEnabled()
    expect(secondResend).toBeEnabled()

    await user.click(firstResend)
    // The clicked row shows it is sending...
    await waitFor(() => expect(firstResend).toHaveAttribute('aria-busy', 'true'))
    expect(firstResend).toBeDisabled()
    // ...and the other row waits for it.
    expect(secondResend).toBeDisabled()
    expect(secondResend).not.toHaveAttribute('aria-busy')
    await user.click(secondResend)
    expect(resendInvite).toHaveBeenCalledTimes(1)
    expect(resendInvite).toHaveBeenCalledWith(first.id)

    finishResend(first)
    await waitFor(() => expect(screen.getByTestId(`org-invite-resend-${second.id}`)).toBeEnabled())
    expect(screen.getByTestId(`org-invite-resend-${first.id}`)).toBeEnabled()
  })
})
