import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

// vi.mock factories are hoisted; vi.hoisted lets us define shared state that's
// available at hoist time. We use it to script the mocked RichTextCellEditor's
// imperative handle from each test.
const mockEditor = vi.hoisted(() => ({
  text: 'initial',
  richText: null as string | null,
  // The component's onBlur prop is captured here so tests can fire it directly.
  fireBlur: () => {},
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))

// Stub heavy dependencies that aren't relevant to the cell-edit logic.
// The stub exposes the search highlight it was given, so tests can check
// which fields are tinted.
vi.mock('@/components/SegmentRow.shared', async (importOriginal) => ({
  SEGMENT_PREVIEW_CLAMP: (await importOriginal<typeof import('@/components/SegmentRow.shared')>()).SEGMENT_PREVIEW_CLAMP,
  SegmentText: ({ text, search }: { text: string; search?: RegExp | null }) =>
    <span data-search={search ? search.source : undefined}>{text}</span>,
}))

// VirtualDataTable uses @tanstack/react-virtual which measures scroll area
// dimensions to decide what to render — in jsdom every measurement is zero
// so no rows mount. Replace with a plain table that renders every row;
// virtualization is exercised via real-browser smoke, not jsdom unit tests.
vi.mock('@/components/ui/virtual-data-table', async () => {
  type Cell = (ctx: { row: { original: unknown }; getValue: () => unknown }) => import('react').ReactNode
  type Props = {
    columns: Array<{ id?: string; accessorKey?: string; header?: string; cell?: Cell }>
    data: unknown[]
    rowClassName?: string | ((row: unknown) => string)
  }
  function VirtualDataTable({ columns, data, rowClassName }: Props) {
    return (
      <table>
        <tbody>
          {data.map((row, i) => {
            const cls = typeof rowClassName === 'function' ? rowClassName(row) : rowClassName
            return (
              <tr key={i} className={cls}>
                {columns.map((col, j) => {
                  const ctx = {
                    row: { original: row },
                    getValue: () => col.accessorKey ? (row as Record<string, unknown>)[col.accessorKey] : undefined,
                  }
                  return <td key={j}>{col.cell ? col.cell(ctx) : null}</td>
                })}
              </tr>
            )
          })}
        </tbody>
      </table>
    )
  }
  return { VirtualDataTable }
})

// Mock RichTextCellEditor so the test exercises the cell-edit flow without
// pulling in TipTap (which is exercised in RichTextCellEditor.test.tsx).
vi.mock('@/components/RichTextCellEditor', async () => {
  const React = await vi.importActual<typeof import('react')>('react')
  const { forwardRef, useEffect, useImperativeHandle } = React
  const Mock = forwardRef<unknown, { initialText: string; onBlur?: () => void; dataTestId?: string }>(
    function MockEditor(props, ref) {
      useEffect(() => { mockEditor.fireBlur = props.onBlur ?? (() => {}) })
      useImperativeHandle(ref, () => ({
        getText: () => mockEditor.text,
        getRichText: () => mockEditor.richText,
        getHTML: () => '',
        focus: () => {},
        destroy: () => {},
        setText: () => {},
        setHTML: () => {},
        getSelection: () => ({ from: 1, to: 1 }),
        setSelection: () => {},
        insertText: () => {},
        replaceRange: () => {},
        getEditor: () => null,
      }), [])
      return (
        <div data-testid={props.dataTestId ?? 'rich-text-editor-mock'}>
          {props.initialText}
        </div>
      )
    },
  )
  return { default: Mock, type: { RichTextCellEditorHandle: null } }
})

import { ApiError } from '../api/request'

// Build the api mock AFTER the imports above so the mocks are wired in time.
const mockListTMCollections = vi.fn()
const mockListTMLanguagePairs = vi.fn()
const mockListTMEntries = vi.fn()
const mockListTMImports = vi.fn()
const mockUpdateTMEntry = vi.fn()
const mockDeleteTMEntry = vi.fn()
const mockListTMEntryHistory = vi.fn()

vi.mock('../api', async () => {
  return {
    ApiError: (await vi.importActual<typeof import('../api/request')>('../api/request')).ApiError,
    listTMCollections: (...a: unknown[]) => mockListTMCollections(...a),
    listTMLanguagePairs: (...a: unknown[]) => mockListTMLanguagePairs(...a),
    listTMEntries: (...a: unknown[]) => mockListTMEntries(...a),
    listTMImports: (...a: unknown[]) => mockListTMImports(...a),
    updateTMEntry: (...a: unknown[]) => mockUpdateTMEntry(...a),
    deleteTMEntry: (...a: unknown[]) => mockDeleteTMEntry(...a),
    listTMEntryHistory: (...a: unknown[]) => mockListTMEntryHistory(...a),
    importTMX: () => ({ promise: Promise.resolve({}), abort: () => {} }),
    exportTMX: vi.fn(),
  }
})

import TMBrowser from './TMBrowser'
import { toast } from 'sonner'
import { SEGMENT_PREVIEW_CLAMP } from '@/components/SegmentRow.shared'

const SAMPLE_ENTRY = {
  id: 42,
  tm_id: 1,
  source_language: 'en',
  target_language: 'es',
  source_text: 'Hello world',
  target_text: 'Hola mundo',
  source_rich_text: null,
  target_rich_text: null,
  created_at: '2026-04-20T10:00:00Z',
  updated_at: '2026-04-20T10:00:00Z',
  created_by: 'tester',
}

// `queryRetry` replaces the no-retry default, for tests about the page's own
// retry rule (production retries once).
function renderTMBrowser(queryRetry: { retry: number; retryDelay: number } | { retry: false } = { retry: false }) {
  const qc = new QueryClient({ defaultOptions: { queries: queryRetry, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/memories/1']}>
        <Routes>
          <Route path="/memories/:tmId" element={<TMBrowser />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockEditor.text = 'initial'
  mockEditor.richText = null
  mockListTMCollections.mockResolvedValue([{ id: 1, name: 'Test TM', created_at: '', updated_at: '' }])
  mockListTMLanguagePairs.mockResolvedValue([{ source_language: 'en', target_language: 'es' }])
  mockListTMEntries.mockResolvedValue({
    data: [SAMPLE_ENTRY],
    cursor: 0,
    has_more: false,
    total: 1,
  })
  mockListTMImports.mockResolvedValue([])
  mockListTMEntryHistory.mockResolvedValue([])
})

afterEach(() => cleanup())

describe('TMBrowser inline cell editing', () => {
  it('shows read-only cells initially with cursor-text affordance', async () => {
    renderTMBrowser()
    await waitFor(() => {
      expect(screen.getByTestId('tm-cell-42-source')).toBeInTheDocument()
      expect(screen.getByTestId('tm-cell-42-target')).toBeInTheDocument()
    })
    expect(screen.getByTestId('tm-cell-42-source')).toHaveClass('cursor-text')
  })

  it('clicking a target cell mounts the editor with current text', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))

    const editor = await screen.findByTestId('tm-cell-edit-42-target')
    expect(editor).toBeInTheDocument()
    expect(editor).toHaveTextContent('Hola mundo')
    // Read-only target cell is no longer rendered while editing.
    expect(screen.queryByTestId('tm-cell-42-target')).toBeNull()
  })

  it('only one cell can be edited at a time', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')
    expect(screen.queryByTestId('tm-cell-edit-42-source')).toBeNull()

    await user.click(screen.getByTestId('tm-cell-42-source'))

    expect(await screen.findByTestId('tm-cell-edit-42-source')).toBeInTheDocument()
    expect(screen.queryByTestId('tm-cell-edit-42-target')).toBeNull()
    expect(screen.getByTestId('tm-cell-42-target')).toBeInTheDocument()
  })

  it('wraps cells up to the CAT preview cap, and uncaps the row being edited', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    const source = await screen.findByTestId('tm-cell-42-source')
    expect(source).toHaveClass(SEGMENT_PREVIEW_CLAMP, 'break-words')
    expect(source).not.toHaveClass('truncate')
    expect(screen.getByTestId('tm-cell-42-target')).toHaveClass(SEGMENT_PREVIEW_CLAMP)

    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')
    const activeSource = screen.getByTestId('tm-cell-42-source')
    expect(activeSource).not.toHaveClass(SEGMENT_PREVIEW_CLAMP)
    expect(activeSource).toHaveClass('break-words')
  })

  it('fires updateTMEntry with the correct payload on blur (target field edit)', async () => {
    mockUpdateTMEntry.mockResolvedValue({ ...SAMPLE_ENTRY, target_text: 'Hola mundo cambiado', updated_at: '2026-04-21T10:00:00Z' })

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    // Simulate a typed change + blur (click outside). Our mocked editor lets
    // us script the new text and trigger the onBlur callback the parent passed.
    mockEditor.text = 'Hola mundo cambiado'
    act(() => mockEditor.fireBlur())

    await waitFor(() => {
      expect(mockUpdateTMEntry).toHaveBeenCalledTimes(1)
    })
    // changeReason defaults to 'edit' inside the API client, so it isn't part
    // of the payload TMBrowser passes — see api/tms.ts updateTMEntry().
    expect(mockUpdateTMEntry).toHaveBeenCalledWith(1, 42, expect.objectContaining({
      sourceText: 'Hello world',
      targetText: 'Hola mundo cambiado',
      sourceRichText: null,
      targetRichText: null,
      expectedUpdatedAt: '2026-04-20T10:00:00Z',
    }))
  })

  it('fires updateTMEntry with the correct payload on blur (source field edit)', async () => {
    mockUpdateTMEntry.mockResolvedValue({ ...SAMPLE_ENTRY, source_text: 'Hello universe', updated_at: '2026-04-21T10:00:00Z' })

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    await user.click(screen.getByTestId('tm-cell-42-source'))
    await screen.findByTestId('tm-cell-edit-42-source')

    mockEditor.text = 'Hello universe'
    act(() => mockEditor.fireBlur())

    await waitFor(() => {
      expect(mockUpdateTMEntry).toHaveBeenCalledTimes(1)
    })
    expect(mockUpdateTMEntry).toHaveBeenCalledWith(1, 42, expect.objectContaining({
      sourceText: 'Hello universe',
      targetText: 'Hola mundo',
      expectedUpdatedAt: '2026-04-20T10:00:00Z',
    }))
  })

  it('skips the PATCH when text is unchanged', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    // No change to mockEditor.text — same as initial 'Hola mundo'
    mockEditor.text = 'Hola mundo'
    act(() => mockEditor.fireBlur())

    await waitFor(() => {
      expect(screen.queryByTestId('tm-cell-edit-42-target')).toBeNull()
    })
    expect(mockUpdateTMEntry).not.toHaveBeenCalled()
  })

  // Helper: build a 409 ApiError with the conflict body shape that
  // `internal/translationmemory/handler.go::writeTMConflictResponse` returns.
  // The banner reads `err.body.conflict` — without it, getTMConflictPayload
  // returns null and the banner won't render.
  function tmConflict409(opts: {
    sourceText?: string
    targetText?: string
    targetRichText?: string | null
    updatedBy?: string
    updatedByUserId?: number | null
    updatedByName?: string
  } = {}) {
    return new ApiError(
      409,
      'TM entry was updated by another user — reload and try again',
      {
        error: { code: 'Conflict', message: 'TM entry was updated by another user — reload and try again' },
        conflict: {
          id: SAMPLE_ENTRY.id,
          source_text: opts.sourceText ?? SAMPLE_ENTRY.source_text,
          target_text: opts.targetText ?? 'their saved target',
          source_rich_text: null,
          target_rich_text: opts.targetRichText ?? null,
          updated_at: '2026-04-29T18:00:00Z',
          updated_by: opts.updatedBy ?? 'another@test.com',
          updated_by_user_id: opts.updatedByUserId ?? 999,
          updated_by_name: opts.updatedByName ?? 'Another User',
        },
      },
    )
  }

  it('shows the conflict banner on a 409 response', async () => {
    mockUpdateTMEntry.mockRejectedValue(tmConflict409({
      targetText: 'their saved target',
      updatedByName: 'Another User',
      updatedByUserId: 999,
    }))

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    mockEditor.text = 'Hola, mundo conflictivo'
    act(() => mockEditor.fireBlur())

    await waitFor(() => expect(mockUpdateTMEntry).toHaveBeenCalled())

    const banner = await screen.findByTestId('conflict-banner')
    // Heading uses the OTHER user's name (the seed user's getMe id won't match
    // 999, so this is the different-user branch).
    expect(screen.getByTestId('conflict-banner-title')).toHaveTextContent(/Another User saved a different version/i)
    // Field label is TARGET (we were editing the target cell).
    expect(banner).toHaveTextContent(/their target/i)
    // Both versions and both action buttons.
    expect(screen.getByTestId('conflict-banner-theirs')).toHaveTextContent('their saved target')
    expect(screen.getByTestId('conflict-banner-mine')).toHaveTextContent('Hola, mundo conflictivo')
    expect(screen.getByTestId('conflict-banner-use-theirs')).toBeInTheDocument()
    expect(screen.getByTestId('conflict-banner-use-mine')).toBeInTheDocument()
  })

  it('"Use theirs" closes banner, exits edit mode, and refetches entries', async () => {
    mockUpdateTMEntry.mockRejectedValue(tmConflict409())

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    mockEditor.text = 'change'
    act(() => mockEditor.fireBlur())
    await screen.findByTestId('conflict-banner')

    // Reset call counter so we can detect the refetch triggered by invalidate.
    mockListTMEntries.mockClear()

    await user.click(screen.getByTestId('conflict-banner-use-theirs'))

    await waitFor(() => {
      expect(screen.queryByTestId('conflict-banner')).toBeNull()
    })
    // Editor should be cleared too — handleUseTheirs nulls editingCell.
    expect(screen.queryByTestId('tm-cell-edit-42-target')).toBeNull()
    // Entries query should have been re-issued by invalidateTMContext.
    await waitFor(() => {
      expect(mockListTMEntries).toHaveBeenCalled()
    })
  })

  it('"Use mine" re-issues the save with expected_updated_at = null and persists the local edit', async () => {
    // First call: 409 (the original conflict). Second call: success (the
    // bypass save). We assert BOTH happened and the second carried no
    // expected_updated_at (last-writer-wins).
    mockUpdateTMEntry
      .mockRejectedValueOnce(tmConflict409({ targetText: 'their saved target' }))
      .mockResolvedValueOnce({
        ...SAMPLE_ENTRY,
        target_text: 'my edit wins',
        updated_at: '2026-04-29T19:00:00Z',
      })

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    mockEditor.text = 'my edit wins'
    act(() => mockEditor.fireBlur())
    await screen.findByTestId('conflict-banner')

    await user.click(screen.getByTestId('conflict-banner-use-mine'))

    // Banner clears + edit mode exits on the success path.
    await waitFor(() => {
      expect(screen.queryByTestId('conflict-banner')).toBeNull()
    })

    // Two calls total: the original 409, then the bypass save.
    await waitFor(() => expect(mockUpdateTMEntry).toHaveBeenCalledTimes(2))

    // Anti-tautology: second call must explicitly carry expectedUpdatedAt: null
    // (NOT undefined / NOT the stale baseline). If "Use mine" forgot to bypass
    // the version check, the call would re-409 and this test would fail at
    // the "banner clears" wait above.
    const secondCall = mockUpdateTMEntry.mock.calls[1]
    const secondPayload = secondCall[2] as { expectedUpdatedAt: string | null; targetText: string; sourceText: string }
    expect(secondPayload.expectedUpdatedAt).toBeNull()
    expect(secondPayload.targetText).toBe('my edit wins')
    // The source side stays at the server's value (we didn't intend to overwrite it).
    expect(secondPayload.sourceText).toBe(SAMPLE_ENTRY.source_text)
  })

  it('non-409 errors do not open the conflict banner', async () => {
    mockUpdateTMEntry.mockRejectedValue(new ApiError(500, 'server boom'))

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    mockEditor.text = 'change'
    act(() => mockEditor.fireBlur())
    await waitFor(() => expect(mockUpdateTMEntry).toHaveBeenCalled())

    // No banner (server error → toast.error path, not the 409 banner).
    expect(screen.queryByTestId('conflict-banner')).toBeNull()
  })

  it('shows the edited pencil indicator on the row after a successful save', async () => {
    mockUpdateTMEntry.mockResolvedValue({
      ...SAMPLE_ENTRY,
      target_text: 'Hola mundo cambiado',
      updated_at: '2026-04-21T10:00:00Z',
    })

    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-target')
    await user.click(screen.getByTestId('tm-cell-42-target'))
    await screen.findByTestId('tm-cell-edit-42-target')

    mockEditor.text = 'Hola mundo cambiado'
    act(() => mockEditor.fireBlur())
    await waitFor(() => expect(mockUpdateTMEntry).toHaveBeenCalled())

    // After save resolves, the cell re-renders with the pencil aria-label.
    await waitFor(() => {
      const cell = screen.getByTestId('tm-cell-42-target')
      expect(cell.querySelector('[aria-label="Edited this session"]')).not.toBeNull()
    })
  })

  it('clicking the history button opens the history dialog scoped to that entry', async () => {
    mockListTMEntryHistory.mockResolvedValue([])
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-history-btn-42')
    await user.click(screen.getByTestId('tm-history-btn-42'))

    await screen.findByTestId('tm-history-dialog')
    await waitFor(() => {
      expect(mockListTMEntryHistory).toHaveBeenCalledWith(1, 42)
    })
  })
})

