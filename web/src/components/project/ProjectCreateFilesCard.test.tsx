// The wizard's Files card pages through the draft's files: the server sends
// 100 at a time, and scrolling to the end loads the next page.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockListFiles = vi.fn()
vi.mock('../../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../api')>(),
  listFiles: (...a: unknown[]) => mockListFiles(...a),
}))

// jsdom has no layout, so the virtualizer would mount no rows: render every
// row, and expose the load-more callback the real table calls near the end.
const loadMore = vi.hoisted(() => ({ current: undefined as undefined | (() => void) }))
vi.mock('@/components/ui/virtual-data-table', () => ({
  VirtualDataTable: ({ data, columns, onLoadMore }: {
    data: unknown[]
    columns: Array<{ cell?: (ctx: { row: { original: unknown } }) => React.ReactNode }>
    onLoadMore?: () => void
  }) => {
    loadMore.current = onLoadMore
    return (
      <table><tbody>
        {data.map((row, i) => <tr key={i}>{columns.map((c, j) => <td key={j}>{c.cell?.({ row: { original: row } })}</td>)}</tr>)}
      </tbody></table>
    )
  },
}))

import { ProjectCreateFilesCard } from './ProjectCreateFilesCard'

const file = (id: number) => ({ id, project_id: 5, name: `file-${id}.txt`, original_name: null, format: 'txt', segment_count: 3 })
const page = (from: number, count: number, more: boolean) => ({
  data: Array.from({ length: count }, (_, i) => file(from + i)),
  cursor: more ? from + count - 1 : 0,
  has_more: more,
  total: 150,
})

afterEach(() => {
  cleanup()
  mockListFiles.mockReset()
})

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProjectCreateFilesCard draftId={5} ensureDraft={async () => 5} analyze={false} onAnalyzeChange={() => {}} />
    </QueryClientProvider>,
  )
}

describe('ProjectCreateFilesCard — many files', () => {
  it('shows the total and loads the files past the first page', async () => {
    mockListFiles.mockImplementation((_id: number, opts?: { cursor?: number }) =>
      Promise.resolve(opts?.cursor ? page(101, 50, false) : page(1, 100, true)))
    renderCard()

    await screen.findByTestId('create-file-100')
    expect(screen.getByText('Files (150)')).toBeInTheDocument()
    expect(screen.queryByTestId('create-file-101')).toBeNull()

    act(() => loadMore.current?.())
    await screen.findByTestId('create-file-150')
    expect(mockListFiles).toHaveBeenLastCalledWith(5, expect.objectContaining({ cursor: 100 }))
    await waitFor(() => expect(screen.getAllByTestId(/^create-file-\d+$/)).toHaveLength(150))
  })
})
