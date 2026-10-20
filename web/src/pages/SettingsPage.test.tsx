import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import SettingsPage from './SettingsPage'
import type { UserProfile } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const getMe = vi.fn()
const updateMe = vi.fn()
vi.mock('../api', () => ({
  getMe: () => getMe(),
  updateMe: (data: unknown) => updateMe(data),
  getNotificationPreferences: () =>
    Promise.resolve({ email_assignment: true, email_rejection: true, email_completion: true, email_deadline: true, email_issue_updates: true }),
  updateNotificationPreferences: vi.fn(),
  listOAuthConnections: () => Promise.resolve([]),
  revokeOAuthConnection: vi.fn(),
}))
// The appearance hook imports this module directly, not through '../api'.
vi.mock('../api/appearance', async () => {
  const { DEFAULT_DARK_ID, DEFAULT_LIGHT_ID } = await import('../lib/palettes')
  return {
    getAppearance: () =>
      Promise.resolve({ light_palette: DEFAULT_LIGHT_ID, dark_palette: DEFAULT_DARK_ID, custom_palettes: [] }),
    updateAppearance: vi.fn(),
  }
})

const REASON = "The profile can't be changed while you are logged in as an organization."

const profile: UserProfile = {
  id: 1,
  email: 'test@test.com',
  name: 'Org Owner',
  needs_org_selection: false,
  role: 'owner',
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><SettingsPage /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  updateMe.mockImplementation((data: { name: string }) => Promise.resolve({ ...profile, ...data }))
})

// A platform operator logged in as an organization sees the org owner's
// profile, which the server refuses to save.
describe('SettingsPage profile while impersonating', () => {
  beforeEach(() => {
    getMe.mockResolvedValue({ ...profile, impersonating: true })
  })

  it('shows the name and email read-only, and Save sends nothing', async () => {
    const user = userEvent.setup()
    renderPage()
    const name = await screen.findByPlaceholderText('Your name')
    expect(name).toHaveAttribute('readonly')
    expect(screen.getByDisplayValue('test@test.com')).toHaveAttribute('readonly')

    await user.type(name, ' edited')
    expect(name).toHaveValue('Org Owner')

    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(updateMe).not.toHaveBeenCalled()
  })

  it('Save says why it is disabled', async () => {
    const user = userEvent.setup()
    renderPage()
    const save = await screen.findByRole('button', { name: 'Save changes' })
    await user.hover(save)
    expect(await screen.findByText(REASON)).toBeInTheDocument()
    // Disabled for assistive tech, yet focusable, so the reason is reachable.
    expect(save).toHaveAttribute('aria-disabled', 'true')
  })
})

describe('SettingsPage profile', () => {
  beforeEach(() => {
    getMe.mockResolvedValue(profile)
  })

  it('the name is editable and Save sends it', async () => {
    const user = userEvent.setup()
    renderPage()
    const name = await screen.findByPlaceholderText('Your name')
    expect(name).not.toHaveAttribute('readonly')
    const save = screen.getByRole('button', { name: 'Save changes' })
    expect(save).toBeEnabled()
    expect(save).not.toHaveAttribute('aria-disabled')

    await user.clear(name)
    await user.type(name, 'New Name')
    await user.click(save)
    await waitFor(() => expect(updateMe).toHaveBeenCalledWith({ name: 'New Name' }))
  })

  it('the name stops at 200 characters, the same limit as the server', async () => {
    const user = userEvent.setup()
    renderPage()
    const name = await screen.findByPlaceholderText('Your name')
    await user.clear(name)
    await user.paste('x'.repeat(201))
    expect(name).toHaveValue('x'.repeat(200))
  })

  it('while saving, Save gives no reason', async () => {
    updateMe.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Save changes' }))
    const saving = await screen.findByRole('button', { name: 'Saving...' })
    await user.hover(saving)
    expect(screen.queryByText(REASON)).not.toBeInTheDocument()
  })
})