describe('TMBrowser entry deletion', () => {
  // Entries autosave with no undo, so deletion must be confirmed — clicking
  // the trash button opens a ConfirmDialog instead of deleting immediately.
  it('clicking the delete button opens the confirm dialog without deleting', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-delete-btn-42')
    await user.click(screen.getByTestId('tm-delete-btn-42'))

    await screen.findByText('Delete TM entry?')
    expect(mockDeleteTMEntry).not.toHaveBeenCalled()
  })

  it('confirming the dialog deletes the entry', async () => {
    mockDeleteTMEntry.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-delete-btn-42')
    await user.click(screen.getByTestId('tm-delete-btn-42'))
    await screen.findByText('Delete TM entry?')

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(mockDeleteTMEntry).toHaveBeenCalledWith(1, 42))
  })

  // A TM can hold thousands of loaded rows; one delete must not reload them all.
  it('a delete removes the row and lowers the count without reloading the entries', async () => {
    mockListTMEntries.mockResolvedValue({
      data: [SAMPLE_ENTRY, { ...SAMPLE_ENTRY, id: 43, source_text: 'Second' }],
      cursor: 0, has_more: false, total: 2,
    })
    mockDeleteTMEntry.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderTMBrowser()
    await user.click(await screen.findByTestId('tm-delete-btn-42'))
    await screen.findByText('Delete TM entry?')
    expect(screen.getByText('2 of 2 entries')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(screen.queryByTestId('tm-delete-btn-42')).toBeNull())
    expect(screen.getByTestId('tm-delete-btn-43')).toBeInTheDocument()
    expect(screen.getByText('1 of 1 entries')).toBeInTheDocument()
    // The entry counts and language pairs a delete can change are reloaded.
    await waitFor(() => expect(mockListTMLanguagePairs).toHaveBeenCalledTimes(2))
    expect(mockListTMEntries).toHaveBeenCalledTimes(1)
  })

  it('cancelling the dialog does not delete and closes the dialog', async () => {
    const user = userEvent.setup()
    renderTMBrowser()
    await screen.findByTestId('tm-delete-btn-42')
    await user.click(screen.getByTestId('tm-delete-btn-42'))
    await screen.findByText('Delete TM entry?')

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Delete TM entry?')).toBeNull())
    expect(mockDeleteTMEntry).not.toHaveBeenCalled()
  })
})

