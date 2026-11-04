import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { AttentionProvider, useAttentionTarget } from '../hooks/useAttention'
import { ORG_VENDORS_KEY } from '../pages/org/org-paths'

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
      }, {
        id: 2, event_type: 'vendor_admin_added', subject: 'Acme Vendors has a new admin: Lina (lina@acme.test)',
        body_preview: 'Promoted by Vera.',
        reference_type: 'vendor_org', reference_id: 7,
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

/** The Members tab, reduced to the section the vendor notices point at. */
function OrgRoute() {
  return (
    <>
      <p>Members page {useLocation().pathname}</p>
      <section data-testid="vendors-section" ref={useAttentionTarget(ORG_VENDORS_KEY)}>Vendors</section>
    </>
  )
}

function renderBell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects']}>
        <AttentionProvider>
          <NotificationBell />
          <Routes>
            <Route path="/projects" element={<p>Projects</p>} />
            <Route path="/issues/:issueId" element={<IssueRoute />} />
            <Route path="/org" element={<OrgRoute />} />
          </Routes>
        </AttentionProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('NotificationBell', () => {
  it('opens the issue an issue notification is about', async () => {
    renderBell()
    await userEvent.click(screen.getByTitle('Notifications'))
    await userEvent.click(await screen.findByText('Update on your issue: Export does nothing'))
    expect(await screen.findByText('Issue page 42')).toBeInTheDocument()
  })

  it('opens the Members tab pointing at the Vendors section for a new vendor admin', async () => {
    renderBell()
    await userEvent.click(screen.getByTitle('Notifications'))
    await userEvent.click(await screen.findByText('Acme Vendors has a new admin: Lina (lina@acme.test)'))
    expect(await screen.findByText('Members page /org')).toBeInTheDocument()
    expect(screen.getByTestId('vendors-section').dataset.attention).toBe('')
  })
})
