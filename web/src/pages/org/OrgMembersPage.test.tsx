import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import OrgMembersPage from './OrgMembersPage'
import type { Invite, MemberFilters, MemberPageItem, MembersPage, UserProfile } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

const getMe = vi.fn()
const listMembersPage = vi.fn()
const listInvites = vi.fn()
const resendInvite = vi.fn()
const createInvite = vi.fn()
const listVendorOrgs = vi.fn()
const getMemberFacets = vi.fn()
const listAcceptedInvites = vi.fn()
const enableMember = vi.fn()
const enableVendorAdmin = vi.fn()
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return {
    ...actual,
    getMe: () => getMe(),
    listMembersPage: (filters: MemberFilters, cursor: number, limit: number) => listMembersPage(filters, cursor, limit),
    listInvites: (opts: unknown) => listInvites(opts),
    createInvite: (...args: unknown[]) => createInvite(...args),
    deleteInvite: vi.fn(),
    resendInvite: (id: number) => resendInvite(id),
    updateMemberRole: vi.fn(),
    listUserLanguagePairs: () => Promise.resolve([]),
    replaceUserLanguagePairs: vi.fn(),
    listLanguages: () => Promise.resolve([]),
    listVendorOrgs: (filters: unknown, cursor: number, limit: number) => listVendorOrgs(filters, cursor, limit),
    getMemberFacets: (filters: unknown) => getMemberFacets(filters),
    listAcceptedInvites: (q: string, cursor: number, limit: number) => listAcceptedInvites(q, cursor, limit),
    getVendorOrgFacets: () => Promise.resolve({ sources: [], targets: [] }),
    enableMember: (id: number) => enableMember(id),
    enableVendorAdmin: (vendorId: number, userId: number) => enableVendorAdmin(vendorId, userId),
  }
})

const CUSTOMER_OWNER: Partial<UserProfile> = { id: 1, role: 'owner', tenant_kind: 'customer', tenant_name: 'Example Org' }
const VENDOR_ADMIN: Partial<UserProfile> = {
  id: 1, role: 'admin', tenant_kind: 'vendor', tenant_name: 'Acme Vendors', client_org_name: 'Example Org',
}

function pendingInvite(id: number, email: string): Invite {
  return {
    id,
    tenant_id: 1,
    email,
    role: 'project_manager',
    kind: 'member',
    invited_by: 1,
    accepted_at: null,
    expires_at: '2026-12-31T00:00:00Z',
    created_at: '2026-10-01T00:00:00Z',
  }
}

const first = pendingInvite(11, 'first@example.com')
const second = pendingInvite(12, 'second@example.com')

function renderPage(url = '/org') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[url]}><OrgMembersPage /></MemoryRouter></QueryClientProvider>)
}

function member(id: number, name: string, role: string, pairs: MemberPageItem['language_pairs'] = []): MemberPageItem {
  return { id, name, email: `${name.toLowerCase()}@example.com`, role, created_at: '2026-10-01T00:00:00Z', language_pairs: pairs, disabled_at: null }
}

function page(data: MemberPageItem[], more: Partial<MembersPage> = {}): MembersPage {
  return { data, cursor: data.length, has_more: false, total: data.length, ...more }
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  getMe.mockResolvedValue(CUSTOMER_OWNER)
  listMembersPage.mockResolvedValue(page([]))
  listInvites.mockResolvedValue([first, second])
  listVendorOrgs.mockResolvedValue({ data: [], cursor: 0, has_more: false, total: 0 })
  getMemberFacets.mockResolvedValue({ roles: ['owner', 'admin', 'project_manager', 'linguist'], sources: [], targets: [] })
  listAcceptedInvites.mockResolvedValue({ data: [], cursor: 0, has_more: false, total: 0 })
})

/** The values of a role picker (a native select under the combobox mock). */
const roleOptions = (testId: string) =>
  [...(screen.getByTestId(testId) as HTMLSelectElement).options].filter(o => o.value).map(o => o.textContent)

