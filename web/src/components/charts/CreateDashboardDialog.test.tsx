// "New dashboard" dialog. The side follows the tab the
// button was pressed on, and a tab change that renders while the dialog is
// open must not wipe what the user already typed.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, createDashboard: vi.fn() }
})

import { createDashboard } from '@/api'
import { CreateDashboardDialog } from './CreateDashboardDialog'

const createMock = vi.mocked(createDashboard)

function setup(personal: boolean, onClose = () => {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const wrap = (ui: ReactElement) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )
  const dialog = (p: boolean) => <CreateDashboardDialog open onClose={onClose} personal={p} canShare />
  const utils = render(wrap(dialog(personal)))
  return { ...utils, rerenderWith: (p: boolean) => utils.rerender(wrap(dialog(p))) }
}

describe('CreateDashboardDialog', () => {
  beforeEach(() => {
    createMock.mockReset()
    createMock.mockResolvedValue({ id: 9, name: 'Late', chart_count: 0, personal: true, created_at: '' })
  })

  // The tab switch is a router transition, so it can render after the dialog
  // opened and the name was typed.
  it('keeps the typed name when the tab changes while the dialog is open', async () => {
    const { rerenderWith } = setup(false)
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Late' } })
    rerenderWith(true)
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(createMock).toHaveBeenCalledWith('Late', true))
  })

  // A side the user picked by hand wins over a late tab change.
  it('keeps a side the user chose when the tab changes', async () => {
    const { rerenderWith } = setup(true)
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Org' } })
    fireEvent.click(screen.getByRole('radio', { name: 'Everyone' }))
    rerenderWith(false)
    rerenderWith(true)
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(createMock).toHaveBeenCalledWith('Org', false))
  })

  // Closing mid-save would drop the typed name if the request then fails.
  it('stays open on Escape and disables Cancel while the create runs', async () => {
    createMock.mockReturnValue(new Promise(() => {}))
    const onClose = vi.fn()
    setup(true, onClose)
    fireEvent.change(await screen.findByTestId('dashboard-name'), { target: { value: 'Pending' } })
    fireEvent.click(screen.getByTestId('create-dashboard-submit'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled())
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
  })
})
