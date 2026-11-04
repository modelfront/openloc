import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { UserProfile } from '@/api'
import { queryKeys } from '@/lib/query-keys'

const getMe = vi.fn()
vi.mock('@/api', () => ({ getMe: () => getMe() }))

import { OrgPageFrame } from './OrgPageFrame'

function renderFrame() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/org/settings']}>
        <OrgPageFrame><p>tab body</p></OrgPageFrame>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return qc
}

beforeEach(() => getMe.mockReset())

describe('OrgPageFrame', () => {
  it('shows the Members and Settings tabs, the current one marked', async () => {
    getMe.mockResolvedValue({ tenant_kind: 'customer' } satisfies Partial<UserProfile>)
    const qc = renderFrame()
    // The frame decides on the tabs from the profile: assert once it loaded.
    await waitFor(() => expect(qc.getQueryState(queryKeys.auth.me)?.status).toBe('success'))
    expect(screen.getByRole('heading', { name: 'Organization' })).toBeInTheDocument()
    expect(screen.getByText('tab body')).toBeInTheDocument()
    const tabs = screen.getByRole('navigation', { name: 'Section' })
    expect([...tabs.querySelectorAll('a')].map(a => [a.textContent, a.getAttribute('href')])).toEqual([
      ['Members', '/org'],
      ['Settings', '/org/settings'],
    ])
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByText(/Vendor for/)).toBeNull()
  })

  it('in a vendor org, names the client and shows no tab row', async () => {
    getMe.mockResolvedValue({ tenant_kind: 'vendor', client_org_name: 'Example Org' } satisfies Partial<UserProfile>)
    renderFrame()
    expect(await screen.findByText('Vendor for Example Org')).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Section' })).toBeNull()
    expect(screen.getByText('tab body')).toBeInTheDocument()
  })
})
