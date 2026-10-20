import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { TMEntry } from '../api'

vi.mock('@/components/SegmentRow.shared', () => ({
  SegmentText: ({ text }: { text: string }) => <span>{text}</span>,
}))

const mockListTMEntryHistory = vi.fn()
vi.mock('../api', () => ({
  listTMEntryHistory: (...a: unknown[]) => mockListTMEntryHistory(...a),
}))

import TMEntryHistoryDialog from './TMEntryHistoryDialog'

const SAMPLE_ENTRY: TMEntry = {
  id: 42,
  tm_id: 1,
  source_language: 'en',
  target_language: 'es',
  source_text: 'Hello world',
  target_text: 'Hola mundo',
  source_rich_text: null,
  target_rich_text: null,
  created_at: '2026-04-01T10:00:00Z',
  updated_at: '2026-04-20T10:00:00Z',
  created_by: 'tester',
  updated_by: 'editor@test.com',
}

function renderDialog(props: Partial<React.ComponentProps<typeof TMEntryHistoryDialog>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TMEntryHistoryDialog
        tmId={1}
        entry={props.entry !== undefined ? props.entry : SAMPLE_ENTRY}
        onClose={props.onClose ?? vi.fn()}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => cleanup())

describe('TMEntryHistoryDialog', () => {
  // A work modal the user reads the entry list behind, so it opts into dragging.
  // The drag itself is covered in ui/dialog.test.tsx.
  it('can be moved by its header', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    renderDialog()
    const dialog = await screen.findByTestId('tm-history-dialog')
    expect(dialog.querySelector('[data-slot="dialog-header"]')).toHaveAttribute('data-draggable', 'true')
  })

  it('does not render the dialog when entry is null', () => {
    mockListTMEntryHistory.mockResolvedValue([])
    renderDialog({ entry: null })
    expect(screen.queryByTestId('tm-history-dialog')).toBeNull()
    expect(mockListTMEntryHistory).not.toHaveBeenCalled()
  })

  it('renders the dialog title + description and shows current values when open', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    renderDialog()
    expect(await screen.findByTestId('tm-history-dialog')).toBeInTheDocument()
    expect(screen.getByText('Edit history')).toBeInTheDocument()
    expect(screen.getByText(/Past edits to this TM entry, newest first/i)).toBeInTheDocument()
    // Current section attributions
    expect(screen.getByText(/last edited by editor@test.com/i)).toBeInTheDocument()
    // Source + target text rendered
    expect(screen.getByText('Hello world')).toBeInTheDocument()
    expect(screen.getByText('Hola mundo')).toBeInTheDocument()
  })

  it('shows the empty state when history is empty', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    renderDialog()
    const empty = await screen.findByTestId('tm-history-empty')
    expect(empty).toHaveTextContent(/No past edits/i)
    expect(empty).toHaveTextContent(/created by tester/i)
    expect(screen.queryByTestId('tm-history-list')).toBeNull()
  })

  it('renders one row per history record with author + reason + before-values', async () => {
    mockListTMEntryHistory.mockResolvedValue([
      {
        id: 100,
        entry_id: 42,
        source_text: 'Old source',
        target_text: 'Old target',
        source_rich_text: null,
        target_rich_text: null,
        changed_by_user_id: 7,
        changed_by_email: 'alice@test.com',
        change_reason: 'edit',
        created_at: '2026-04-22T15:30:00Z',
      },
      {
        id: 99,
        entry_id: 42,
        source_text: 'Older source',
        target_text: 'Older target',
        source_rich_text: null,
        target_rich_text: null,
        changed_by_user_id: 8,
        changed_by_email: 'bob@test.com',
        change_reason: 'edit',
        created_at: '2026-04-21T10:00:00Z',
      },
    ])

    renderDialog()
    await waitFor(() => {
      expect(screen.getByTestId('tm-history-list')).toBeInTheDocument()
    })

    expect(screen.getByTestId('tm-history-row-100')).toHaveTextContent('alice@test.com')
    expect(screen.getByTestId('tm-history-row-100')).toHaveTextContent('Old source')
    expect(screen.getByTestId('tm-history-row-100')).toHaveTextContent('Old target')
    expect(screen.getByTestId('tm-history-row-99')).toHaveTextContent('bob@test.com')
    expect(screen.getByTestId('tm-history-row-99')).toHaveTextContent('Older source')
    // The reason arrives as the wire enum `edit`; the row must read the label.
    expect(screen.getByTestId('tm-history-row-100')).toHaveTextContent('Edited')

    // Empty state must NOT show when there are rows
    expect(screen.queryByTestId('tm-history-empty')).toBeNull()
  })

  // `change_reason` is a free-form string on the wire, so the label lookup has
  // to tolerate anything a client stored — including keys that exist on
  // Object.prototype. A bare `map[key]` returns Object.prototype for
  // "__proto__", which is not nullish, so `??` would not catch it and React
  // would throw "Objects are not valid as a React child" — crashing the dialog
  // for everyone in the tenant until the row is deleted.
  it.each(['__proto__', 'constructor', 'toString', 'valueOf', 'mystery_reason'])(
    'renders %s as a plain label instead of crashing',
    async reason => {
      mockListTMEntryHistory.mockResolvedValue([
        {
          id: 100,
          entry_id: 42,
          source_text: 'Old source',
          target_text: 'Old target',
          source_rich_text: null,
          target_rich_text: null,
          changed_by_user_id: 7,
          changed_by_email: 'alice@test.com',
          change_reason: reason,
          created_at: '2026-04-22T15:30:00Z',
        },
      ])
      renderDialog()
      const row = await screen.findByTestId('tm-history-row-100')
      expect(row).toHaveTextContent(reason)
    },
  )

  it('renders "unknown user" when changed_by_email is null', async () => {
    mockListTMEntryHistory.mockResolvedValue([
      {
        id: 100,
        entry_id: 42,
        source_text: 'Old source',
        target_text: 'Old target',
        source_rich_text: null,
        target_rich_text: null,
        changed_by_user_id: null,
        changed_by_email: null,
        change_reason: '',
        created_at: '2026-04-22T15:30:00Z',
      },
    ])
    renderDialog()
    const row = await screen.findByTestId('tm-history-row-100')
    expect(row).toHaveTextContent(/unknown user/i)
  })

  it('shows an error message when the history fetch fails', async () => {
    mockListTMEntryHistory.mockRejectedValue(new Error('network down'))
    renderDialog()
    await waitFor(() => {
      expect(screen.getByText(/Failed to load history/i)).toBeInTheDocument()
    })
    expect(screen.getByText(/network down/i)).toBeInTheDocument()
  })

  it('calls onClose when the dialog is dismissed via the X button', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    const onClose = vi.fn()
    const user = userEvent.setup()
    renderDialog({ onClose })

    await screen.findByTestId('tm-history-dialog')
    // Radix's close button has an accessible name "Close"
    const closeBtn = screen.getByRole('button', { name: /close/i })
    await user.click(closeBtn)
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  })

  it('passes the entry id to the API client (binding integrity)', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    renderDialog({ entry: { ...SAMPLE_ENTRY, id: 999 } })
    await waitFor(() => {
      expect(mockListTMEntryHistory).toHaveBeenCalledWith(1, 999)
    })
  })
})
