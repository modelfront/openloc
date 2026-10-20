import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return {
    ...actual,
    getMe: vi.fn().mockResolvedValue({ id: 1, role: 'owner' }),
    listMembers: vi.fn(),
    listInvites: vi.fn(),
    resendInvite: vi.fn().mockResolvedValue({}),
    deleteInvite: vi.fn().mockResolvedValue({}),
  }
})

import { listMembers, listInvites, resendInvite, deleteInvite, type Invite, type Member } from '../../api'
import VendorsPage from './VendorsPage'

const membersMock = vi.mocked(listMembers)
const invitesMock = vi.mocked(listInvites)

const member = (id: number, role: string, name: string): Member =>
  ({ id, role, name, email: `${name.toLowerCase()}@example.com`, created_at: '2026-09-01T10:00:00Z' })
const invite = (id: number, email: string, acceptedAt: string | null = null): Invite =>
  ({ id, email, role: 'linguist', tenant_id: 1, invited_by: 1, accepted_at: acceptedAt, expires_at: '2026-12-01T00:00:00Z', created_at: '2026-10-01T10:00:00Z' })

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/org/vendors']}>
        <VendorsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  membersMock.mockReset()
  invitesMock.mockReset()
})

describe('VendorsPage list', () => {
  it('lists pending invites and vendor accounts, and nobody else', async () => {
    membersMock.mockResolvedValue([member(1, 'owner', 'Owner'), member(2, 'linguist', 'Lena')])
    invitesMock.mockResolvedValue([invite(7, 'new@example.com'), invite(8, 'done@example.com', '2026-10-02T00:00:00Z')])

    renderPage()

    const pending = await screen.findByTestId('vendor-invite-row-7')
    expect(pending).toHaveTextContent('new@example.com')
    expect(within(pending).getByTestId('vendor-status-pending-7')).toHaveTextContent('Not joined yet')
    expect(pending).toHaveTextContent(/Invited/)

    const vendor = screen.getByTestId('vendor-row-2')
    expect(vendor).toHaveTextContent('Lena')
    expect(vendor).toHaveTextContent('lena@example.com')
    expect(vendor).toHaveTextContent('Active')
    expect(vendor).toHaveTextContent(/Joined/)

    // The owner is a member but not a vendor; an accepted invite is already a member row.
    expect(screen.queryByTestId('vendor-row-1')).not.toBeInTheDocument()
    expect(screen.queryByTestId('vendor-invite-row-8')).not.toBeInTheDocument()
  })

  it('Resend and Revoke are icon buttons that act on that invite', async () => {
    membersMock.mockResolvedValue([])
    invitesMock.mockResolvedValue([invite(7, 'new@example.com')])

    renderPage()

    const row = await screen.findByTestId('vendor-invite-row-7')
    const resend = within(row).getByRole('button', { name: 'Resend invite' })
    const revoke = within(row).getByRole('button', { name: 'Revoke invite' })
    // Icons, like the Members tab's invite actions — no text label.
    expect(resend).toHaveTextContent('')
    expect(revoke).toHaveTextContent('')

    fireEvent.click(resend)
    await waitFor(() => expect(vi.mocked(resendInvite)).toHaveBeenCalledWith(7))
    fireEvent.click(revoke)
    await waitFor(() => expect(vi.mocked(deleteInvite)).toHaveBeenCalledWith(7))
  })

  it('does not focus the invite field on load', async () => {
    membersMock.mockResolvedValue([])
    invitesMock.mockResolvedValue([])

    renderPage()

    expect(await screen.findByTestId('vendor-invite-email')).not.toHaveFocus()
  })

  it('says there are no vendors yet when both lists are empty', async () => {
    membersMock.mockResolvedValue([member(1, 'owner', 'Owner')])
    invitesMock.mockResolvedValue([])

    renderPage()

    expect(await screen.findByText(/No vendors yet/)).toBeInTheDocument()
  })
})

// A failed load must say so: "No vendors yet" for a server error reads as
// "your vendors are gone".
describe('VendorsPage when the list fails to load', () => {
  it('shows the error with a Retry, not "No vendors yet"', async () => {
    membersMock
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValueOnce([member(2, 'linguist', 'Lena')])
    invitesMock.mockResolvedValue([])

    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the vendors: server unavailable")
    expect(screen.queryByText(/No vendors yet/)).not.toBeInTheDocument()

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('vendor-row-2')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('an invites failure is reported too, and Retry loads the invites', async () => {
    membersMock.mockResolvedValue([member(2, 'linguist', 'Lena')])
    invitesMock
      .mockRejectedValueOnce(new Error('invites unavailable'))
      .mockResolvedValueOnce([invite(7, 'new@example.com')])

    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the vendors: invites unavailable")
    expect(screen.queryByText(/No vendors yet/)).not.toBeInTheDocument()

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('vendor-invite-row-7')).toBeInTheDocument()
  })

  // A failed refetch keeps what is already on screen: after a revoke the invites
  // are reloaded, and a hiccup there must not hide a list that is still valid.
  it('a failed background refetch keeps the loaded rows', async () => {
    membersMock.mockResolvedValue([member(2, 'linguist', 'Lena')])
    invitesMock
      .mockResolvedValueOnce([invite(7, 'new@example.com')])
      .mockRejectedValue(new Error('refetch failed'))

    renderPage()

    fireEvent.click(await screen.findByTestId('vendor-invite-revoke-7'))
    await waitFor(() => expect(invitesMock).toHaveBeenCalledTimes(2))
    expect(screen.getByTestId('vendor-row-2')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
