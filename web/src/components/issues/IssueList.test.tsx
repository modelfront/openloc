// The issue list's delete action: only on the rows the server allows, behind a
// confirmation, and never opening the issue on the way.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, listIssues: vi.fn(), deleteIssue: vi.fn() }
})

import { listIssues, deleteIssue, type Issue } from '@/api'
import { IssueList } from './IssueList'
import type { IssueSortLevel } from '@/lib/issue-sort'

const issue = (id: number, title: string, can_delete: boolean): Issue => ({
  id, title, can_delete, kind: 'bug', severity: 'major', area: 'editor', description: 'd', status: 'open',
  context: {}, attachments: [], last_activity_by: 'reporter', created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z', resolved_at: null, confirmed_at: null, reporter_name: 'Ann', reporter_email: 'ann@example.com',
})

function renderList() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/issues']}>
        <Routes>
          <Route path="/issues" element={<IssueList scope="mine" showReporter onSortChange={() => {}} emptyMessage="None" />} />
          <Route path="/issues/:id" element={<p>issue page</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listIssues).mockResolvedValue({ data: [issue(1, 'Mine', true), issue(2, 'A colleague’s', false)], has_more: false, cursor: '' })
  vi.mocked(deleteIssue).mockResolvedValue()
})

describe('IssueList delete', () => {
  it('offers delete only where the server allows it', async () => {
    renderList()
    expect(await screen.findByTestId('issue-delete-1')).toBeInTheDocument()
    expect(screen.queryByTestId('issue-delete-2')).not.toBeInTheDocument()
  })

  it('deletes after a confirmation, without opening the issue', async () => {
    renderList()
    fireEvent.click(await screen.findByTestId('issue-delete-1'))
    expect(screen.queryByText('issue page')).not.toBeInTheDocument()
    expect(deleteIssue).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleteIssue).toHaveBeenCalledWith('mine', 1))
    expect(screen.queryByText('issue page')).not.toBeInTheDocument()
  })
})

describe('IssueList for the support team', () => {
  it("shows the assistant's check in the title cell, before the severity", async () => {
    vi.mocked(listIssues).mockResolvedValue({
      data: [{ ...issue(3, 'Grid is empty', true), chat_verification: 'verified' }, issue(4, 'Typed by hand', true)],
      has_more: false, cursor: '',
    })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <IssueList scope="platform" showReporter onSortChange={() => {}} emptyMessage="None" />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const checked = await screen.findByTestId('issue-row-3')
    const chips = Array.from(checked.querySelectorAll('[data-testid="issue-verification"], [data-testid="issue-severity"]'))
    expect(chips.map(c => c.textContent)).toEqual(['Verified', 'Major'])
    expect(screen.getByTestId('issue-row-4').querySelector('[data-testid="issue-verification"]')).toBeNull()
  })
})

describe('IssueList sorting', () => {
  function renderSorted(scope: 'mine' | 'platform', onSortChange = vi.fn(), sort?: IssueSortLevel[]) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <IssueList scope={scope} filter={{ sort }} showReporter onSortChange={onSortChange} emptyMessage="None" />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    return onSortChange
  }
  const sortable = () => Array.from(document.querySelectorAll('[data-testid^="sort-"]:not([data-testid^="sort-rank"])')).map(e => e.getAttribute('data-testid'))

  it("sorts the members' list by its columns; Issue sorts by type", async () => {
    renderSorted('mine')
    await screen.findByTestId('issue-row-1')
    expect(sortable()).toEqual(['sort-title', 'sort-reporter', 'sort-area', 'sort-status', 'sort-updated'])
  })

  it("adds the support team's priority and assignee", async () => {
    renderSorted('platform')
    await screen.findByTestId('issue-row-1')
    expect(sortable()).toEqual(['sort-title', 'sort-reporter', 'sort-area', 'sort-priority', 'sort-assignee', 'sort-status', 'sort-updated'])
  })

  it('a click sorts by a column; Shift+click adds it to the order', async () => {
    const onSortChange = renderSorted('platform', vi.fn(), [{ key: 'status', desc: false }])
    await screen.findByTestId('issue-row-1')
    fireEvent.click(screen.getByTestId('sort-title'))
    fireEvent.click(screen.getByTestId('sort-priority'), { shiftKey: true })
    expect(onSortChange.mock.calls).toEqual([
      [[{ key: 'kind', desc: false }]],
      [[{ key: 'status', desc: false }, { key: 'priority', desc: false }]],
    ])
  })

  it("points the arrow down for each column's natural order and up for its reverse", async () => {
    renderSorted('platform', vi.fn(), [{ key: 'kind', desc: false }, { key: 'reporter', desc: true }, { key: 'updated', desc: false }])
    await screen.findByTestId('issue-row-1')
    const arrow = (id: string) => {
      const button = screen.getByTestId(id)
      return button.querySelector('.lucide-arrow-down') ? 'down' : button.querySelector('.lucide-arrow-up') ? 'up' : 'none'
    }
    // Bugs first, worst first, is the Issue column's natural order.
    expect(arrow('sort-title')).toBe('down')
    expect(arrow('sort-reporter')).toBe('up')
    expect(arrow('sort-updated')).toBe('up')
  })
})