describe('OrgMembersPage pending invites', () => {
  it('asks the server for the pending invites only', async () => {
    renderPage()
    expect(await screen.findByTestId(`org-invite-row-${first.id}`)).toBeVisible()
    expect(listInvites).toHaveBeenCalledWith({ status: 'pending' })
  })

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

describe('OrgMembersPage invite form', () => {
  it('keeps Send invite disabled until the email is valid, and says why', async () => {
    const user = userEvent.setup()
    renderPage()
    // The invite sections start closed; the row opens one.
    await user.click(await screen.findByRole('button', { name: 'Invite member' }))
    const email = screen.getByPlaceholderText('colleague@example.com')
    const submit = () => screen.getByTestId('member-invite-submit')
    await waitFor(() => expect(submit()).toHaveAttribute('aria-disabled', 'true'))
    await user.type(email, 'not-an-email')
    await user.hover(submit())
    expect(await screen.findByText('Enter a valid email address first.')).toBeInTheDocument()
    await user.clear(email)
    await user.type(email, 'new@example.com')
    await waitFor(() => expect(submit()).not.toHaveAttribute('aria-disabled'))
    expect(submit()).toBeEnabled()
  })

  it('offers Admin, Project manager and Linguist to Invite member', async () => {
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: 'Invite member' }))
    expect(screen.getByTestId('member-invite-email')).toBeVisible()
    expect(roleOptions('member-invite-role')).toEqual(['Admin', 'Project manager', 'Linguist'])
  })

  it('invites a vendor org by name and admin email, with no role to pick', async () => {
    const user = userEvent.setup()
    createInvite.mockResolvedValue({})
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Invite vendor' }))
    const email = screen.getByTestId('vendor-invite-email')
    const card = email.closest('form')!
    expect(card.querySelector('[data-slot="combobox-trigger"], select')).toBeNull()
    await user.type(screen.getByTestId('vendor-invite-org-name'), 'Acme Translations')
    await user.type(email, ' boss@vendor.com ')
    await user.click(screen.getByTestId('vendor-invite-submit'))
    await waitFor(() => expect(createInvite).toHaveBeenCalledWith('boss@vendor.com', 'admin', 'Acme Translations'))
    await waitFor(() => expect(email).toHaveValue(''))
  })
})

const vendorInvite = (id: number, accepted: boolean): Invite => ({
  ...pendingInvite(id, `boss${id}@vendor.com`),
  role: 'admin',
  kind: 'vendor_org',
  vendor_org_name: `Vendor ${id}`,
  accepted_at: accepted ? '2026-10-20T00:00:00Z' : null,
})

describe('OrgMembersPage vendor-org invites', () => {
  it('shows the vendor name and Vendor org admin, not Admin, on pending and accepted rows', async () => {
    listInvites.mockResolvedValue([vendorInvite(21, false)])
    listAcceptedInvites.mockResolvedValue({ data: [vendorInvite(22, true)], cursor: 1, has_more: false, total: 1 })
    renderPage()
    expect(await screen.findByTestId('org-invite-accepted-22')).toBeVisible()
    for (const [id, row] of [[21, await screen.findByTestId('org-invite-row-21')], [22, screen.getByTestId('org-invite-accepted-22')]] as const) {
      expect(within(row).getByText(`Vendor ${id}`)).toBeInTheDocument()
      expect(within(row).getByText('Vendor org admin')).toBeInTheDocument()
      expect(within(row).queryByText('Admin')).toBeNull()
    }
  })
})

