// The comment draft must belong to the segment it was typed against.
//
// Anti-tautology note: this renders the REAL CommentPanel through
// EditorSidebar rather than mounting CommentPanel with a key of its own —
// mounting it directly with `key={translationId}` would be asserting the fix
// against itself. The load-bearing assertion is that after switching
// translations, submitting posts nothing carried over from the previous one.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import EditorSidebar from './EditorSidebar'

const mockGetTMMatches = vi.fn()
const mockGetMTPreview = vi.fn()
const mockListComments = vi.fn()
const mockCreateComment = vi.fn()
const mockDeleteComment = vi.fn()

vi.mock('../api', () => ({
  getTMMatches: (...a: unknown[]) => mockGetTMMatches(...a),
  getMTPreview: (...a: unknown[]) => mockGetMTPreview(...a),
  listComments: (...a: unknown[]) => mockListComments(...a),
  createComment: (...a: unknown[]) => mockCreateComment(...a),
  deleteComment: (...a: unknown[]) => mockDeleteComment(...a),
}))

vi.mock('./SegmentRow.shared', () => ({
  SegmentText: ({ text }: { text: string }) => <span>{text}</span>,
}))

function renderSidebar(translationId: number) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={qc}>
      <EditorSidebar
        jobId={1}
        sourceText="Hello world"
        editText=""
        terms={[]}
        onInsertMatch={vi.fn()}
        onTermClick={vi.fn()}
        onClose={vi.fn()}
        translationId={translationId}
      />
    </QueryClientProvider>,
  )
  const rerenderWith = (nextId: number) =>
    view.rerender(
      <QueryClientProvider client={qc}>
        <EditorSidebar
          jobId={1}
          sourceText="Hello world"
          editText=""
          terms={[]}
          onInsertMatch={vi.fn()}
          onTermClick={vi.fn()}
          onClose={vi.fn()}
          translationId={nextId}
        />
      </QueryClientProvider>,
    )
  return { rerenderWith }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetTMMatches.mockResolvedValue([])
  mockGetMTPreview.mockResolvedValue(null)
  mockListComments.mockResolvedValue([])
  mockCreateComment.mockResolvedValue({ id: 1 })
})

describe('comment draft is scoped to its segment', () => {
  it('clears an unsent draft when the active segment changes', async () => {
    const { rerenderWith } = renderSidebar(10)

    const input = await screen.findByTestId('comment-input')
    await userEvent.type(input, 'meant for segment A')
    expect(input).toHaveValue('meant for segment A')

    rerenderWith(20)

    await waitFor(() => {
      expect(screen.getByTestId('comment-input')).toHaveValue('')
    })
  })

  it('never posts a segment-A draft onto segment B', async () => {
    const { rerenderWith } = renderSidebar(10)

    await userEvent.type(await screen.findByTestId('comment-input'), 'meant for segment A')
    rerenderWith(20)

    // The Add button stays disabled because the draft went with segment A.
    await waitFor(() => expect(screen.getByTestId('comment-add')).toBeDisabled())

    // Typing fresh text on B posts only B's text, against B's translation id.
    await userEvent.type(screen.getByTestId('comment-input'), 'meant for segment B')
    await userEvent.click(screen.getByTestId('comment-add'))

    await waitFor(() => expect(mockCreateComment).toHaveBeenCalledTimes(1))
    expect(mockCreateComment).toHaveBeenCalledWith(20, 'meant for segment B')
  })
})
