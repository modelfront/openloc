import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { OrgSwitcher } from './OrgSwitcher'

const api = vi.hoisted(() => ({ getAppConfig: vi.fn(), getMe: vi.fn(), listOrgs: vi.fn(), leaveDemo: vi.fn() }))

vi.mock('../api', () => ({
  getMe: api.getMe,
  listOrgs: api.listOrgs,
  leaveDemo: api.leaveDemo,
  selectOrg: vi.fn(),
  getAppConfig: api.getAppConfig,
}))

const config = (playground: boolean) =>
  ({ app_url: '', docs_url: '', docs_host: false, developer_url: '', developer_host: false, playground })

beforeEach(() => {
  api.getAppConfig.mockResolvedValue(config(true))
  api.getMe.mockResolvedValue({ tenant_id: 1, tenant_name: 'Acme' })
  api.listOrgs.mockClear()
  api.listOrgs.mockResolvedValue([
    { tenant_id: 1, tenant_name: 'Acme', role: 'admin' },
    { tenant_id: 2, tenant_name: 'Example', role: 'owner' },
  ])
  api.leaveDemo.mockReset()
})

async function renderSwitcher() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <OrgSwitcher />
    </QueryClientProvider>,
  )
  return screen.findByRole('button', { name: /switch organization/i })
}

describe('OrgSwitcher', () => {
  it('opens when the mouse moves over it', async () => {
    const trigger = await renderSwitcher()
    fireEvent.mouseMove(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(await screen.findByRole('menu')).toBeInTheDocument()
  })

  it('stays open when the click follows the hover that opened it', async () => {
    const trigger = await renderSwitcher()
    // A real click moves the pointer onto the trigger first: the hover opens
    // the menu, then the press reaches a trigger that is already open.
    fireEvent.mouseMove(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(await screen.findByRole('menu')).toBeInTheDocument()
  })

  it('closes on a press when the menu was not opened by the hover', async () => {
    const trigger = await renderSwitcher()
    // A keyboard press opens and closes it without any mouse movement.
    trigger.focus()
    await userEvent.keyboard('{Enter}')
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
  })

  it('stays closed when it slides under a still cursor', async () => {
    // A layout shift (the nav rail collapsing) puts the trigger under the
    // cursor: the browser fires `mouseenter` with no `mousemove`.
    const trigger = await renderSwitcher()
    fireEvent.mouseEnter(trigger)
    fireEvent.mouseOver(trigger)
    // `aria-expanded` follows the open state in the same render, so it needs no
    // wait, unlike the portalled menu content.
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('menu')).toBeNull()
  })
})

describe('OrgSwitcher Playground entry', () => {
  it('is offered when the server has the Playground on', async () => {
    fireEvent.mouseMove(await renderSwitcher())
    expect(await screen.findByTestId('org-switcher-demo')).toBeInTheDocument()
  })

  it('is hidden when the server has the Playground off', async () => {
    api.getAppConfig.mockResolvedValue(config(false))
    fireEvent.mouseMove(await renderSwitcher())
    expect(await screen.findByRole('menuitem', { name: 'Example' })).toBeInTheDocument()
    // Negative check: let the config arrive before asserting.
    await vi.waitFor(() => expect(api.getAppConfig).toHaveBeenCalled())
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByTestId('org-switcher-demo')).toBeNull()
  })
})

describe('OrgSwitcher inside the Playground', () => {
  it('offers only the way back', async () => {
    api.getMe.mockResolvedValue({ tenant_id: 9, tenant_name: 'Playground', demo: true, demo_return_org_name: 'Acme' })
    api.leaveDemo.mockReturnValue(new Promise(() => {}))
    fireEvent.mouseMove(await renderSwitcher())
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Back to Acme' }))
    expect(api.leaveDemo).toHaveBeenCalled()
    expect(screen.queryByTestId('org-switcher-demo')).toBeNull()
    expect(api.listOrgs).not.toHaveBeenCalled()
  })

  it('marks vendor orgs with a Vendor chip, in the menu and on the current org', async () => {
    api.getMe.mockResolvedValue({ tenant_id: 3, tenant_name: 'Acme Vendors', tenant_kind: 'vendor' })
    api.listOrgs.mockResolvedValue([
      { tenant_id: 2, tenant_name: 'Example', role: 'owner', tenant_kind: 'customer' },
      { tenant_id: 3, tenant_name: 'Acme Vendors', role: 'admin', tenant_kind: 'vendor' },
    ])
    const trigger = await renderSwitcher()
    expect(within(trigger).getByTestId('org-kind-vendor')).toHaveTextContent('Vendor')
    fireEvent.mouseMove(trigger)
    const items = await screen.findAllByRole('menuitem')
    const vendorRow = items.find(i => i.textContent?.includes('Acme Vendors'))!
    const customerRow = items.find(i => i.textContent?.includes('Example'))!
    expect(within(vendorRow).getByTestId('org-kind-vendor')).toBeInTheDocument()
    expect(within(customerRow).queryByTestId('org-kind-vendor')).toBeNull()
  })

  it('has no Vendor chip on a customer org', async () => {
    const trigger = await renderSwitcher()
    expect(within(trigger).queryByTestId('org-kind-vendor')).toBeNull()
  })
})
