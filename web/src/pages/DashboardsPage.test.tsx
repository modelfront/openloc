// Dashboards list: Organization dashboards and My dashboards are two tabs over
// ONE fetch — the endpoint already returns only what this user may see, so the
// tabs are a filter, not a second request. Pinned has its own list.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getMe: vi.fn(),
    listDashboards: vi.fn(),
    deleteDashboard: vi.fn(),
    setDashboardVisibility: vi.fn(),
    renameDashboard: vi.fn(),
    reorderDashboards: vi.fn(),
    duplicateDashboard: vi.fn(),
    getSharedDashboardCount: vi.fn(),
    resetSharedDashboards: vi.fn(),
    createDashboard: vi.fn(),
    listPinnedDashboards: vi.fn(),
    getPinState: vi.fn(),
    pinDashboard: vi.fn(),
    unpinDashboard: vi.fn(),
    deletePinnedDashboard: vi.fn(),
    renamePinnedDashboard: vi.fn(),
    reorderPinnedDashboards: vi.fn(),
  }
})

import {
  getMe, listDashboards, deleteDashboard, setDashboardVisibility, renameDashboard, reorderDashboards, duplicateDashboard, createDashboard,
  listPinnedDashboards, getPinState, pinDashboard, unpinDashboard, deletePinnedDashboard, renamePinnedDashboard,
} from '../api'
import { DashboardsPage, DashboardsLanding, resolveDrop } from './DashboardsPage'

const getMeMock = vi.mocked(getMe)
const listMock = vi.mocked(listDashboards)
const deleteDashboardMock = vi.mocked(deleteDashboard)
const visibilityMock = vi.mocked(setDashboardVisibility)
const createDashboardMock = vi.mocked(createDashboard)
const renameDashboardMock = vi.mocked(renameDashboard)
const reorderDashboardsMock = vi.mocked(reorderDashboards)
const duplicateMock = vi.mocked(duplicateDashboard)
const pinnedListMock = vi.mocked(listPinnedDashboards)
const pinStateMock = vi.mocked(getPinState)
const pinDashboardMock = vi.mocked(pinDashboard)
const unpinDashboardMock = vi.mocked(unpinDashboard)
const deletePinnedMock = vi.mocked(deletePinnedDashboard)
const renamePinnedMock = vi.mocked(renamePinnedDashboard)

const PINNED = [{ id: 7, name: 'Operations', chart_count: 2, preview_charts: [] }]

const DASHBOARDS = [
  { id: 1, name: 'Operations', chart_count: 3, personal: false, created_at: '2026-08-18T00:00:00Z', preview_charts: [] },
  { id: 2, name: 'My scratch', chart_count: 1, personal: true, created_at: '2026-09-16T00:00:00Z', preview_charts: [] },
]

