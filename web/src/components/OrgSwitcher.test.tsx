import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
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
})
