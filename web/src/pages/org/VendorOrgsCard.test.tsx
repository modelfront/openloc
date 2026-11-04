import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AttentionProvider } from '../../hooks/useAttention'
import { queryKeys } from '../../lib/query-keys'
import type { VendorOrg } from '../../api'
import { VendorOrgsCard } from './VendorOrgsCard'
import { ORG_VENDORS_PATH } from './org-paths'

const listVendorOrgs = vi.fn()
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return {
    ...actual,
    listVendorOrgs: (filters: unknown, cursor: number, limit: number) => listVendorOrgs(filters, cursor, limit),
    getVendorOrgFacets: () => Promise.resolve({ sources: [], targets: [] }),
    listLanguages: () => Promise.resolve([]),
  }
})

const ACME: VendorOrg = {
  id: 4, name: 'Acme Vendors', created_at: '2026-10-31T00:00:00Z',
  admins: [{ user_id: 41, email: 'vera@acme.test', disabled: false }], language_pairs: [],
}

// The Members list above the Vendors section, its rows held back until the test releases them.
let releaseMembers: () => void = () => {}
function MembersAbove() {
  const { data } = useQuery({
    queryKey: queryKeys.members.page({}, 1),
    queryFn: () => new Promise<string[]>(resolve => { releaseMembers = () => resolve(['Pablo']) }),
  })
  return <ul>{data?.map(name => <li key={name}>{name}</li>)}</ul>
}

beforeEach(() => {
  vi.clearAllMocks()
  listVendorOrgs.mockResolvedValue({ data: [ACME], cursor: 1, has_more: false, total: 1 })
})

describe('VendorOrgsCard', () => {
  it('takes the link from a vendor notice once its rows and the Members list above it are in', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[ORG_VENDORS_PATH]}>
          <AttentionProvider>
            <MembersAbove />
            <VendorOrgsCard />
          </AttentionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const section = (await screen.findByTestId('vendor-org-row-4')).closest('section')!
    // The members still load: the section would move after a scroll now.
    expect(section.dataset.attention).toBeUndefined()

    releaseMembers()
    expect(await screen.findByText('Pablo')).toBeInTheDocument()
    await waitFor(() => expect(section.dataset.attention).toBe(''))
  })
})