function renderPage(path = '/dashboards/organization') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/dashboards" element={<DashboardsLanding />} />
          <Route path="/dashboards/pinned" element={<DashboardsPage />} />
          <Route path="/dashboards/pinned/:pinnedId" element={<p>pinned page</p>} />
          <Route path="/dashboards/organization" element={<DashboardsPage />} />
          <Route path="/dashboards/mine" element={<DashboardsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const tabLink = (name: string) =>
  within(screen.getByRole('navigation', { name: 'Section' })).getByRole('link', { name })

// The move/copy pair lives behind one folder button.
async function openFolderMenu() {
  await waitFor(() => expect(screen.getByTestId('dashboard-folder-menu')).toBeInTheDocument())
  fireEvent.click(screen.getByTestId('dashboard-folder-menu'))
  await waitFor(() => expect(screen.getByRole('menu')).toBeInTheDocument())
}

beforeEach(() => {
  vi.clearAllMocks()
  getMeMock.mockResolvedValue({ role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
  listMock.mockResolvedValue(DASHBOARDS)
  visibilityMock.mockResolvedValue()
  renameDashboardMock.mockResolvedValue()
  reorderDashboardsMock.mockResolvedValue()
  duplicateMock.mockResolvedValue({ ...DASHBOARDS[1], id: 9, name: 'Copy of Operations' })
  pinnedListMock.mockResolvedValue([])
  pinStateMock.mockResolvedValue({ chart_ids: [], dashboard_ids: [] })
  pinDashboardMock.mockResolvedValue()
  unpinDashboardMock.mockResolvedValue()
  deletePinnedMock.mockResolvedValue()
  renamePinnedMock.mockResolvedValue()
})

describe('DashboardsPage', () => {
  // A failed list must say so, not render as an empty page.
  it('shows the load error with a Retry that refetches', async () => {
    listMock.mockRejectedValueOnce(new Error('Server unavailable'))
    renderPage()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the dashboards: Server unavailable")

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // Deleting an organization dashboard takes it away from everyone, so it
  // asks first and says so.
  it('confirms before deleting an organization dashboard', async () => {
    deleteDashboardMock.mockResolvedValue()
    renderPage()
    await waitFor(() => expect(screen.getByLabelText('Delete dashboard')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Delete dashboard'))

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/"Operations".*everyone in the organization/i)
    expect(deleteDashboardMock).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('dashboard-delete-confirm'))
    await waitFor(() => expect(deleteDashboardMock).toHaveBeenCalledWith(1))
  })

  it('lists only org-wide dashboards on Organization dashboards', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    expect(screen.queryByText('My scratch')).not.toBeInTheDocument()
  })

  it('switches to My dashboards without refetching', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())

    fireEvent.click(tabLink('My dashboards'))

    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    expect(screen.queryByText('Operations')).not.toBeInTheDocument()
    // One list serves both tabs.
    expect(listMock).toHaveBeenCalledTimes(1)
  })

  // Each tab is a route, so a shared URL lands on it.
  it('honours the tab in the URL', async () => {
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    expect(screen.queryByText('Operations')).not.toBeInTheDocument()
    expect(tabLink('My dashboards')).toHaveAttribute('aria-current', 'page')
    expect(tabLink('Organization dashboards')).not.toHaveAttribute('aria-current')
  })

  it('sends an old ?tab=mine link to My dashboards', async () => {
    renderPage('/dashboards?tab=mine')
    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    expect(screen.queryByText('Operations')).not.toBeInTheDocument()
    expect(tabLink('My dashboards')).toHaveAttribute('aria-current', 'page')
  })

  it('explains the empty personal tab rather than showing a bare grid', async () => {
    listMock.mockResolvedValue([DASHBOARDS[0]])
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByText(/share it when it's ready/i)).toBeInTheDocument())
  })

  it('offers a drag handle for reordering', async () => {
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    expect(screen.getByTestId('dashboard-drag-handle')).toBeInTheDocument()
  })

  // Reordering has to be reachable without a mouse.
  it('labels the drag handle for assistive tech', async () => {
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByLabelText(/Reorder My scratch/i)).toBeInTheDocument())
  })

  // Moving is an explicit action, not a second meaning for the drag.
  it('moves a shared dashboard to My dashboards from the card', async () => {
    renderPage()
    await openFolderMenu()
    expect(screen.getByTestId('dashboard-move')).toHaveTextContent('Move to My dashboards')
    fireEvent.click(screen.getByTestId('dashboard-move'))
    await waitFor(() => expect(visibilityMock).toHaveBeenCalledWith(1, true))
  })

  // A personal dashboard moves to Organization dashboards from the card menu.
  it('moves a personal dashboard to Organization dashboards from the card', async () => {
    renderPage('/dashboards/mine')
    await openFolderMenu()
    expect(screen.getByTestId('dashboard-move')).toHaveTextContent('Move to Organization dashboards')
    fireEvent.click(screen.getByTestId('dashboard-move'))
    await waitFor(() => expect(visibilityMock).toHaveBeenCalledWith(2, false))
  })

  // Pins that publishing your OWN dashboard needs no admin role — it is the
  // owner's call, as on the server.
  it('lets a non-admin publish their own dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage('/dashboards/mine')
    await openFolderMenu()
    fireEvent.click(screen.getByTestId('dashboard-move'))
    await waitFor(() => expect(visibilityMock).toHaveBeenCalledWith(2, false))
  })

  // …but taking a SHARED one private removes something the org was using.
  it('hides the move action from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    // The menu still opens — a PM may COPY the org's dashboard into their own
    // space — but it offers no Move.
    await openFolderMenu()
    expect(screen.queryByTestId('dashboard-move')).not.toBeInTheDocument()
    expect(screen.getByTestId('dashboard-copy')).toBeInTheDocument()
  })

  it('copies a shared dashboard into My dashboards', async () => {
    renderPage()
    await openFolderMenu()
    expect(screen.getByTestId('dashboard-copy')).toHaveTextContent('Copy to My dashboards')
    fireEvent.click(screen.getByTestId('dashboard-copy'))
    await waitFor(() => expect(duplicateMock).toHaveBeenCalledWith(1, true))
  })

  it('copies a personal dashboard into Organization dashboards', async () => {
    renderPage('/dashboards/mine')
    await openFolderMenu()
    expect(screen.getByTestId('dashboard-copy')).toHaveTextContent('Copy to Organization dashboards')
    fireEvent.click(screen.getByTestId('dashboard-copy'))
    await waitFor(() => expect(duplicateMock).toHaveBeenCalledWith(2, false))
  })

  // Copying INTO Organization dashboards is publishing, so a non-admin is offered only the move
  // of their own dashboard.
  it('hides copy-to-Organization from a non-admin', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage('/dashboards/mine')
    await openFolderMenu()
    expect(screen.queryByTestId('dashboard-copy')).not.toBeInTheDocument()
    expect(screen.getByTestId('dashboard-move')).toBeInTheDocument()
  })

  it('renames a dashboard from its card', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-rename')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('dashboard-rename'))
    const field = screen.getByLabelText('Rename dashboard')
    fireEvent.change(field, { target: { value: 'Operations EU' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    await waitFor(() => expect(renameDashboardMock).toHaveBeenCalledWith(1, 'Operations EU'))
  })

  // While renaming, the card must stop being a link — an input inside an
  // anchor swallows its own clicks.
  it('swaps the card link for the field while renaming', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-rename')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('dashboard-rename'))
    expect(screen.queryByTestId('dashboard-card')).not.toBeInTheDocument()
    fireEvent.keyDown(screen.getByLabelText('Rename dashboard'), { key: 'Escape' })
    await waitFor(() => expect(screen.getByTestId('dashboard-card')).toBeInTheDocument())
    expect(renameDashboardMock).not.toHaveBeenCalled()
  })

  // While a card is being renamed its own actions are noise: they overlap the
  // field, and dragging a card you are typing into makes no sense.
  it('hides the card actions and the grip while renaming', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('dashboard-rename')).toBeInTheDocument())
    expect(screen.getByTestId('dashboard-drag-handle')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('dashboard-rename'))

    expect(screen.queryByTestId('dashboard-rename')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Delete dashboard')).not.toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-drag-handle')).not.toBeInTheDocument()

    // All three come back when the edit ends.
    fireEvent.keyDown(screen.getByLabelText('Rename dashboard'), { key: 'Escape' })
    await waitFor(() => expect(screen.getByTestId('dashboard-rename')).toBeInTheDocument())
    expect(screen.getByTestId('dashboard-drag-handle')).toBeInTheDocument()
  })

  // Only the card being edited loses its actions — its neighbours stay usable.
  it('leaves other cards untouched while one is renamed', async () => {
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('dashboard-rename'))
    // The Organization dashboards tab's card is not rendered here, so switch
    // and confirm the other card kept its pencil.
    fireEvent.click(tabLink('Organization dashboards'))
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    expect(screen.getByTestId('dashboard-rename')).toBeInTheDocument()
  })

  // Replacing what the whole org sees is not a project manager's call.
  it('offers Reset to defaults on Organization dashboards, to an admin only', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('reset-defaults')).toBeInTheDocument())
  })

  it('hides Reset to defaults on My dashboards', async () => {
    renderPage('/dashboards/mine')
    await waitFor(() => expect(screen.getByText('My scratch')).toBeInTheDocument())
    expect(screen.queryByTestId('reset-defaults')).not.toBeInTheDocument()
  })

  it('hides Reset to defaults from a non-admin', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    expect(screen.queryByTestId('reset-defaults')).not.toBeInTheDocument()
  })

  it('hides rename from a non-admin on a shared dashboard', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    expect(screen.queryByTestId('dashboard-rename')).not.toBeInTheDocument()
  })

  // The New-dashboard button carries the tab: starting from My dashboards
  // creates a private one, and only someone who may publish org-wide is offered
  // the choice at all.
  it('creates a personal dashboard from the My dashboards tab', async () => {
    createDashboardMock.mockResolvedValue({ id: 9, name: 'Scratch', chart_count: 0, personal: true, created_at: '' })
    renderPage('/dashboards/mine')
    fireEvent.click(await screen.findByTestId('new-dashboard'))
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Scratch' } })
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(createDashboardMock).toHaveBeenCalledWith('Scratch', true))
  })

  // The dialog is mounted with the page, so the side has to follow the tab as
  // it is WHEN the button is pressed, not as it was on first load.
  it('follows a tab switch made after the page loaded', async () => {
    createDashboardMock.mockResolvedValue({ id: 9, name: 'Later', chart_count: 0, personal: true, created_at: '' })
    renderPage()
    fireEvent.click(await screen.findByRole('link', { name: 'My dashboards' }))
    fireEvent.click(await screen.findByTestId('new-dashboard'))
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Later' } })
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(createDashboardMock).toHaveBeenCalledWith('Later', true))
  })

  it('creates a shared dashboard from the Organization dashboards tab', async () => {
    createDashboardMock.mockResolvedValue({ id: 9, name: 'Org wide', chart_count: 0, personal: false, created_at: '' })
    renderPage()
    fireEvent.click(await screen.findByTestId('new-dashboard'))
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Org wide' } })
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(createDashboardMock).toHaveBeenCalledWith('Org wide', false))
  })

  // A non-admin cannot publish org-wide, so the dialog must not offer the
  // choice — the server refuses it, and an option that always fails is worse
  // than no option.
  it('hides the visibility choice from a user who cannot publish', async () => {
    getMeMock.mockResolvedValue({ role: 'project_manager' } as Awaited<ReturnType<typeof getMe>>)
    renderPage('/dashboards/mine')
    fireEvent.click(await screen.findByTestId('new-dashboard'))
    await screen.findByTestId('dashboard-name')
    expect(screen.queryByTestId('dashboard-side')).not.toBeInTheDocument()
  })

  // Says which field is missing rather than only greying the button out.
  it('will not submit an unnamed dashboard, and says why', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('new-dashboard'))
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: '   ' } })
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
    expect(createDashboardMock).not.toHaveBeenCalled()
  })

})


