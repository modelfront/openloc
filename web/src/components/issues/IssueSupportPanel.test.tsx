import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getIssue: vi.fn(), listIssues: vi.fn(), setIssueStatus: vi.fn() }
})

import { ApiError, getIssue, listIssues, setIssueStatus, type Issue, type IssueDetail } from '@/api'
import { IssueStatusForm } from './IssueSupportPanel'

const issue = (over: Partial<Issue>): Issue => ({
  id: 7, kind: 'bug', severity: 'major', area: 'editor', title: 'Export loses tables', description: 'It does.',
  status: 'open', context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-15T10:00:00Z',
  updated_at: '2026-10-15T10:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Rita',
  reporter_email: 'rita@example.com', can_delete: false, ...over,
})

const self = issue({ id: 7, title: 'Tables vanish on export' })
const exportBug = issue({ id: 12, title: 'Export drops tables', status: 'in_progress' })
const fontBug = issue({ id: 40, title: 'Wrong font in preview', status: 'resolved' })
const copyBug = issue({ id: 41, title: 'Export drops tables again', status: 'duplicate' })

function renderForm() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <IssueStatusForm issueId={7} current="open" onUpdated={() => {}} />
    </QueryClientProvider>,
  )
}

async function chooseDuplicate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByTestId('issue-status-select'))
  await user.click(screen.getByTestId('issue-status-select-option-duplicate'))
  await user.click(screen.getByTestId('issue-duplicate-of'))
}

beforeEach(() => {
  vi.mocked(listIssues).mockReset()
  vi.mocked(getIssue).mockReset()
  vi.mocked(setIssueStatus).mockReset()
  vi.mocked(listIssues).mockImplementation(async (_scope, _cursor, f) => {
    const q = f?.q?.toLowerCase() ?? ''
    const data = [self, exportBug, fontBug, copyBug].filter(i => i.title.toLowerCase().includes(q))
    return { data, cursor: '', has_more: false }
  })
  vi.mocked(setIssueStatus).mockResolvedValue({ issue: self, comments: [] } as IssueDetail)
})

describe('IssueStatusForm marking a duplicate', () => {
  it('searches the platform list by title, never offers the issue itself or duplicates, and saves the pick', async () => {
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    expect(screen.getByTestId('issue-status-save')).toHaveAttribute('aria-disabled', 'true')

    await user.type(screen.getByPlaceholderText('Search by title or number'), 'export')
    await waitFor(() => expect(listIssues).toHaveBeenLastCalledWith('platform', '', expect.objectContaining({ q: 'export' })))
    const statuses = vi.mocked(listIssues).mock.lastCall![2]!.statuses
    expect(statuses).not.toContain('duplicate')
    const row = await screen.findByTestId('issue-duplicate-of-option-12')
    expect(row).toHaveTextContent('#12 Export drops tables')
    expect(row).toHaveTextContent('In progress')
    expect(screen.queryByTestId('issue-duplicate-of-option-7')).not.toBeInTheDocument()
    expect(screen.queryByTestId('issue-duplicate-of-option-41')).not.toBeInTheDocument()

    await user.click(row)
    expect(screen.getByTestId('issue-duplicate-of')).toHaveTextContent('#12 Export drops tables')

    // A later search that does not match the pick still lists it and keeps it in the field.
    await user.click(screen.getByTestId('issue-duplicate-of'))
    await user.type(screen.getByPlaceholderText('Search by title or number'), 'font')
    await waitFor(() => expect(listIssues).toHaveBeenLastCalledWith('platform', '', expect.objectContaining({ q: 'font' })))
    expect(await screen.findByTestId('issue-duplicate-of-option-40')).toBeInTheDocument()
    expect(screen.getByTestId('issue-duplicate-of-option-12')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.getByTestId('issue-duplicate-of')).toHaveTextContent('#12 Export drops tables')

    await user.click(screen.getByTestId('issue-status-save'))
    await waitFor(() => expect(setIssueStatus).toHaveBeenCalledWith(7, 'duplicate', '', 12))
  })

  it('finds an issue by its number, which the title search does not match', async () => {
    const oldBug = issue({ id: 55, title: 'Slow upload', status: 'wont_fix' })
    vi.mocked(getIssue).mockResolvedValue({ issue: oldBug, comments: [] })
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    expect(screen.queryByTestId('issue-duplicate-of-option-55')).not.toBeInTheDocument()
    await user.type(screen.getByPlaceholderText('Search by title or number'), '#55')

    const row = await screen.findByTestId('issue-duplicate-of-option-55')
    expect(row).toHaveTextContent('#55 Slow upload')
    expect(getIssue).toHaveBeenCalledWith('platform', 55)
  })

  it('says nothing matches when the number is unknown and no title has it', async () => {
    vi.mocked(getIssue).mockRejectedValue(new ApiError(404, 'Not found'))
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    await user.type(screen.getByPlaceholderText('Search by title or number'), '999')

    expect(await screen.findByText('No matching issues')).toBeInTheDocument()
  })

  it('says the search failed rather than that nothing matches', async () => {
    vi.mocked(listIssues).mockRejectedValue(new ApiError(500, 'Internal error'))
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)

    expect(await screen.findByText('The search failed. Type again to retry.')).toBeInTheDocument()
    expect(screen.queryByText('No matching issues')).not.toBeInTheDocument()
  })

  it('says the search failed when the number lookup fails for another reason than an unknown number', async () => {
    vi.mocked(getIssue).mockRejectedValue(new ApiError(500, 'Internal error'))
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    await user.type(screen.getByPlaceholderText('Search by title or number'), '999')

    expect(await screen.findByText('The search failed. Type again to retry.')).toBeInTheDocument()
  })

  it('still says the search failed after an original is picked', async () => {
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    await user.click(await screen.findByText('#12 Export drops tables'))
    vi.mocked(listIssues).mockRejectedValue(new ApiError(500, 'Internal error'))
    await user.click(screen.getByTestId('issue-duplicate-of'))
    await user.type(screen.getByPlaceholderText('Search by title or number'), 'font')

    expect(await screen.findByText('The search failed. Type again to retry.')).toBeInTheDocument()
    expect(screen.queryByText('No other matching issues')).not.toBeInTheDocument()
  })

  it('says no other issue matches after an original is picked', async () => {
    const user = userEvent.setup()
    renderForm()
    await chooseDuplicate(user)
    await user.click(await screen.findByText('#12 Export drops tables'))
    await user.click(screen.getByTestId('issue-duplicate-of'))
    await user.type(screen.getByPlaceholderText('Search by title or number'), 'zzz')

    expect(await screen.findByText('No other matching issues')).toBeInTheDocument()
  })

  it('does not search until the status is Duplicate', () => {
    renderForm()
    expect(listIssues).not.toHaveBeenCalled()
  })
})