describe('OrgMembersPage in a vendor org', () => {
  const pm = member(7, 'Pablo', 'project_manager')

  beforeEach(() => {
    getMe.mockResolvedValue(VENDOR_ADMIN)
    listMembersPage.mockResolvedValue(page([pm]))
    listInvites.mockResolvedValue([])
  })

  it('marks a disabled member with a plain chip; their dialog enables them', async () => {
    const user = userEvent.setup()
    enableMember.mockResolvedValue({ status: 'ok' })
    listMembersPage.mockResolvedValue(page([pm, { ...member(8, 'Lina', 'linguist'), disabled_at: '2026-10-01T00:00:00Z' }]))
    renderPage()
    const row = await screen.findByTestId('member-row-8')
    expect(within(row).getByTestId('member-disabled')).toHaveTextContent('Disabled')
    // The chip is a label, not a button: the row is what opens.
    expect(within(row).queryByRole('button', { name: /Disabled/ })).toBeNull()
    expect(within(screen.getByTestId('member-row-7')).queryByTestId('member-disabled')).toBeNull()

    await user.click(row)
    const dialog = await screen.findByRole('dialog', { name: 'Lina' })
    expect(within(dialog).getByText('Access disabled')).toBeInTheDocument()
    expect(within(dialog).getByText('A member who does not sign in for 90 days is disabled.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Enable lina@example.com' }))
    await waitFor(() => expect(enableMember).toHaveBeenCalledWith(8))
    await waitFor(() => expect(within(dialog).queryByText('Access disabled')).toBeNull())
  })

  it('an enabled member\'s dialog has no access notice', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('member-row-7'))
    const dialog = await screen.findByRole('dialog', { name: 'Pablo' })
    expect(within(dialog).queryByText('Access disabled')).toBeNull()
  })

  it('names the client in the header and has no Settings tab', async () => {
    renderPage()
    await screen.findByTestId('member-invite-open')
    expect(screen.getByText('Vendor for Example Org')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Settings' })).toBeNull()
  })

  it('has no Invite vendor button and invites project managers and linguists only', async () => {
    renderPage()
    await userEvent.click(await screen.findByTestId('member-invite-open'))
    await screen.findByTestId('member-invite-email')
    expect(screen.queryByTestId('vendor-invite-open')).toBeNull()
    expect(screen.queryByText('Invite vendor')).toBeNull()
    expect(roleOptions('member-invite-role')).toEqual(['Project manager', 'Linguist'])
  })

  it('lets a member become Admin, Project manager or Linguist, never Owner', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId(`member-row-${pm.id}`))
    await screen.findByTestId('member-dialog')
    expect(roleOptions('member-role')).toEqual(['Admin', 'Project manager', 'Linguist'])
  })
})

const ACME = {
  id: 4, name: 'Acme Vendors', created_at: '2026-10-31T00:00:00Z', admins: [{ user_id: 41, email: 'vera@acme.test', disabled: false }, { user_id: 42, email: 'max@acme.test', disabled: false }],
  language_pairs: [{ source_lang: 'en', target_lang: 'de' }, { source_lang: 'en', target_lang: 'fr' }],
}
const vendorPage = (data: unknown[], total = data.length) => ({ data, cursor: data.length, has_more: false, total })

