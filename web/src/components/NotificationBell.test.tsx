import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import NotificationBell, { NotificationPreview } from './NotificationBell'
import type { Notification } from '../api'

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  getUnreadCount: vi.fn(),
  listNotifications: vi.fn(),
  markNotificationRead: vi.fn(),
  markAllNotificationsRead: vi.fn(),
}))
vi.mock('../hooks/useNotificationStream', () => ({ useNotificationStream: () => {} }))

const api = await import('../api')

const UNREAD: Notification = {
  id: 7, tenant_id: 1, user_id: 1, event_type: 'assignment', subject: 'You were assigned a task',
  body_preview: '', reference_type: 'job', reference_id: 3, read: false, emailed: false,
  created_at: new Date().toISOString(),
}

function renderBell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={qc}><NotificationBell /></QueryClientProvider></MemoryRouter>)
}

describe('NotificationBell', () => {
  beforeEach(() => {
    vi.mocked(api.getUnreadCount).mockResolvedValue(1)
    vi.mocked(api.listNotifications).mockResolvedValue({ data: [UNREAD], cursor: 0, has_more: false })
    vi.mocked(api.markNotificationRead).mockResolvedValue(undefined)
  })

  it('shows the unread count and opens the menu with the notifications', async () => {
    renderBell()
    const bell = screen.getByTitle('Notifications')
    await waitFor(() => expect(bell).toHaveTextContent('1'))

    await userEvent.click(bell)
    const menu = await screen.findByTestId('notification-menu')
    expect(menu).toHaveTextContent('You were assigned a task')
  })

  it('closes the menu on Escape', async () => {
    renderBell()
    await userEvent.click(screen.getByTitle('Notifications'))
    await screen.findByTestId('notification-menu')

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('notification-menu')).toBeNull())
  })

  it('marks a notification read and closes the menu when it is clicked', async () => {
    renderBell()
    await userEvent.click(screen.getByTitle('Notifications'))
    await userEvent.click(await screen.findByText('You were assigned a task'))

    await waitFor(() => expect(vi.mocked(api.markNotificationRead).mock.calls[0]?.[0]).toBe(7))
    await waitFor(() => expect(screen.queryByTestId('notification-menu')).toBeNull())
  })
})

const LONG = 'The AI step could not complete: Acme QE: Acme QE returned status 400: {"message":"This model requires metadata."}'

function clip(el: HTMLElement, clipped: boolean) {
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: clipped ? 80 : 32 })
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: 32 })
}

describe('NotificationPreview', () => {
  it('shows the whole text in a tooltip when the preview is cut', async () => {
    render(<NotificationPreview text={LONG} />)
    const preview = screen.getByTestId('notification-preview')
    clip(preview, true)
    await userEvent.hover(preview)
    await waitFor(() => expect(screen.getByTestId('notification-preview-full')).toHaveTextContent('This model requires metadata.'))
  })

  it('adds no tooltip when the whole text already fits', async () => {
    render(<NotificationPreview text="Short reason" />)
    const preview = screen.getByTestId('notification-preview')
    clip(preview, false)
    await userEvent.hover(preview)
    await new Promise(r => setTimeout(r, 300))
    expect(screen.queryByTestId('notification-preview-full')).toBeNull()
  })
})
