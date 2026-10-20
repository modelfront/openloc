import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'

vi.mock('../hooks/useNotificationStream', () => ({ useNotificationStream: () => {} }))
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    getUnreadCount: vi.fn().mockResolvedValue(1),
    markNotificationRead: vi.fn().mockResolvedValue(undefined),
    listNotifications: vi.fn().mockResolvedValue({
      data: [{
        id: 1, event_type: 'issue_updated', subject: 'Update on your issue: Export does nothing',
        body_preview: 'Status: Needs info. Which format?', reference_type: 'issue', reference_id: 42,
        read: false, emailed: false, created_at: new Date().toISOString(),
      }],
      cursor: 0, has_more: false,
    }),
  }
})

import NotificationBell from './NotificationBell'

function IssueRoute() {
  return <p>Issue page {useParams().issueId}</p>
}

describe('NotificationBell', () => {
  it('opens the issue an issue notification is about', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects']}>
          <NotificationBell />
          <Routes>
            <Route path="/projects" element={<p>Projects</p>} />
            <Route path="/issues/:issueId" element={<IssueRoute />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await userEvent.click(screen.getByTitle('Notifications'))
    await userEvent.click(await screen.findByText('Update on your issue: Export does nothing'))
    expect(await screen.findByText('Issue page 42')).toBeInTheDocument()
  })
})
