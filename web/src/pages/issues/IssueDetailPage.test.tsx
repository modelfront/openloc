import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return {
    ...actual, getIssue: vi.fn(), getMe: vi.fn(), confirmIssueFixed: vi.fn(), commentOnIssue: vi.fn(),
    addIssueNote: vi.fn(), listIssueAssignees: vi.fn(), listIssueTags: vi.fn(), updateIssue: vi.fn(), deleteIssue: vi.fn(),
    changeIssueKind: vi.fn(),
  }
})

import {
  addIssueNote, changeIssueKind, commentOnIssue, confirmIssueFixed, deleteIssue, getIssue, getMe, listIssueAssignees, listIssueTags, updateIssue,
  type Issue, type IssueComment, type IssueDetail, type IssueScope,
} from '@/api'
import IssueDetailPage from './IssueDetailPage'

const issue = (over: Partial<Issue> = {}): Issue => ({
  id: 7, kind: 'bug', severity: 'major', area: 'editor', title: 'Export loses tables', description: 'It does.',
  status: 'open', context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-15T10:00:00Z',
  updated_at: '2026-10-15T10:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Rita',
  reporter_email: 'rita@example.com', can_delete: false, ...over,
})

const entry = (over: Partial<IssueComment>): IssueComment => ({
  id: 1, author_id: 99, author_side: 'operator', author_name: 'Sam', body: null, status: null, attachments: [],
  created_at: '2026-10-15T11:00:00Z', ...over,
})

function renderPage(scope: IssueScope, detail: IssueDetail) {
  vi.mocked(getIssue).mockResolvedValue(detail)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const path = scope === 'mine' ? '/issues/:issueId' : '/platform/issues/:issueId'
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[scope === 'mine' ? '/issues/7' : '/platform/issues/7']}>
        <Routes>
          <Route path={path} element={<IssueDetailPage scope={scope} />} />
          <Route path="/issues" element={<p>issue list</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue({ id: 1 } as Awaited<ReturnType<typeof getMe>>)
  vi.mocked(confirmIssueFixed).mockReset()
  vi.mocked(commentOnIssue).mockReset()
  vi.mocked(addIssueNote).mockReset()
  vi.mocked(deleteIssue).mockReset()
  vi.mocked(changeIssueKind).mockReset()
  vi.mocked(deleteIssue).mockResolvedValue()
  vi.mocked(listIssueAssignees).mockResolvedValue([{ id: 99, name: 'Sam', email: 'sam@example.com' }])
  vi.mocked(listIssueTags).mockResolvedValue(['mvp'])
})

