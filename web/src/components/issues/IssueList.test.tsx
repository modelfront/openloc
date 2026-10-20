// The issue list's delete action: only on the rows the server allows, behind a
// confirmation, and never opening the issue on the way.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, listIssues: vi.fn(), deleteIssue: vi.fn() }
})

import { listIssues, deleteIssue, type Issue } from '@/api'
import { IssueList } from './IssueList'

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
          <Route path="/issues" element={<IssueList scope="mine" showReporter onToggleSort={() => {}} emptyMessage="None" />} />
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
          <IssueList scope="platform" showReporter onToggleSort={() => {}} emptyMessage="None" />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const checked = await screen.findByTestId('issue-row-3')
    const chips = Array.from(checked.querySelectorAll('[data-testid="issue-verification"], [data-testid="issue-severity"]'))
    expect(chips.map(c => c.textContent)).toEqual(['Verified', 'Major'])
    expect(screen.getByTestId('issue-row-4').querySelector('[data-testid="issue-verification"]')).toBeNull()
  })
})