describe('Pinned', () => {
  // The tab is always there, so people learn where pins go, but it cannot be
  // opened with nothing in it.
  it('shows Pinned last, disabled with a reason, while nothing is pinned', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText('Operations')).toBeInTheDocument())
    const nav = screen.getByRole('navigation', { name: 'Section' })
    const disabled = within(nav).getByTestId('subnav-disabled-Pinned')
    expect(disabled).toHaveAttribute('aria-disabled', 'true')
    expect(disabled).not.toHaveAttribute('href')
    expect(nav.textContent).toMatch(/^Organization dashboardsMy dashboardsPinned/)
  })

  it('lands on Organization dashboards while nothing is pinned', async () => {
    renderPage('/dashboards')
    await waitFor(() => expect(tabLink('Organization dashboards')).toHaveAttribute('aria-current', 'page'))
    expect(screen.getByText('Operations')).toBeInTheDocument()
  })

  it('lands on Pinned once something is pinned', async () => {
    pinnedListMock.mockResolvedValue(PINNED)
    renderPage('/dashboards')
    await waitFor(() => expect(tabLink('Pinned')).toHaveAttribute('aria-current', 'page'))
    // The pinned card opens inside Pinned, not on the original dashboard.
    expect(screen.getByTestId('dashboard-card')).toHaveAttribute('href', '/dashboards/pinned/7')
    // Nothing is created in Pinned.
    expect(screen.queryByTestId('new-dashboard')).not.toBeInTheDocument()
  })

  it('pins a dashboard from its card, and unpins a pinned one', async () => {
    pinStateMock.mockResolvedValue({ chart_ids: [], dashboard_ids: [1] })
    listMock.mockResolvedValue([DASHBOARDS[0], { ...DASHBOARDS[0], id: 3, name: 'Quality' }])
    renderPage()
    const unpin = await screen.findByRole('button', { name: 'Unpin Operations' })
    expect(unpin).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(unpin)
    await waitFor(() => expect(unpinDashboardMock).toHaveBeenCalledWith(1))

    fireEvent.click(screen.getByRole('button', { name: 'Pin Quality' }))
    await waitFor(() => expect(pinDashboardMock).toHaveBeenCalledWith(3))
  })

  // The pin takes the place of the leading icon: always shown, outside the
  // hover actions, which keep their own show-on-hover behaviour.
  it('shows the pin before the name, outside the hover actions', async () => {
    pinStateMock.mockResolvedValue({ chart_ids: [], dashboard_ids: [1] })
    listMock.mockResolvedValue([DASHBOARDS[0], { ...DASHBOARDS[0], id: 3, name: 'Quality' }])
    renderPage()
    await screen.findByRole('button', { name: 'Unpin Operations' })
    const pins = screen.getAllByTestId('pin-button')
    const actions = screen.getAllByTestId('dashboard-card-actions')
    expect(pins.map(p => p.dataset.pinned)).toEqual(['true', undefined])
    for (const [i, a] of actions.entries()) {
      expect(a).not.toContainElement(pins[i]!)
      expect(a.className).toMatch(/(^| )opacity-0( |$)/)
    }
    for (const p of pins) expect(p.className).not.toMatch(/opacity-0/)
  })

  it('renames a pinned dashboard through the pinned endpoint', async () => {
    pinnedListMock.mockResolvedValue(PINNED)
    renderPage('/dashboards/pinned')
    fireEvent.click(await screen.findByTestId('dashboard-rename'))
    const field = screen.getByLabelText('Rename dashboard')
    fireEvent.change(field, { target: { value: 'My ops' } })
    fireEvent.keyDown(field, { key: 'Enter' })
    await waitFor(() => expect(renamePinnedMock).toHaveBeenCalledWith(7, 'My ops'))
    expect(renameDashboardMock).not.toHaveBeenCalled()
  })

  it('unpins a pinned dashboard after a confirmation', async () => {
    pinnedListMock.mockResolvedValue(PINNED)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderPage('/dashboards/pinned')
    fireEvent.click(await screen.findByRole('button', { name: 'Unpin Operations' }))
    await waitFor(() => expect(deletePinnedMock).toHaveBeenCalledWith(7))
    expect(confirm).toHaveBeenCalledWith(expect.stringMatching(/originals stay/))
    expect(unpinDashboardMock).not.toHaveBeenCalled()
    confirm.mockRestore()
  })
})

describe('resolveDrop', () => {
  const shared = { id: 1, name: 'Operations', chart_count: 3, personal: false, created_at: '', preview_charts: [] }
  const mine = { id: 2, name: 'My scratch', chart_count: 1, personal: true, created_at: '', preview_charts: [] }
  const all = [shared, mine]

  // Drag carries one meaning: order within the tab you are looking at.
  it('a drop on another card reorders', () => {
    expect(resolveDrop(all, 1, 2)).toEqual({ kind: 'reorder', from: 0, to: 1 })
    expect(resolveDrop(all, 2, 1)).toEqual({ kind: 'reorder', from: 1, to: 0 })
  })

  it('a card dropped on itself does nothing', () => {
    expect(resolveDrop(all, 1, 1)).toEqual({ kind: 'none' })
  })

  it('a drop on nothing does nothing', () => {
    expect(resolveDrop(all, 1, undefined)).toEqual({ kind: 'none' })
  })

  // Ids from another tab cannot be dragged in by a stale render.
  it('ignores an id that is not in the list', () => {
    expect(resolveDrop(all, 1, 99)).toEqual({ kind: 'none' })
    expect(resolveDrop(all, 99, 1)).toEqual({ kind: 'none' })
  })
})

