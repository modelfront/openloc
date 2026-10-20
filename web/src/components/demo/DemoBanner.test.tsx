import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return { ...actual, getMe: vi.fn(), leaveDemo: vi.fn() }
})

import { getMe, leaveDemo } from '../../api'
import { DemoBanner } from './DemoBanner'
import { announcePlaygroundTourEnd } from '../../lib/demo'

let assigned = ''
beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue({ id: 9, email: 'guide@demo.example.com', name: 'Alex Morgan', needs_org_selection: false, demo: true, demo_return_org_name: 'Acme Localization' })
  vi.mocked(leaveDemo).mockReset()
  assigned = ''
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, set href(v: string) { assigned = v }, get href() { return assigned } },
  })
})

function renderBanner() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><DemoBanner /></QueryClientProvider>)
}

describe('DemoBanner', () => {
  it('names the org the visitor returns to and goes back there', async () => {
    vi.mocked(leaveDemo).mockResolvedValue({ token: 't', needs_org_selection: false })
    renderBanner()
    fireEvent.click(await screen.findByRole('button', { name: 'Back to Acme Localization' }))
    await vi.waitFor(() => expect(assigned).toBe('/'))
  })

  it('sends a visitor without an org to the org picker', async () => {
    vi.mocked(leaveDemo).mockResolvedValue({ token: 't', needs_org_selection: true })
    renderBanner()
    fireEvent.click(await screen.findByRole('button', { name: 'Back to Acme Localization' }))
    await vi.waitFor(() => expect(assigned).toBe('/select-org'))
  })
})

describe('the end of a Playground tour', () => {
  it('asks whether to stay, and goes back to the org on request', async () => {
    vi.mocked(leaveDemo).mockResolvedValue({ needs_org_selection: false } as Awaited<ReturnType<typeof leaveDemo>>)
    renderBanner()
    await screen.findByRole('button', { name: 'Back to Acme Localization' })
    act(() => announcePlaygroundTourEnd(true))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Tour complete')).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Back to Acme Localization' }))
    await vi.waitFor(() => expect(assigned).toBe('/'))
    expect(leaveDemo).toHaveBeenCalled()
  })
})
