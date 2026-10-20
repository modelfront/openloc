import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', () => ({ listPlaybooks: vi.fn(), getMe: vi.fn(), enterDemo: vi.fn() }))
vi.mock('../lib/guide', () => ({ runGuide: vi.fn() }))

import { enterDemo, getMe, listPlaybooks } from '../api'
import { runGuide } from '../lib/guide'
import { announcePlaygroundTourEnd } from '../lib/demo'
import LearnPage from './LearnPage'

const listMock = vi.mocked(listPlaybooks)
const meMock = vi.mocked(getMe)
const enterMock = vi.mocked(enterDemo)
const guideMock = vi.mocked(runGuide)
const OVERVIEW = [{ anchor: 'nav.projects', title: 'Projects', body: 'All work.', path: '/projects' }]
const PLAYBOOKS = [
  { name: 'demo-overview', title: 'Product overview demo', description: 'The whole app.', steps: OVERVIEW, has_tour: true },
  { name: 'guided-only', title: 'Guided', description: 'Needs the assistant.', steps: [], has_tour: false },
  // A saved tour this org has no data for: the demo organization has.
  { name: 'tour-dashboards', title: 'Dashboards tour', description: 'Charts.', steps: [], has_tour: true },
]

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><LearnPage /></MemoryRouter>
    </QueryClientProvider>,
  )
}

let assigned = ''
beforeEach(() => {
  listMock.mockReset()
  guideMock.mockReset()
  enterMock.mockReset()
  listMock.mockResolvedValue(PLAYBOOKS)
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

describe('LearnPage', () => {
  it('lists every saved tour, not guided playbooks, plus the Playground', async () => {
    renderPage()
    expect(await screen.findByTestId('learn-tour-demo-overview')).toHaveTextContent('Product overview demo')
    expect(screen.getByTestId('learn-tour-tour-dashboards')).toHaveTextContent('Charts.')
    expect(screen.queryByTestId('learn-tour-guided-only')).not.toBeInTheDocument()
    expect(screen.getByTestId('learn-playground')).toBeInTheDocument()
  })

  it('outside the demo, Start tour opens it in the demo organization', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('learn-start-tour-dashboards'))
    await vi.waitFor(() => expect(assigned).toBe('/tour/tour-dashboards/1'))
    expect(enterMock).toHaveBeenCalled()
    expect(guideMock).not.toHaveBeenCalled()
  })

  it('inside the demo, Start tour starts it at once and the Playground card is gone', async () => {
    inDemo()
    renderPage()
    fireEvent.click(await screen.findByTestId('learn-start-demo-overview'))
    await vi.waitFor(() => expect(guideMock).toHaveBeenCalledWith(OVERVIEW, expect.any(Function), undefined, { name: 'demo-overview', start: 0, onEnd: announcePlaygroundTourEnd }))
    expect(enterMock).not.toHaveBeenCalled()
    expect(screen.queryByTestId('learn-playground')).not.toBeInTheDocument()
  })

  it('Open the Playground enters the demo without a tour', async () => {
    renderPage()
    fireEvent.click(await screen.findByTestId('learn-open-playground'))
    await vi.waitFor(() => expect(assigned).toBe('/projects'))
    expect(guideMock).not.toHaveBeenCalled()
  })

  it('says so when the server lists no saved tours (the Playground is off)', async () => {
    listMock.mockResolvedValue(PLAYBOOKS.map(pb => ({ ...pb, has_tour: false })))
    renderPage()
    expect(await screen.findByText('No tours are available.')).toBeInTheDocument()
    expect(screen.queryByTestId('learn-playground')).not.toBeInTheDocument()
  })

  it('a failed load shows an error with a retry, not the empty state', async () => {
    listMock.mockRejectedValueOnce(new Error('boom'))
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /retry/i }))
    expect(screen.queryByText('No tours are available.')).not.toBeInTheDocument()
    expect(await screen.findByTestId('learn-tour-demo-overview')).toBeInTheDocument()
  })
})
