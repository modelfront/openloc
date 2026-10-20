// After the support team deletes an issue, the tag filter reads its list again:
// a tag only that issue carried must not stay offered.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, deleteIssue: vi.fn(), listIssueTags: vi.fn() }
})

import { deleteIssue, listIssueTags } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useDeleteIssue } from './useDeleteIssue'

function Harness() {
  const { data: tags = [] } = useQuery({ queryKey: queryKeys.platform.issueTags, queryFn: listIssueTags })
  const { ask, dialog } = useDeleteIssue('platform')
  return (
    <>
      <ul>{tags.map(t => <li key={t}>{t}</li>)}</ul>
      <button type="button" onClick={() => ask({ id: 3, title: 'Tagged' })}>ask</button>
      {dialog}
    </>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(deleteIssue).mockResolvedValue()
  // Only the deleted issue carries "mvp".
  vi.mocked(listIssueTags).mockResolvedValueOnce(['mvp', 'export']).mockResolvedValue(['export'])
})

describe('useDeleteIssue for the support team', () => {
  it('stops offering a tag only the deleted issue carried', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={qc}><Harness /></QueryClientProvider>)
    expect(await screen.findByText('mvp')).toBeInTheDocument()

    fireEvent.click(screen.getByText('ask'))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleteIssue).toHaveBeenCalledWith('platform', 3))
    await waitFor(() => expect(screen.queryByText('mvp')).not.toBeInTheDocument())
    expect(screen.getByText('export')).toBeInTheDocument()
  })
})