describe('IssueDetailPage for the reporter\'s org', () => {
  it('asks whether a resolved fix works, and records the confirmation', async () => {
    renderPage('mine', { issue: issue({ status: 'resolved' }), comments: [] })
    const confirmed = {
      issue: issue({ status: 'resolved', confirmed_at: '2026-10-16T09:00:00Z' }),
      comments: [entry({ author_id: 1, author_side: 'reporter', change: { field: 'confirmed' } })],
    }
    // The server holds the confirmation from now on, for the page's re-read too.
    vi.mocked(confirmIssueFixed).mockImplementation(async () => {
      vi.mocked(getIssue).mockResolvedValue(confirmed)
      return confirmed
    })
    await userEvent.click(await screen.findByTestId('issue-confirm-fixed'))
    expect(confirmIssueFixed).toHaveBeenCalledWith(7)
    await waitFor(() => expect(screen.queryByTestId('issue-confirm-fix')).not.toBeInTheDocument())
    expect(screen.getByTestId('issue-confirmed')).toHaveTextContent('Fix confirmed')
    expect(screen.getByTestId('issue-comment-action')).toHaveTextContent('confirmed the fix')
  })

  it('points at the reply box when the fix does not work', async () => {
    renderPage('mine', { issue: issue({ status: 'resolved' }), comments: [] })
    await userEvent.click(await screen.findByTestId('issue-still-broken'))
    expect(screen.getByTestId('issue-comment-input')).toHaveAttribute('placeholder', 'What is still broken? Your reply reopens the issue.')
    expect(confirmIssueFixed).not.toHaveBeenCalled()
  })

  it('does not ask again once confirmed, nor before the issue is resolved', async () => {
    renderPage('mine', { issue: issue({ status: 'resolved', confirmed_at: '2026-10-16T09:00:00Z' }), comments: [] })
    await screen.findByTestId('issue-confirmed')
    expect(screen.queryByTestId('issue-confirm-fix')).not.toBeInTheDocument()
  })

  it('says who has to act next, and who works on it', async () => {
    renderPage('mine', { issue: issue({ last_activity_by: 'operator', assignee: { name: 'Sam' } }), comments: [] })
    expect(await screen.findByTestId('issue-waiting-on')).toHaveTextContent('Your organization')
    expect(screen.getByTestId('issue-assignee')).toHaveTextContent('Assigned to Sam')
    expect(screen.queryByTestId('issue-confirm-fix')).not.toBeInTheDocument()
  })

  it('links the original of a duplicate it may open', async () => {
    renderPage('mine', { issue: issue({ status: 'duplicate', duplicate_of: { id: 3, title: 'Tables vanish', status: 'open', can_open: true } }), comments: [] })
    const link = await screen.findByTestId('issue-duplicate-link')
    expect(link).toHaveTextContent('#3 Tables vanish')
    expect(link).toHaveAttribute('href', '/issues/3')
    expect(screen.queryByTestId('issue-waiting-on')).not.toBeInTheDocument()
  })

  it('gives the number and status of an original it may not open, without a title or a link', async () => {
    renderPage('mine', { issue: issue({ status: 'duplicate', duplicate_of: { id: 3, status: 'in_progress', can_open: false } }), comments: [] })
    const notice = await screen.findByTestId('issue-duplicate-notice')
    expect(notice).toHaveTextContent('The support team tracks this in#3In progress')
    expect(within(notice).queryByRole('link')).not.toBeInTheDocument()
  })

  it("links a support team member to the support team's page of an original they may not open here", async () => {
    vi.mocked(getMe).mockResolvedValue({ id: 1, is_platform_admin: true } as Awaited<ReturnType<typeof getMe>>)
    renderPage('mine', { issue: issue({ status: 'duplicate', duplicate_of: { id: 3, status: 'open', can_open: false } }), comments: [] })
    await waitFor(() => expect(screen.getByTestId('issue-duplicate-link')).toHaveAttribute('href', '/platform/issues/3'))
  })

  it('says when the original is gone', async () => {
    renderPage('mine', { issue: issue({ status: 'duplicate' }), comments: [] })
    expect(await screen.findByTestId('issue-duplicate-notice')).toHaveTextContent('The original issue no longer exists.')
  })

  it('changes the kind with the same picker the support team uses, and shows the entry', async () => {
    const changed = {
      issue: issue({ severity: 'major' }),
      comments: [entry({ author_id: 1, author_side: 'reporter', change: { field: 'kind', from: 'feature_request', to: 'bug' } })],
    }
    // The server holds the change from now on, for the page's re-read too.
    vi.mocked(changeIssueKind).mockImplementation(async () => {
      vi.mocked(getIssue).mockResolvedValue(changed)
      return changed
    })
    renderPage('mine', { issue: issue({ kind: 'feature_request', severity: null }), comments: [] })
    await userEvent.click(await screen.findByTestId('issue-kind-select'))
    await userEvent.click(screen.getByTestId('issue-kind-select-option-bug'))
    expect(changeIssueKind).toHaveBeenCalledWith(7, { kind: 'bug' })
    expect(await screen.findByTestId('issue-comment-action')).toHaveTextContent('changed the kind to Bug')
  })

  it('sends nothing when the same kind is picked again', async () => {
    renderPage('mine', { issue: issue(), comments: [] })
    await userEvent.click(await screen.findByTestId('issue-kind-select'))
    await userEvent.click(screen.getByTestId('issue-kind-select-option-bug'))
    expect(changeIssueKind).not.toHaveBeenCalled()
  })
})

