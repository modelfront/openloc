import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Org } from '../api'

const ORGS: Org[] = [
  { tenant_id: 1, tenant_name: 'Example Org', tenant_slug: 'example-org', role: 'owner', tenant_kind: 'customer', client_org_name: null, disabled_at: null },
  { tenant_id: 2, tenant_name: 'Acme Vendors', tenant_slug: 'acme-vendors', role: 'admin', tenant_kind: 'vendor', client_org_name: 'Example Org', disabled_at: null },
  { tenant_id: 3, tenant_name: 'Quiet Vendors', tenant_slug: 'quiet-vendors', role: 'linguist', tenant_kind: 'vendor', client_org_name: 'Example Org', disabled_at: '2026-10-01T00:00:00Z' },
]

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    listOrgs: () => Promise.resolve(ORGS),
    getMe: () => Promise.resolve({ id: 1, email: 'me@example.com', name: 'Me', needs_org_selection: true }),
  }
})

import OrgPicker from './OrgPicker'

describe('OrgPicker', () => {
  it('says which client a vendor org works for, and nothing extra on other orgs', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><OrgPicker /></MemoryRouter>
      </QueryClientProvider>,
    )
    expect(await screen.findByTestId('org-vendor-for-2')).toHaveTextContent('Vendor for Example Org')
    expect(screen.getByTestId('org-vendor-for-2').closest('button')).toHaveTextContent('Acme Vendors')
    expect(screen.queryByTestId('org-vendor-for-1')).toBeNull()
    expect(screen.getAllByText(/^Vendor for/)).toHaveLength(2)
    // Each vendor org carries the Vendor chip; the customer org does not.
    expect(screen.getAllByTestId('org-kind-vendor')).toHaveLength(2)
    expect(screen.getByText('Example Org').closest('button')).not.toContainElement(screen.getAllByTestId('org-kind-vendor')[0])
    expect(screen.getByTestId('org-vendor-for-2').closest('button')).toContainElement(screen.getAllByTestId('org-kind-vendor')[0])
  })

  it('shows a disabled membership as disabled, and it cannot be entered', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><OrgPicker /></MemoryRouter>
      </QueryClientProvider>,
    )
    const note = await screen.findByTestId('org-disabled-3')
    expect(note).toHaveTextContent('Ask its admin to enable your access')
    const card = note.closest('button')!
    expect(card).toBeDisabled()
    expect(card).toHaveTextContent('Disabled')
    expect(screen.getByTestId('org-vendor-for-2').closest('button')).toBeEnabled()
    expect(screen.queryByTestId('org-disabled-2')).toBeNull()
  })
})