describe('OrgMembersPage Vendors list', () => {
  it('lists the vendor orgs under Members: admins and languages', async () => {
    listVendorOrgs.mockResolvedValue(vendorPage([ACME]))
    renderPage()
    const row = await screen.findByTestId('vendor-org-row-4')
    expect(row).toHaveTextContent('Acme Vendors')
    expect(within(row).getAllByTestId('vendor-admin-4').map(e => e.textContent)).toEqual(['vera@acme.test', 'max@acme.test'])
    expect(row).not.toHaveTextContent('Admin:')
    expect(within(row).getByTestId('vendor-pairs-4')).toHaveTextContent(/en.*de, fr/)
    // Directly below the Members card.
    const titles = screen.getAllByRole('heading').map(h => h.textContent)
    expect(titles.indexOf('Vendors')).toBe(titles.indexOf('Members') + 1)
  })

  it('marks a vendor with disabled admins; its row opens a window that enables the ones you pick', async () => {
    const user = userEvent.setup()
    enableVendorAdmin.mockResolvedValue({ status: 'ok' })
    listVendorOrgs.mockResolvedValue(vendorPage([{ ...ACME, admins: [
      { user_id: 41, email: 'vera@acme.test', disabled: false },
      { user_id: 42, email: 'max@acme.test', disabled: true },
      { user_id: 43, email: 'ida@acme.test', disabled: true },
    ] }]))
    renderPage()
    const row = await screen.findByTestId('vendor-org-row-4')
    // The admin lines are the enabled admins; one plain chip marks the rest.
    expect(within(row).getAllByTestId('vendor-admin-4').map(e => e.textContent)).toEqual(['vera@acme.test'])
    expect(within(row).getAllByTestId('member-disabled')).toHaveLength(1)
    expect(within(row).queryByRole('button', { name: /Disabled/ })).toBeNull()

    await user.click(row)
    const dialog = await screen.findByRole('dialog', { name: 'Acme Vendors' })
    for (const email of ['vera@acme.test', 'max@acme.test', 'ida@acme.test']) {
      expect(within(dialog).getByText(email)).toBeInTheDocument()
    }
    expect(within(dialog).queryByRole('button', { name: 'Enable vera@acme.test' })).toBeNull()
    // No chip inside: Enable marks the disabled admins.
    expect(within(dialog).queryByTestId('member-disabled')).toBeNull()
    await user.click(within(dialog).getByRole('button', { name: 'Enable ida@acme.test' }))
    await waitFor(() => expect(enableVendorAdmin).toHaveBeenCalledWith(4, 43))
    expect(enableVendorAdmin).toHaveBeenCalledTimes(1)
  })

  it('has no Disabled chip when every admin is enabled', async () => {
    listVendorOrgs.mockResolvedValue(vendorPage([ACME]))
    renderPage()
    const row = await screen.findByTestId('vendor-org-row-4')
    expect(within(row).queryByTestId('member-disabled')).toBeNull()
  })

  it('says so when there are no vendors', async () => {
    renderPage()
    expect(await screen.findByText(/No vendors yet/)).toBeInTheDocument()
  })

  it('searches vendors as you type, apart from the members search', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(await screen.findByTestId('vendor-search'), 'acme')
    await waitFor(() => expect(listVendorOrgs).toHaveBeenLastCalledWith({ q: 'acme' }, 0, 10))
    expect(listMembersPage).not.toHaveBeenCalledWith(expect.objectContaining({ q: 'acme' }), expect.anything(), expect.anything())
  })

  it('reads its own filters and page from the URL, and clears them', async () => {
    const user = userEvent.setup()
    listVendorOrgs.mockResolvedValue(vendorPage([ACME], 40))
    renderPage('/org?vsource=en&vtarget=de&vpage=2&source=fr')
    await waitFor(() => expect(listVendorOrgs).toHaveBeenCalledWith({ source: 'en', target: 'de' }, 10, 10))
    await user.click(await screen.findByTestId('vendor-filter-clear'))
    await waitFor(() => expect(listVendorOrgs).toHaveBeenLastCalledWith({}, 0, 10))
    // The members' own Source filter stays.
    expect(listMembersPage).toHaveBeenLastCalledWith({ source: 'fr' }, 0, 10)
  })

  it('has no Vendors list inside a vendor org', async () => {
    getMe.mockResolvedValue(VENDOR_ADMIN)
    renderPage()
    await screen.findByRole('heading', { name: 'Members' })
    expect(screen.queryByRole('heading', { name: 'Vendors' })).toBeNull()
    expect(listVendorOrgs).not.toHaveBeenCalled()
  })
})

