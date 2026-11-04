import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { InviteLookup } from '../api'

const lookupInvite = vi.fn()
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getAuthConfig: () => Promise.resolve({ google_enabled: false, dev_login_enabled: false }),
    lookupInvite: (token: string) => lookupInvite(token),
  }
})

import AcceptInvite from './AcceptInvite'

const MEMBER_INVITE: InviteLookup = {
  email: 'new@example.com', org_name: 'Example Org', role: 'project_manager', kind: 'member',
  user_exists: false, has_password: false,
}

function renderPage() {
  render(
    <MemoryRouter initialEntries={['/invite/tok']}>
      <Routes><Route path="/invite/:token" element={<AcceptInvite />} /></Routes>
    </MemoryRouter>,
  )
}

beforeEach(() => lookupInvite.mockReset())

describe('AcceptInvite', () => {
  it('invites a member into the org', async () => {
    lookupInvite.mockResolvedValue(MEMBER_INVITE)
    renderPage()
    const intro = await screen.findByText(/You've been invited to/)
    expect(intro).toHaveTextContent("You've been invited to Example Org as new@example.com.")
    expect(screen.queryByText(/as a vendor for/)).toBeNull()
  })

  it('sets up a vendor org for its client, with the invitee as its admin', async () => {
    lookupInvite.mockResolvedValue({
      ...MEMBER_INVITE, role: 'admin', kind: 'vendor_org', vendor_org_name: 'Acme Translations',
    } satisfies InviteLookup)
    renderPage()
    const intro = await screen.findByTestId('invite-vendor-copy')
    expect(intro).toHaveTextContent("Set up Acme Translations as a vendor for Example Org. You'll be its admin.")
    expect(screen.queryByText(/You've been invited to/)).toBeNull()
  })
})
