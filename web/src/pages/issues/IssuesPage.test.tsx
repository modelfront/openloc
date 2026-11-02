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
    ...actual, listIssues: vi.fn(), countIssues: vi.fn(), listIssueOrgs: vi.fn(), listIssueReporters: vi.fn(), listIssueAssignees: vi.fn(), listIssueTags: vi.fn(),
  }
})

import { countIssues, listIssues, listIssueAssignees, listIssueOrgs, listIssueReporters, listIssueTags, type Issue, type IssueScope } from '@/api'
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
  localStorage.clear()
  vi.mocked(countIssues).mockResolvedValue(0)
  vi.mocked(listIssues).mockResolvedValue({ data: [], cursor: '', has_more: false })
  vi.mocked(listIssueOrgs).mockResolvedValue([])
  vi.mocked(listIssueReporters).mockResolvedValue([])
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

describe('IssuesPage sorting', () => {
  const row = {
    id: 1, title: 'One', can_delete: false, kind: 'bug', severity: 'major', area: 'editor', description: 'd', status: 'open',
    context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Ann', reporter_email: 'ann@example.com',
  } as unknown as Issue

  it('reads the order from the URL and sends it', async () => {
    renderPage('platform', '/platform/issues?sort=-priority,status,title')
    // An unknown key in the link is dropped, not sent.
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'priority', desc: true }, { key: 'status', desc: false }]))
  })

  it('keeps the order the headers set in the URL, and none for the default', async () => {
    vi.mocked(listIssues).mockResolvedValue({ data: [row], cursor: '', has_more: false })
    renderPage('mine')
    fireEvent.click(await screen.findByTestId('sort-area'))
    expect(screen.getByTestId('location')).toHaveTextContent(/^\?sort=area$/)
    fireEvent.click(await screen.findByTestId('sort-updated'), { shiftKey: true })
    expect(screen.getByTestId('location')).toHaveTextContent(/^\?sort=area%2C-updated$/)
    // A click on the second level sorts by it alone, newest first; then
    // oldest first; then back to the default, which needs no parameter.
    for (const want of [/^\?sort=-updated$/, /^\?sort=updated$/, /^$/]) {
      fireEvent.click(await screen.findByTestId('sort-updated'))
      expect(screen.getByTestId('location')).toHaveTextContent(want)
    }
  })

  it('keeps the rows and headers on screen while a new order loads', async () => {
    vi.mocked(listIssues).mockResolvedValueOnce({ data: [row], cursor: '', has_more: false })
    vi.mocked(listIssues).mockReturnValueOnce(new Promise(() => {}))
    renderPage('mine')
    fireEvent.click(await screen.findByTestId('sort-area'))
    expect(screen.getByTestId('location')).toHaveTextContent(/^\?sort=area$/)
    expect(screen.getByTestId('issue-row-1')).toBeInTheDocument()
    expect(screen.getByTestId('sort-updated')).toBeInTheDocument()
  })
})

describe('IssuesPage count', () => {
  it("shows how many issues match the page's filters, and a new sort does not count again", async () => {
    vi.mocked(countIssues).mockResolvedValue(1234)
    vi.mocked(listIssues).mockResolvedValue({ data: [{
      id: 1, title: 'One', can_delete: false, kind: 'bug', severity: 'major', area: 'editor', description: 'd', status: 'open',
      context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-01T00:00:00Z',
      updated_at: '2026-10-01T00:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Ann', reporter_email: 'ann@example.com',
    }], cursor: '', has_more: false })
    renderPage('platform', '/platform/issues?kind=bug&org=3&q=export&sort=-priority')
    expect(await screen.findByTestId('issues-count')).toHaveTextContent(/^1,234 issues$/)
    expect(countIssues).toHaveBeenCalledTimes(1)
    const [scope, filter] = vi.mocked(countIssues).mock.calls[0]
    expect(scope).toBe('platform')
    expect(filter).toMatchObject({ kinds: ['bug'], orgs: [3], q: 'export' })
    expect(filter).not.toHaveProperty('sort')

    fireEvent.click(await screen.findByTestId('sort-area'))
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'area', desc: false }]))
    expect(countIssues).toHaveBeenCalledTimes(1)
  })

  it('says "1 issue" for one', async () => {
    vi.mocked(countIssues).mockResolvedValue(1)
    renderPage('mine')
    expect(await screen.findByTestId('issues-count')).toHaveTextContent(/^1 issue$/)
  })
})

