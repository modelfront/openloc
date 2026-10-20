import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', () => ({ listPlaybooks: vi.fn(), getMe: vi.fn(), enterDemo: vi.fn() }))
vi.mock('../../lib/guide', () => ({ runGuide: vi.fn() }))

import { enterDemo, getMe, listPlaybooks } from '../../api'
import { runGuide } from '../../lib/guide'
import { announcePlaygroundTourEnd } from '../../lib/demo'
import { usePlaybooks } from '../../lib/playbooks'
import { menuItem } from '@/test/menu'
import { TakeTourButton } from './TakeTourButton'

const listMock = vi.mocked(listPlaybooks)
const meMock = vi.mocked(getMe)
const enterMock = vi.mocked(enterDemo)
const guideMock = vi.mocked(runGuide)
const OVERVIEW = [{ anchor: 'nav.projects', title: 'Projects', body: 'All work.', path: '/projects' }]
const AI = [{ anchor: 'ai_routers.list', title: 'Routers', body: 'Per language.', path: '/ai' }]
const TOURS = [
  { name: 'demo-overview', title: 'Product overview demo', description: 'The whole app.', steps: OVERVIEW, has_tour: true },
  { name: 'setup-ai-review', title: 'Set up AI review', description: 'Engine to project.', steps: AI, has_tour: true },
  { name: 'guided-only', title: 'Guided', description: 'Needs the assistant.', steps: [], has_tour: false },
  // A saved tour this org has no data for: the demo organization has.
  { name: 'tour-dashboards', title: 'Dashboards', description: 'Charts.', steps: [], has_tour: true },
]

function renderWith(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

// Shows up in the same render that hands the playbooks to the components
// beside it, so "nothing rendered" is checked after the data arrived.
function PlaybooksLoaded() {
  return usePlaybooks().isSuccess ? <span data-testid="playbooks-loaded" /> : null
}

let assigned = ''
beforeEach(() => {
  listMock.mockReset()
  guideMock.mockReset()
  enterMock.mockReset()
  listMock.mockResolvedValue(TOURS)
  meMock.mockResolvedValue({ id: 1, email: 'a@example.com', name: 'A', needs_org_selection: false })
  enterMock.mockResolvedValue({ token: 't' })
  assigned = ''
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, set href(v: string) { assigned = v }, get href() { return assigned } },
  })
})

const inDemo = () =>
  meMock.mockResolvedValue({ id: 9, email: 'guide@demo.example.com', name: 'Guide', needs_org_selection: false, demo: true })

describe('TakeTourButton', () => {
  it('outside the demo, a named tour opens it in the demo organization', async () => {
    renderWith(<TakeTourButton playbook="tour-dashboards" />)
    fireEvent.click(await screen.findByTestId('take-tour-tour-dashboards'))
    await vi.waitFor(() => expect(assigned).toBe('/tour/tour-dashboards/1'))
    expect(enterMock).toHaveBeenCalled()
    expect(guideMock).not.toHaveBeenCalled()
  })

  it('inside the demo, a named tour starts at once with fresh steps', async () => {
    inDemo()
    renderWith(<TakeTourButton playbook="demo-overview" />)
    fireEvent.click(await screen.findByTestId('take-tour-demo-overview'))
    await vi.waitFor(() => expect(guideMock).toHaveBeenCalledWith(OVERVIEW, expect.any(Function), undefined, { name: 'demo-overview', start: 0, onEnd: announcePlaygroundTourEnd }))
    expect(enterMock).not.toHaveBeenCalled()
  })

  it('the menu offers every saved tour, even one this org has no data for, and the demo itself', async () => {
    renderWith(<TakeTourButton />)
    fireEvent.click(await screen.findByTestId('take-tour-menu'))
    expect(await menuItem('take-tour-tour-dashboards')).toBeInTheDocument()
    expect(screen.queryByTestId('take-tour-guided-only')).not.toBeInTheDocument()
    fireEvent.click(await menuItem('take-tour-setup-ai-review'))
    await vi.waitFor(() => expect(assigned).toBe('/tour/setup-ai-review/1'))
  })

  it('offers nothing when the server lists no saved tours (the Playground is off)', async () => {
    listMock.mockResolvedValue(TOURS.map(pb => ({ ...pb, has_tour: false })))
    renderWith(<><PlaybooksLoaded /><TakeTourButton /><TakeTourButton playbook="tour-dashboards" /></>)
    await screen.findByTestId('playbooks-loaded')
    expect(screen.queryByTestId('take-tour-menu')).toBeNull()
    expect(screen.queryByTestId('take-tour-tour-dashboards')).toBeNull()
  })

  it('the menu opens the demo organization without a tour', async () => {
    renderWith(<TakeTourButton />)
    fireEvent.click(await screen.findByTestId('take-tour-menu'))
    fireEvent.click(await menuItem('take-tour-demo'))
    await vi.waitFor(() => expect(assigned).toBe('/projects'))
  })

  it('inside the demo, the menu starts tours in place and has no demo entry', async () => {
    inDemo()
    renderWith(<TakeTourButton />)
    fireEvent.click(await screen.findByTestId('take-tour-menu'))
    const aiTour = await menuItem('take-tour-setup-ai-review')
    // The menu is open and populated here; after the click it closes, and an
    // absence check would pass for any menu.
    expect(screen.queryByTestId('take-tour-demo')).not.toBeInTheDocument()
    expect(screen.queryByTestId('take-tour-tour-dashboards')).not.toBeInTheDocument()
    fireEvent.click(aiTour)
    await vi.waitFor(() => expect(guideMock).toHaveBeenCalledWith(AI, expect.any(Function), undefined, { name: 'setup-ai-review', start: 0, onEnd: announcePlaygroundTourEnd }))
  })

  it('renders nothing when the named playbook has no tour for this role', async () => {
    renderWith(<><TakeTourButton playbook="guided-only" /><PlaybooksLoaded /></>)
    await screen.findByTestId('playbooks-loaded')
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})
