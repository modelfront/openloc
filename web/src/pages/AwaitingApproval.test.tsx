import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const getMe = vi.fn()
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return { ...actual, getMe: () => getMe() }
})

import AwaitingApproval from './AwaitingApproval'

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><AwaitingApproval /></MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('AwaitingApproval', () => {
  it('tells a disabled vendor member why, and offers their other orgs', async () => {
    getMe.mockResolvedValue({
      id: 1, email: 'lina@acme.test', name: 'Lina', needs_org_selection: false, tenant_id: 3,
      tenant_name: 'Acme Vendors', tenant_kind: 'vendor', org_pending: false, tenant_status: 'approved',
      membership_disabled: true,
    })
    renderPage()
    expect(await screen.findByRole('heading', { name: 'Your access to Acme Vendors is disabled' })).toBeInTheDocument()
    expect(screen.getByText(/Ask an admin of Acme Vendors to\s+enable it/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open another organization' })).toHaveAttribute('href', '/select-org')
    expect(screen.queryByText(/under review/)).toBeNull()
  })

  it('tells a vendor member that the client org is not active, and offers their other orgs', async () => {
    getMe.mockResolvedValue({
      id: 1, email: 'lina@acme.test', name: 'Lina', needs_org_selection: false, tenant_id: 3,
      tenant_name: 'Acme Vendors', tenant_kind: 'vendor', org_pending: true, tenant_status: 'client_inactive',
    })
    renderPage()
    expect(await screen.findByRole('heading', { name: 'Acme Vendors is not available' })).toBeInTheDocument()
    expect(screen.getByText(/The organization this vendor works for is not active right now/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open another organization' })).toHaveAttribute('href', '/select-org')
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeInTheDocument()
    expect(screen.queryByText(/under review/)).toBeNull()
  })
})