describe('IssueList next page', () => {
  it('shows a refused next page, and Retry loads the list again from the first page', async () => {
    vi.mocked(listIssues).mockImplementation(async (_scope, cursor) => {
      if (cursor === 'old') throw new Error('Invalid cursor. Load the list again.')
      return cursor === 'new'
        ? { data: [issue(3, 'Older', false)], has_more: false, cursor: '' }
        : { data: [issue(1, 'Mine', true)], has_more: true, cursor: vi.mocked(listIssues).mock.calls.length > 2 ? 'new' : 'old' }
    })
    renderList()
    fireEvent.click(await screen.findByTestId('issues-load-more'))
    expect(await screen.findByText(/Invalid cursor\. Load the list again\./)).toBeInTheDocument()
    expect(screen.getByTestId('issue-row-1')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    fireEvent.click(await screen.findByTestId('issues-load-more'))
    expect(await screen.findByTestId('issue-row-3')).toBeInTheDocument()
    expect(vi.mocked(listIssues).mock.calls.map(c => c[1])).toEqual(['', 'old', '', 'new'])
  })
})

describe('IssueList Issue column', () => {
  it("shows each issue's number before its title", async () => {
    renderList()
    const row = await screen.findByTestId('issue-row-1')
    expect(within(row).getByTestId('issue-number')).toHaveTextContent(/^1$/)
    expect(row).toHaveTextContent(/1\s*Mine/)
    expect(within(screen.getByTestId('issue-row-2')).getByTestId('issue-number')).toHaveTextContent(/^2$/)
  })
})

describe('IssueList numbers', () => {
  it('gives every number the width of the longest, so the titles line up', async () => {
    vi.mocked(listIssues).mockResolvedValue({ data: [issue(7, 'Short', false), issue(1234, 'Long', false)], has_more: false, cursor: '' })
    renderList()
    await screen.findByTestId('issue-row-7')
    for (const id of [7, 1234]) {
      expect(within(screen.getByTestId(`issue-row-${id}`)).getByTestId('issue-number')).toHaveStyle({ minWidth: '4ch' })
    }
  })
})

describe('IssueList row links', () => {
  it('opens an issue here on a click, and in a new tab on Cmd/Ctrl+click or a middle click', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    renderList()
    const row = await screen.findByTestId('issue-row-1')
    fireEvent.click(row, { metaKey: true })
    fireEvent.click(row, { ctrlKey: true })
    fireEvent(row, new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(open.mock.calls).toEqual([
      ['/issues/1', '_blank', 'noopener'], ['/issues/1', '_blank', 'noopener'], ['/issues/1', '_blank', 'noopener'],
    ])
    expect(screen.queryByText('issue page')).not.toBeInTheDocument()

    fireEvent.click(row)
    expect(await screen.findByText('issue page')).toBeInTheDocument()
    expect(open).toHaveBeenCalledTimes(3)
    open.mockRestore()
  })
})