describe('TMBrowser search options', () => {
  const lastSearchCall = () => mockListTMEntries.mock.calls.at(-1)?.[1]

  it('sends the scope and match options with the search text', async () => {
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.click(screen.getByRole('radio', { name: 'Target' }))
    await userEvent.click(screen.getByLabelText('Exact'))
    await userEvent.click(screen.getByLabelText('Case sensitive'))
    await userEvent.type(screen.getByTestId('tm-search'), 'Hola mundo')

    await waitFor(() => expect(lastSearchCall()?.search).toBe('Hola mundo'))
    expect(lastSearchCall()?.searchOptions).toEqual({
      searchSource: false, searchTarget: true, exactMatch: true, caseSensitive: true, useRegex: false,
    })
  })

  it('shows the options without a toggle, at their defaults', async () => {
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    expect(screen.queryByTestId('tm-search-advanced-toggle')).not.toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Both' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Source' })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: 'Target' })).not.toBeChecked()
    for (const name of ['Exact', 'Case sensitive', 'Regex']) {
      expect(screen.getByLabelText(name)).not.toBeChecked()
    }
  })

  it('Search in picks one choice, and Both goes back to both fields', async () => {
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.type(screen.getByTestId('tm-search'), 'hello')
    await userEvent.click(screen.getByRole('radio', { name: 'Source' }))
    expect(screen.getByRole('radio', { name: 'Source' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Both' })).not.toBeChecked()
    await waitFor(() => expect(lastSearchCall()?.searchOptions).toMatchObject({ searchSource: true, searchTarget: false }))

    await userEvent.click(screen.getByRole('radio', { name: 'Both' }))
    await waitFor(() => expect(lastSearchCall()?.searchOptions).toMatchObject({ searchSource: true, searchTarget: true }))
  })

  it('tints hits only in the fields the search covers', async () => {
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.click(screen.getByRole('radio', { name: 'Source' }))
    await userEvent.type(screen.getByTestId('tm-search'), 'hello')

    await waitFor(() => expect(screen.getByText('Hello world')).toHaveAttribute('data-search', 'hello'))
    expect(screen.getByText('Hola mundo')).not.toHaveAttribute('data-search')
  })

  it('an invalid regex shows an error and never reaches the server', async () => {
    renderTMBrowser()
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.click(screen.getByLabelText('Regex'))
    await userEvent.type(screen.getByTestId('tm-search'), '(')

    expect(screen.getByTestId('tm-search-regex-error')).toHaveTextContent('Invalid regular expression')
    // Past the debounce: the rows of the previous listing are gone, no request was made.
    await waitFor(() => expect(screen.queryByTestId('tm-cell-42-source')).not.toBeInTheDocument())
    expect(mockListTMEntries.mock.calls.some(c => c[1]?.search === '(')).toBe(false)
  })

  // A search the server refuses fails for the same reason every time, so the
  // page must not retry it. Run with production's retry-once default, so the
  // control (a 500) proves retries are on.
  const PROD_RETRY = { retry: 1, retryDelay: 0 }
  const searchFailsWith = (err: ApiError) =>
    mockListTMEntries.mockImplementation((_id: number, opts: { search?: string }) => opts?.search
      ? Promise.reject(err)
      : Promise.resolve({ data: [SAMPLE_ENTRY], cursor: 0, has_more: false, total: 1 }))
  const searchCalls = () => mockListTMEntries.mock.calls.filter(c => c[1]?.search).length

  it('a search the server refuses shows its message, no rows, and is not retried', async () => {
    searchFailsWith(new ApiError(400, 'This regular expression is not valid.'))
    renderTMBrowser(PROD_RETRY)
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.click(screen.getByLabelText('Regex'))
    await userEvent.type(screen.getByTestId('tm-search'), '\\p{{L}')

    expect(await screen.findByTestId('tm-search-regex-error')).toHaveTextContent('This regular expression is not valid.')
    expect(screen.queryByTestId('tm-cell-42-source')).not.toBeInTheDocument()
    // Give a retry the time it would need with retryDelay 0.
    await new Promise(r => setTimeout(r, 50))
    expect(searchCalls()).toBe(1)
  })

  it('a server failure is retried once (control for the no-retry rule)', async () => {
    searchFailsWith(new ApiError(500, 'failed to list TM entries'))
    renderTMBrowser(PROD_RETRY)
    await screen.findByTestId('tm-cell-42-source')
    await userEvent.type(screen.getByTestId('tm-search'), 'hello')

    await waitFor(() => expect(searchCalls()).toBe(2))
  })
})

describe('TMBrowser failures', () => {
  it('a failed entry list shows the error with a Retry that loads the entries', async () => {
    mockListTMEntries.mockRejectedValue(new ApiError(500, 'failed to list TM entries'))
    renderTMBrowser({ retry: 1, retryDelay: 0 })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the entries: failed to list TM entries")
    expect(screen.queryByText(/No TM entries yet/)).toBeNull()

    mockListTMEntries.mockResolvedValue({ data: [SAMPLE_ENTRY], cursor: 0, has_more: false, total: 1 })
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('tm-cell-42-source')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('a delete the demo organization refuses shows no error toast of its own', async () => {
    // The query client shows the Playground notice for every refused write.
    mockDeleteTMEntry.mockRejectedValue(new ApiError(403, 'read only', { error: { code: 'demo_read_only' } }))
    const user = userEvent.setup()
    renderTMBrowser()
    await user.click(await screen.findByTestId('tm-delete-btn-42'))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(mockDeleteTMEntry).toHaveBeenCalledWith(1, 42))
    await new Promise(r => setTimeout(r, 0))
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('a delete that fails for another reason shows the error toast', async () => {
    mockDeleteTMEntry.mockRejectedValue(new ApiError(500, 'server boom'))
    const user = userEvent.setup()
    renderTMBrowser()
    await user.click(await screen.findByTestId('tm-delete-btn-42'))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Delete failed: server boom'))
  })
})
