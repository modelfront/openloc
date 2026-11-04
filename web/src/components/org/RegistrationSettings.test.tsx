import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import RegistrationSettings from './RegistrationSettings'

const api = vi.hoisted(() => ({ getOrgSettings: vi.fn(), updateOrgSettings: vi.fn() }))
vi.mock('../../api', async (importOriginal) => ({ ...(await importOriginal<object>()), ...api }))

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

function renderSettings() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RegistrationSettings />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  api.getOrgSettings.mockReset()
  api.updateOrgSettings.mockReset()
  toast.success.mockReset()
  toast.error.mockReset()
})
afterEach(cleanup)

describe('RegistrationSettingsSection', () => {
  it('a failed load shows Retry and no switch', async () => {
    api.getOrgSettings.mockRejectedValueOnce(new Error('server unavailable'))
    renderSettings()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the registration settings: server unavailable")
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()

    api.getOrgSettings.mockResolvedValueOnce({ open_registration: false, domain: 'example.com' })
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('switch')).not.toBeChecked()
    expect(screen.getByRole('region', { name: 'Registration' })).toHaveTextContent('Invite only')
    expect(screen.getByText('@example.com')).toBeInTheDocument()
  })

  it('the switch turns open registration on and shows the saved state', async () => {
    api.getOrgSettings
      .mockResolvedValueOnce({ open_registration: false, domain: null })
      .mockResolvedValue({ open_registration: true, domain: null })
    api.updateOrgSettings.mockResolvedValue({ open_registration: true, domain: null })
    renderSettings()

    await userEvent.click(await screen.findByRole('switch'))

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Open registration enabled'))
    expect(api.updateOrgSettings).toHaveBeenCalledWith({ open_registration: true })
    expect(screen.getByText('Anyone with organization email can join')).toBeInTheDocument()
  })
})

describe('RegistrationSettingsSection header', () => {
  it('says the current mode in its header, with the switch shown', async () => {
    api.getOrgSettings.mockResolvedValue({ open_registration: true, domain: 'example.com' })
    renderSettings()

    const section = screen.getByRole('region', { name: 'Registration' })
    expect(await within(section).findByText(/Open to @example.com/)).toBeVisible()
    expect(within(section).getByRole('switch')).toBeVisible()
  })
})
