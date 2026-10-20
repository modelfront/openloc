import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import NotificationRules from './NotificationRules'

const api = vi.hoisted(() => ({
  getOrgNotificationRules: vi.fn(),
  updateOrgNotificationRules: vi.fn(),
}))
vi.mock('../../api', async (importOriginal) => ({ ...(await importOriginal<object>()), ...api }))

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

// The server's real rules: deliberately mixed, so a PUT built from invented
// all-true defaults is told apart from one built from what the server holds.
const serverRules = {
  notify_assignment: true,
  notify_rejection: false,
  notify_completion: false,
  notify_deadline: true,
}

function renderRules() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <NotificationRules />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  api.getOrgNotificationRules.mockReset()
  api.updateOrgNotificationRules.mockReset()
  toast.success.mockReset()
  toast.error.mockReset()
})
afterEach(cleanup)

describe('OrgNotificationRulesSection', () => {
  it('a failed load shows Retry and no toggles, so no PUT can overwrite the server rules', async () => {
    api.getOrgNotificationRules.mockRejectedValueOnce(new Error('server unavailable'))
    renderRules()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the notification rules: server unavailable")
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(api.updateOrgNotificationRules).not.toHaveBeenCalled()

    // Retry loads the real rules; the toggles then show the server's values.
    api.getOrgNotificationRules.mockResolvedValueOnce(serverRules)
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('checkbox', { name: /Task assignment/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /Step rejection/ })).not.toBeChecked()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('toggling one rule sends the server values for the other three', async () => {
    const saved = { ...serverRules, notify_deadline: false }
    api.getOrgNotificationRules.mockResolvedValueOnce(serverRules).mockResolvedValue(saved)
    api.updateOrgNotificationRules.mockResolvedValue(saved)
    renderRules()

    await userEvent.click(await screen.findByRole('checkbox', { name: /Deadline approaching/ }))

    await waitFor(() => expect(api.updateOrgNotificationRules).toHaveBeenCalledTimes(1))
    expect(api.updateOrgNotificationRules.mock.calls[0][0]).toEqual(saved)
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Notification rules saved'))
    expect(screen.getByRole('checkbox', { name: /Deadline approaching/ })).not.toBeChecked()
  })

  it('a failed save puts the toggle back to the server value', async () => {
    api.getOrgNotificationRules.mockResolvedValue(serverRules)
    api.updateOrgNotificationRules.mockRejectedValue(new Error('forbidden'))
    renderRules()

    await userEvent.click(await screen.findByRole('checkbox', { name: /Workflow completion/ }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('Failed to save notification rules')))
    expect(screen.getByRole('checkbox', { name: /Workflow completion/ })).not.toBeChecked()
  })
})
