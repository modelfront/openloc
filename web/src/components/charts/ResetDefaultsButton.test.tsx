import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getSharedDashboardCount: vi.fn(), resetSharedDashboards: vi.fn() }
})

import { getSharedDashboardCount, resetSharedDashboards } from '@/api'
import { ResetDefaultsButton } from './ResetDefaultsButton'

const countMock = vi.mocked(getSharedDashboardCount)
const resetMock = vi.mocked(resetSharedDashboards)

function renderButton() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><ResetDefaultsButton /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  countMock.mockResolvedValue({ shared: 4, defaults: 3, pinned_users: 0 })
  resetMock.mockResolvedValue()
})

describe('ResetDefaultsButton', () => {
  // It deletes work, so it must never fire from one click.
  it('asks before doing anything', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(resetMock).not.toHaveBeenCalled()
  })

  // A vague warning invites a reflexive confirm; a number does not.
  it('names how many dashboards will be deleted', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByText(/all 4 organization dashboards/)).toBeInTheDocument())
    expect(screen.getByText(/3 built-in ones/)).toBeInTheDocument()
  })

  // The consequence nobody anticipates: share links point at charts, and the
  // charts go with their dashboards.
  it('warns that share links will break, and that personal dashboards are safe', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByText(/share links.*will stop working/i)).toBeInTheDocument())
    expect(screen.getByText(/personal dashboards are not affected/i)).toBeInTheDocument()
  })

  // Pins are references: other people's Pinned loses the org charts too.
  it('says how many people lose pinned charts', async () => {
    countMock.mockResolvedValue({ shared: 4, defaults: 3, pinned_users: 2 })
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByText(/leave 2 people's Pinned/)).toBeInTheDocument())
  })

  it('does not mention Pinned when nobody pinned an org chart', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByText(/all 4 organization dashboards/)).toBeInTheDocument())
    expect(screen.queryByText(/Pinned/)).not.toBeInTheDocument()
  })

  it('resets only after the destructive confirmation', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByTestId('reset-defaults-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('reset-defaults-confirm'))
    await waitFor(() => expect(resetMock).toHaveBeenCalledTimes(1))
  })

  it('cancelling changes nothing', async () => {
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(resetMock).not.toHaveBeenCalled()
  })

  it('stays open on Escape and disables Cancel while the reset runs', async () => {
    resetMock.mockReturnValue(new Promise(() => {}))
    renderButton()
    fireEvent.click(screen.getByTestId('reset-defaults'))
    await waitFor(() => expect(screen.getByTestId('reset-defaults-confirm')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('reset-defaults-confirm'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled())
    await userEvent.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