describe('OrgMembersPage members list', () => {
  it('shows the total and each member with their pairs, and moves between numbered pages', async () => {
    const user = userEvent.setup()
    listMembersPage.mockImplementation((_f: MemberFilters, offset: number) => Promise.resolve(
      offset === 0
        ? page([member(1, 'Ada', 'owner', [{ source_lang: 'en', target_lang: 'de', row_group: 0 }])], { has_more: true, total: 30 })
        : page([member(offset + 1, 'Bo', 'linguist')], { total: 30 }),
    ))
    renderPage()
    expect(await screen.findByTestId('members-total')).toHaveTextContent('30 members')
    expect(screen.getByTestId('pairs-display-1')).toBeInTheDocument()
    expect(listMembersPage).toHaveBeenLastCalledWith({}, 0, 10)
    // 30 members, 10 a page: pages 1 to 3, the first one current.
    expect(screen.getByTestId('members-pages-page-1')).toHaveAttribute('aria-current', 'page')
    expect(screen.getByTestId('members-pages-page-3')).toBeInTheDocument()
    expect(screen.queryByTestId('members-pages-page-4')).toBeNull()

    await user.click(screen.getByTestId('members-pages-page-3'))
    expect(await screen.findByTestId('member-row-21')).toBeInTheDocument()
    expect(listMembersPage).toHaveBeenLastCalledWith({}, 20, 10)
    expect(screen.queryByTestId('member-row-1')).toBeNull()
    expect(screen.getByTestId('members-pages-page-3')).toHaveAttribute('aria-current', 'page')

    await user.click(screen.getByTestId('members-pages-prev'))
    expect(await screen.findByTestId('member-row-11')).toBeInTheDocument()
  })

  it('keeps the scroll where it is when the page changes', async () => {
    const user = userEvent.setup()
    listMembersPage.mockImplementation((_f: MemberFilters, offset: number) => Promise.resolve(
      page([member(offset + 1, 'Ada', 'linguist')], { has_more: offset === 0, total: 40 }),
    ))
    renderPage()
    await screen.findByTestId('member-row-1')
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    await user.click(screen.getByTestId('members-pages-page-2'))
    expect(await screen.findByTestId('member-row-11')).toBeInTheDocument()
    expect(scroll).not.toHaveBeenCalled()
    scroll.mockRestore()
  })

  it('opens the page in the URL, and a filter change goes back to page 1', async () => {
    const user = userEvent.setup()
    listMembersPage.mockResolvedValue(page([member(11, 'Cy', 'linguist')], { total: 40 }))
    renderPage('/org?page=2')
    await waitFor(() => expect(listMembersPage).toHaveBeenLastCalledWith({}, 10, 10))
    await user.type(screen.getByTestId('member-search'), 'cy')
    await waitFor(() => expect(listMembersPage).toHaveBeenLastCalledWith({ q: 'cy' }, 0, 10))
  })

  it('goes back to page 1 from a page past the end', async () => {
    listMembersPage.mockImplementation((_f: MemberFilters, offset: number) => Promise.resolve(
      offset === 0 ? page([member(1, 'Ada', 'owner')]) : { data: [], cursor: offset, has_more: false },
    ))
    renderPage('/org?page=9')
    expect(await screen.findByTestId('member-row-1')).toBeInTheDocument()
    expect(listMembersPage).toHaveBeenLastCalledWith({}, 0, 10)
  })

  it('has a Languages column: chips for a member with pairs, a dash for one without', async () => {
    listMembersPage.mockResolvedValue(page([
      member(1, 'Ada', 'linguist', [{ source_lang: 'en', target_lang: 'de', row_group: 0 }]),
      member(2, 'Bo', 'linguist'),
    ]))
    renderPage()
    expect(await screen.findByRole('columnheader', { name: 'Languages' })).toBeInTheDocument()
    expect(within(screen.getByTestId('member-row-1')).getByTestId('pairs-display-1')).toHaveTextContent(/en.*de/)
    expect(within(screen.getByTestId('member-row-2')).getByTestId('pairs-none-2')).toHaveTextContent('—')
  })

  it('offers only the roles with a hit, and asks with the current filters', async () => {
    const user = userEvent.setup()
    getMemberFacets.mockResolvedValue({ roles: ['linguist'], sources: ['en'], targets: ['de'] })
    renderPage('/org?target=de')
    await waitFor(() => expect(getMemberFacets).toHaveBeenCalledWith({ target: 'de' }))
    await user.click(await screen.findByTestId('filter-role'))
    const options = (await screen.findAllByRole('button', { name: /^(All|Owner|Admin|Project manager|Linguist)$/ })).map(b => b.textContent)
    expect(options).toEqual(['All', 'Linguist'])
  })

  it('sums up the section with the member count only', async () => {
    listMembersPage.mockResolvedValue(page([member(1, 'Ada', 'admin'), member(2, 'Bo', 'admin'), member(3, 'Cy', 'linguist')]))
    renderPage()
    const members = (await screen.findByRole('heading', { name: 'Members' })).closest('section')!
    await waitFor(() => expect(within(members).getByTestId('members-total').parentElement).toHaveTextContent(/^3 members$/))
  })

  it('searches name or email as you type', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(await screen.findByTestId('member-search'), 'ada')
    await waitFor(() => expect(listMembersPage).toHaveBeenLastCalledWith({ q: 'ada' }, 0, 10))
  })

  it('reads the filters from the URL, ignores an unknown role, and clears them', async () => {
    const user = userEvent.setup()
    renderPage('/org?role=linguist&source=en&target=de&page=2')
    await waitFor(() => expect(listMembersPage).toHaveBeenLastCalledWith({ role: 'linguist', source: 'en', target: 'de' }, 10, 10))
    expect(await screen.findByText('No members match these filters.')).toBeInTheDocument()
    await user.click(screen.getByTestId('member-filter-clear'))
    await waitFor(() => expect(listMembersPage).toHaveBeenLastCalledWith({}, 0, 10))
  })

  it('drops a role that does not exist', async () => {
    renderPage('/org?role=boss')
    await waitFor(() => expect(listMembersPage).toHaveBeenCalledWith({}, 0, 10))
    expect(screen.queryByTestId('member-filter-clear')).toBeNull()
  })
})