describe('IssueDetailPage for the support team', () => {
  const detail: IssueDetail = {
    issue: issue({ priority: 'high', tags: ['mvp'], assignee_id: 99, assignee: { name: 'Sam' } }),
    comments: [
      entry({ id: 1, internal: true, change: { field: 'priority', to: 'high' } }),
      entry({ id: 2, change: { field: 'assignee', to: 'Sam' } }),
      entry({ id: 3, internal: true, body: 'Root cause found' }),
      entry({ id: 4, status: 'duplicate', change: { field: 'duplicate', to: '3' } }),
      entry({ id: 5, internal: true, change: { field: 'kind', from: 'bug', to: 'feature_request' } }),
    ],
  }

  it('shows internal entries as such, and what each change did', async () => {
    renderPage('platform', detail)
    const entries = await screen.findAllByTestId('issue-comment')
    expect(entries.map(e => e.dataset.internal === 'true')).toEqual([true, false, true, false, true])
    expect(within(entries[0]).getByTestId('issue-comment-action')).toHaveTextContent('set the priority to High')
    expect(within(entries[1]).getByTestId('issue-comment-action')).toHaveTextContent('assigned it to Sam')
    expect(within(entries[2]).getByTestId('issue-comment-internal')).toHaveTextContent('Internal')
    expect(within(entries[3]).getByRole('link', { name: '#3' })).toHaveAttribute('href', '/platform/issues/3')
    expect(within(entries[4]).getByTestId('issue-comment-action')).toHaveTextContent('changed the kind to Feature request')
    expect(screen.getByTestId('issue-waiting-on')).toHaveTextContent('Support team')
  })

  it('lists the duplicates of an original, with their titles, statuses and links', async () => {
    renderPage('platform', {
      ...detail,
      issue: { ...detail.issue, duplicates: [{ id: 12, title: 'Same export bug', status: 'duplicate', can_open: true }] },
    })
    const link = await screen.findByTestId('issue-duplicates-link')
    expect(link).toHaveTextContent('#12 Same export bug')
    expect(link).toHaveAttribute('href', '/platform/issues/12')
    expect(screen.getByTestId('issue-duplicates-link-status')).toHaveTextContent('Duplicate')
  })

  it("links a duplicate to its original with the original's title and status", async () => {
    renderPage('platform', {
      ...detail,
      issue: { ...detail.issue, status: 'duplicate', duplicate_of: { id: 3, title: 'Tables vanish', status: 'resolved', can_open: true } },
    })
    const link = await screen.findByTestId('issue-duplicate-link')
    expect(link).toHaveTextContent('#3 Tables vanish')
    expect(link).toHaveAttribute('href', '/platform/issues/3')
    expect(screen.getByTestId('issue-duplicate-link-status')).toHaveTextContent('Resolved')
    // The summary's own status chip stays the only one with that test id.
    expect(within(screen.getByTestId('issue-summary')).getAllByTestId('issue-status')).toHaveLength(1)
  })

  it("shows the assistant's check with its evidence", async () => {
    renderPage('platform', {
      ...detail,
      issue: { ...detail.issue, chat_verification: 'not_reproduced', chat_verification_note: 'The export of job 9 ran.' },
    })
    const check = await screen.findByTestId('issue-check')
    expect(within(check).getByTestId('issue-verification')).toHaveTextContent('Not reproduced')
    expect(within(check).getByTestId('issue-check-note')).toHaveTextContent('The export of job 9 ran.')
  })

  it('shows the trail the report carried', async () => {
    renderPage('platform', { ...detail, trail: [{ at: '2026-10-15T09:59:00Z', kind: 'click', target: 'Export' }] })
    await userEvent.click(await screen.findByTestId('issue-trail-toggle'))
    expect(screen.getByTestId('issue-trail-events')).toHaveTextContent('Clicked “Export”')
  })

  it('shows the recording with the start of each failed response', async () => {
    renderPage('platform', {
      ...detail,
      recording: {
        started_at: '2026-10-15T09:58:00Z', ended_at: '2026-10-15T09:59:00Z',
        events: [
          { at: '2026-10-15T09:58:10Z', kind: 'input', target: 'Name', value: 'Q4 report' },
          { at: '2026-10-15T09:58:20Z', kind: 'api', method: 'POST', path: '/api/projects', status: 500, body: '{"error":"boom"}' },
        ],
      },
    })
    await userEvent.click(await screen.findByTestId('issue-recording-toggle'))
    const events = screen.getByTestId('issue-recording-events')
    expect(events).toHaveTextContent('Typed “Q4 report” in Name')
    expect(events).toHaveTextContent('POST /api/projects failed (500){"error":"boom"}')
  })

  it('shows no check on a report the assistant did not write', async () => {
    renderPage('platform', detail)
    await screen.findByTestId('issue-summary')
    expect(screen.queryByTestId('issue-check')).toBeNull()
  })

  it('saves a picked priority at once and says so', async () => {
    renderPage('platform', { ...detail, issue: { ...detail.issue, priority: null } })
    vi.mocked(updateIssue).mockResolvedValue(detail)
    await userEvent.click(await screen.findByTestId('issue-priority-select'))
    await userEvent.click(screen.getByTestId('issue-priority-select-option-high'))
    await waitFor(() => expect(updateIssue).toHaveBeenCalledWith(7, { priority: 'high' }))
    // The same element says Saving... first: wait for the end state.
    await waitFor(() => expect(screen.getByTestId('issue-fields-save-status')).toHaveTextContent('Saved ✓'))
  })

  it('changes the kind, and leaves the severity to the server', async () => {
    renderPage('platform', detail)
    vi.mocked(updateIssue).mockResolvedValue(detail)
    await userEvent.click(await screen.findByTestId('issue-kind-select'))
    await userEvent.click(screen.getByTestId('issue-kind-select-option-enhancement'))
    await waitFor(() => expect(updateIssue).toHaveBeenCalledWith(7, { kind: 'enhancement' }))
  })

  it('sends an internal note through the note tab, not as a reply', async () => {
    renderPage('platform', detail)
    vi.mocked(addIssueNote).mockResolvedValue(detail)
    await userEvent.click(await screen.findByTestId('issue-composer-note'))
    await userEvent.type(screen.getByTestId('issue-comment-input'), 'Only for us')
    await userEvent.click(screen.getByTestId('issue-comment-send'))
    await waitFor(() => expect(addIssueNote).toHaveBeenCalledWith(7, 'Only for us', []))
    expect(commentOnIssue).not.toHaveBeenCalled()
  })
})

// Deleting from the issue's own page: offered when the server allows it, and
// back to the list afterwards.
describe('IssueDetailPage delete', () => {
  it('hides delete when the server does not allow it', async () => {
    renderPage('mine', { issue: issue({ can_delete: false }), comments: [] })
    await screen.findAllByText('Export loses tables')
    expect(screen.queryByTestId('issue-delete')).not.toBeInTheDocument()
  })

  it('deletes after a confirmation and returns to the list', async () => {
    renderPage('mine', { issue: issue({ can_delete: true }), comments: [] })
    fireEvent.click(await screen.findByTestId('issue-delete'))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleteIssue).toHaveBeenCalledWith('mine', 7))
    expect(await screen.findByText('issue list')).toBeInTheDocument()
  })
})
