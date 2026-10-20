// Reported issues: anyone can report from the list, operators included — they
// report into their own org, as from the account menu. An emptied status
// filter stays in the URL as "any status".
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return {
    ...actual, listIssues: vi.fn(), listIssueOrgs: vi.fn(), listIssueAssignees: vi.fn(), listIssueTags: vi.fn(),
  }
})

import { listIssues, listIssueAssignees, listIssueOrgs, listIssueTags, type IssueScope } from '@/api'
import IssuesPage from './IssuesPage'

function Location() {
  const { search } = useLocation()
  return <span data-testid="location">{search}</span>
}

function renderPage(scope: IssueScope, url = '/issues') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <IssuesPage scope={scope} role="owner" />
        <Location />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const lastFilter = () => vi.mocked(listIssues).mock.lastCall?.[2]

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listIssues).mockResolvedValue({ data: [], cursor: '', has_more: false })
  vi.mocked(listIssueOrgs).mockResolvedValue([])
  vi.mocked(listIssueAssignees).mockResolvedValue([])
  vi.mocked(listIssueTags).mockResolvedValue([])
})

describe('IssuesPage', () => {
  it.each(['mine', 'platform'] as const)('offers Report an issue on the %s list', async scope => {
    renderPage(scope)
    fireEvent.click(await screen.findByTestId('issues-report'))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

describe('IssuesPage status filter', () => {
  it('lists the issues that need work until the filter is cleared, then every status', async () => {
    const user = userEvent.setup()
    renderPage('mine')
    await waitFor(() => expect(lastFilter()?.statuses).toEqual(['open', 'in_progress', 'needs_info']))

    for (const label of ['Open', 'In progress', 'Needs info']) {
      await user.click(screen.getAllByLabelText(`Remove ${label}`)[0])
    }

    expect(screen.getByTestId('location')).toHaveTextContent(/^\?status=$/)
    await waitFor(() => expect(lastFilter()?.statuses).toEqual([]))
  })

  it('drops another emptied filter from the URL', async () => {
    const user = userEvent.setup()
    renderPage('mine', '/issues?kind=bug')
    await user.click(screen.getAllByLabelText('Remove Bug')[0])
    expect(screen.getByTestId('location')).toHaveTextContent(/^$/)
  })
})