describe('OrgMembersPage Accepted invites', () => {
  const accepted = (id: number, email: string) => ({ ...pendingInvite(id, email), accepted_at: '2026-10-20T00:00:00Z' })

  it('is hidden while no invite was accepted', async () => {
    renderPage()
    await screen.findByRole('heading', { name: 'Members' })
    expect(screen.queryByRole('heading', { name: 'Accepted invites' })).toBeNull()
  })

  it('searches its own list and moves by pages', async () => {
    const user = userEvent.setup()
    listAcceptedInvites.mockImplementation((_q: string, cursor: number) => Promise.resolve(
      { data: [accepted(100 + cursor, `p${cursor}@example.com`)], cursor: cursor + 1, has_more: cursor === 0, total: 25 },
    ))
    renderPage()
    expect(await screen.findByTestId('org-invite-accepted-100')).toBeInTheDocument()
    await user.click(screen.getByTestId('accepted-pages-page-3'))
    expect(await screen.findByTestId('org-invite-accepted-120')).toBeInTheDocument()
    expect(listAcceptedInvites).toHaveBeenLastCalledWith('', 20, 10)
    await user.type(screen.getByTestId('accepted-search'), 'p0@')
    await waitFor(() => expect(listAcceptedInvites).toHaveBeenLastCalledWith('p0@', 0, 10))
    // The members list is not searched.
    expect(listMembersPage).not.toHaveBeenCalledWith(expect.objectContaining({ q: 'p0@' }), expect.anything(), expect.anything())
  })
})

describe('OrgMembersPage layout', () => {
  it('puts Invite member in the Members header and Invite vendor in the Vendors header, and no section collapses', async () => {
    listInvites.mockResolvedValue([vendorInvite(21, false)])
    renderPage()
    const members = (await screen.findByRole('heading', { name: 'Members' })).closest('section')!
    const vendors = (await screen.findByRole('heading', { name: 'Vendors' })).closest('section')!
    expect(within(members).getByTestId('member-invite-open')).toBeInTheDocument()
    expect(within(members).queryByTestId('vendor-invite-open')).toBeNull()
    expect(within(vendors).getByTestId('vendor-invite-open')).toBeInTheDocument()
    await screen.findByTestId('org-invite-row-21')
    expect(document.querySelectorAll('section h2 button[aria-expanded]')).toHaveLength(0)
  })
})