describe('IssuesPage reporter filter', () => {
  it('offers the orgs, a line, then the people; a pick of each filters by both, and Clear filters clears both', async () => {
    vi.mocked(listIssueOrgs).mockResolvedValue([{ id: 3, name: 'Acme' }, { id: 4, name: 'Globex' }])
    vi.mocked(listIssueReporters).mockResolvedValue([
      { id: 7, name: 'Rita', email: 'rita@example.com', orgs: 'Acme' },
      { id: 8, name: '', email: 'carl@example.com', orgs: 'Globex' },
    ])
    const user = userEvent.setup()
    renderPage('platform')
    const field = await screen.findByTestId('issues-filter-reporter')
    expect(field).toHaveTextContent('Any reporter')
    await user.click(field)
    await screen.findByTestId('issues-filter-reporter-option-user:8')
    const list = document.querySelector('[data-slot="command-list"]')!
    const rows = Array.from(list.querySelectorAll('[data-slot="command-item"], [data-slot="command-separator"]'))
      .map(e => (e.getAttribute('data-slot') === 'command-separator' ? '---' : e.textContent))
    expect(rows).toEqual(['Acme', 'Globex', '---', 'RitaAcme', 'carl@example.comGlobex'])

    await user.click(screen.getByTestId('issues-filter-reporter-option-org:4'))
    await user.click(screen.getByTestId('issues-filter-reporter-option-user:7'))
    expect(screen.getByTestId('location')).toHaveTextContent(/^\?org=4&reporter=7$/)
    await waitFor(() => expect(lastFilter()).toMatchObject({ orgs: [4], reporters: [7] }))
    expect(screen.getByTestId('issues-clear-filters')).toHaveTextContent('Clear filters (1)')

    await user.click(screen.getByTestId('issues-clear-filters'))
    expect(screen.getByTestId('location')).toHaveTextContent(/^$/)
  })
})

describe('IssuesPage remembered order', () => {
  const one = {
    id: 1, title: 'One', can_delete: false, kind: 'bug', severity: 'major', area: 'editor', description: 'd', status: 'open',
    context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Ann', reporter_email: 'ann@example.com',
  } as unknown as Issue

  it('opens with the order last chosen on the list, unless the link names one', async () => {
    vi.mocked(listIssues).mockResolvedValue({ data: [one], cursor: '', has_more: false })
    const first = renderPage('mine')
    fireEvent.click(await screen.findByTestId('sort-area'))
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'area', desc: false }]))
    first.unmount()

    const again = renderPage('mine')
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'area', desc: false }]))
    expect(screen.getByTestId('location')).toHaveTextContent(/^$/)
    again.unmount()

    const linked = renderPage('mine', '/issues?sort=-updated')
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'updated', desc: true }]))
    linked.unmount()

    // Each list remembers its own order.
    renderPage('platform')
    await waitFor(() => expect(lastFilter()?.sort).toEqual([]))
  })

  it('remembers a click back to the default order', async () => {
    localStorage.setItem('issues.sort.mine', 'area')
    vi.mocked(listIssues).mockResolvedValue({ data: [one], cursor: '', has_more: false })
    const first = renderPage('mine')
    await waitFor(() => expect(lastFilter()?.sort).toEqual([{ key: 'area', desc: false }]))
    fireEvent.click(await screen.findByTestId('sort-area'))
    fireEvent.click(await screen.findByTestId('sort-area'))
    await waitFor(() => expect(lastFilter()?.sort).toEqual([]))
    first.unmount()
    renderPage('mine')
    await waitFor(() => expect(lastFilter()?.sort).toEqual([]))
  })
})

