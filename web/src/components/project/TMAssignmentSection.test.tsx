import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { ReactNode, ReactElement } from 'react'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DRAFT_SAVE_DELAY_MS } from '@/hooks/useProjectDraft'
import { TMAssignmentSection, reorderRows, type TMAssignmentItem, type TMResource } from './TMAssignmentSection'

// Edit-mode autoSave is debounced (~400ms) — use real timers + waitFor.

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

// The "add TM" picker → native <select> (`data-testid="add-tm-select"`); the
// create action is the option valued `__action__:create`.
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

vi.mock('@/components/ui/tooltip', async () => {
  const { cloneElement } = await import('react')
  return {
    Tooltip: ({ children }: { children?: ReactNode }) => children,
    TooltipTrigger: ({ children, render }: { children?: ReactNode; render?: ReactElement }) => (render ? cloneElement(render, {}, children) : children),
    TooltipContent: ({ children }: { children?: ReactNode }) => children,
    TooltipProvider: ({ children }: { children?: ReactNode }) => children,
  }
})

const tmCollections: TMResource[] = [
  { id: 1, name: 'EN→ES/FR TM', source_language: 'en', target_languages: ['es', 'fr'] },
  { id: 2, name: 'EN→DE only TM', source_language: 'en', target_languages: ['de'] },
  { id: 3, name: 'FR source TM', source_language: 'fr', target_languages: ['en'] },
]

function renderSection(overrides: Partial<Parameters<typeof TMAssignmentSection>[0]> = {}) {
  const defaults = {
    items: [] as TMAssignmentItem[],
    tmCollections,
    projectSourceLanguage: 'en',
    projectTargetLanguages: ['es', 'fr'],
    onSave: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  }
  return { ...render(<TMAssignmentSection {...defaults} />), onSave: defaults.onSave as ReturnType<typeof vi.fn> }
}

beforeEach(() => { vi.clearAllMocks() })

describe('TMAssignmentSection', () => {
  it('only offers TMs whose source language matches the project', () => {
    renderSection()
    const select = screen.getByTestId('add-tm-select') as HTMLSelectElement
    const labels = [...select.options].map(o => o.textContent)
    expect(labels).toContain('EN→ES/FR TM')
    expect(labels).toContain('EN→DE only TM')
    // FR-source TM must NOT be offered (project source is en).
    expect(labels).not.toContain('FR source TM')
  })

  it('renders an assigned TM with its declared languages read-only (no lang pickers)', () => {
    renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    expect(screen.getByText('EN→ES/FR TM')).toBeInTheDocument()
    expect(screen.getByText(/en → es, fr/i)).toBeInTheDocument()
    // No source/target language inputs anywhere.
    expect(screen.queryByPlaceholderText(/source/i)).not.toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/target/i)).not.toBeInTheDocument()
  })

  it('warns when the assigned TM shares no target with the project', () => {
    // TM 2 is en→[de]; project targets are es/fr → no overlap.
    renderSection({ items: [{ id: 1, tmId: 2, mode: 'read_write', priority: 0 }] })
    expect(screen.getByTestId('tm-assignment-no-overlap')).toBeInTheDocument()
  })

  it('does NOT warn when the assigned TM covers a project target', () => {
    renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    expect(screen.queryByTestId('tm-assignment-no-overlap')).not.toBeInTheDocument()
  })

  it('adds a TM (per-TM item) and debounce-saves', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection()
    await user.selectOptions(screen.getByTestId('add-tm-select'), '1')
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls.at(-1)![0]
    expect(saved).toEqual([{ id: 0, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 0 }])
  })

  it('does not offer an already-assigned TM', () => {
    renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    const select = screen.getByTestId('add-tm-select') as HTMLSelectElement
    expect([...select.options].map(o => o.textContent)).not.toContain('EN→ES/FR TM')
  })

  it('removes an assignment and saves the remainder', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    await user.click(screen.getByTestId('tm-assignment-remove-1'))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith([]))
    expect(await screen.findByText('Saved ✓')).toBeInTheDocument()
  })

  it('mode is two checkboxes; unchecking Write leaves mode = read', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    expect(screen.getByTestId('tm-assignment-read-1')).toBeChecked()
    expect(screen.getByTestId('tm-assignment-write-1')).toBeChecked()
    await user.click(screen.getByTestId('tm-assignment-write-1'))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0]).toEqual([{ id: 1, tmId: 1, mode: 'read', priority: 0 }])
  })

  it('keeps at least one mode checked — unchecking the only one is a no-op', async () => {
    const user = userEvent.setup()
    renderSection({ items: [{ id: 1, tmId: 1, mode: 'read', priority: 0 }] })
    const read = screen.getByTestId('tm-assignment-read-1')
    expect(read).toBeChecked()
    expect(screen.getByTestId('tm-assignment-write-1')).not.toBeChecked()
    await user.click(read) // try to clear the only checked mode
    expect(read).toBeChecked() // still checked — a TM with neither mode does nothing
  })

  it('create mode reports drafts synchronously (no onSave)', async () => {
    const user = userEvent.setup()
    const onDraftChange = vi.fn()
    render(
      <TMAssignmentSection
        items={[]}
        tmCollections={tmCollections}
        projectSourceLanguage="en"
        projectTargetLanguages={['es']}
        initialEditing
        onDraftChange={onDraftChange}
      />,
    )
    await user.selectOptions(screen.getByTestId('add-tm-select'), '1')
    expect(onDraftChange).toHaveBeenCalledWith([{ id: 0, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 0 }])
  })

  // Drag itself is dnd-kit (pointer + keyboard sensors) and needs layout, which
  // jsdom lacks — the real-browser check lives in e2e/tests/project-settings.spec.ts.
  // What is ours to test is the drop result: order = priority, renumbered.
  it('reorderRows moves a row and renumbers priority to match the order', () => {
    const rows: TMAssignmentItem[] = [
      { id: 1, tmId: 1, mode: 'read_write', priority: 0 },
      { id: 2, tmId: 2, mode: 'read_write', priority: 1 },
      { id: 3, tmId: 3, mode: 'read', priority: 2, penaltyPct: 5 },
    ]
    expect(reorderRows(rows, 2, 0)).toEqual([
      { id: 3, tmId: 3, mode: 'read', priority: 0, penaltyPct: 5 },
      { id: 1, tmId: 1, mode: 'read_write', priority: 1 },
      { id: 2, tmId: 2, mode: 'read_write', priority: 2 },
    ])
    expect(reorderRows(rows, 0, 1).map(r => r.tmId)).toEqual([2, 1, 3])
    expect(rows[0].priority).toBe(0) // input untouched
  })

  it('has a drag handle and no priority number input', () => {
    renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    expect(screen.getByTestId('tm-assignment-drag-1')).toBeInTheDocument()
    expect(screen.queryByTestId('tm-assignment-priority-1')).not.toBeInTheDocument()
  })

  it('no source filter when project source is unknown (template context)', () => {
    renderSection({ projectSourceLanguage: undefined })
    const labels = [...(screen.getByTestId('add-tm-select') as HTMLSelectElement).options].map(o => o.textContent)
    expect(labels).toContain('FR source TM') // all TMs offered
  })

  it('edits a TM penalty and persists it', async () => {
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })

    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })
    expect(penalty).toHaveValue(0)
    fireEvent.change(penalty, { target: { value: '5' } })
    fireEvent.blur(penalty, { target: { value: '5' } })

    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0]).toEqual([
      { id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 5 },
    ])
  })

  // The backend clamps too (settings.ClampPct); the UI shouldn't send a value
  // the CHECK constraint would reject.
  it('clamps an out-of-range penalty', async () => {
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })

    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })
    fireEvent.change(penalty, { target: { value: '150' } })
    fireEvent.blur(penalty, { target: { value: '150' } })
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0][0].penaltyPct).toBe(100)
  })

})

describe('TMAssignmentSection penalty saves while typing', () => {
  afterEach(() => { vi.useRealTimers() })

  it('saves after a pause while the field keeps focus', async () => {
    vi.useFakeTimers()
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] })
    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })
    act(() => penalty.focus())
    fireEvent.change(penalty, { target: { value: '7' } })
    // The field's pause, then the section's own save debounce.
    await act(async () => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS + 400) })
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0][0][0].penaltyPct).toBe(7)
    expect(penalty).toHaveFocus()
  })
})

describe('TMAssignmentSection refused penalty save', () => {
  it('puts the saved penalty back, and the same value can be saved again', async () => {
    const onSave = vi.fn().mockRejectedValueOnce(new Error('500')).mockResolvedValue(undefined)
    renderSection({ onSave, items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 3 }] })
    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })

    fireEvent.change(penalty, { target: { value: '25' } })
    fireEvent.blur(penalty)
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(penalty).toHaveValue(3))

    fireEvent.change(penalty, { target: { value: '25' } })
    fireEvent.blur(penalty)
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    expect(onSave.mock.calls[1][0][0].penaltyPct).toBe(25)
  })

  it('saves reach the server one at a time, in order', async () => {
    let finish: () => void = () => {}
    const onSave = vi.fn()
      .mockImplementationOnce(() => new Promise<void>(r => { finish = r }))
      .mockResolvedValue(undefined)
    renderSection({ onSave, items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 0 }] })
    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })

    fireEvent.change(penalty, { target: { value: '5' } })
    fireEvent.blur(penalty)
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    // A mode change while the penalty save is still on its way: a full replace
    // of the rows, so it must not overtake the first one.
    fireEvent.click(screen.getByRole('checkbox', { name: 'Write' }))
    await new Promise(r => setTimeout(r, 500))
    expect(onSave).toHaveBeenCalledTimes(1)

    await act(async () => { finish() })
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    expect(onSave.mock.calls[1][0][0]).toMatchObject({ mode: 'read', penaltyPct: 5 })
  })
})

describe('TMAssignmentSection refused save with another queued behind it', () => {
  it('the refused value never reaches the server, and the rows show what it holds', async () => {
    let refuse: (e: Error) => void = () => {}
    const onSave = vi.fn()
      .mockImplementationOnce(() => new Promise<void>((_, fail) => { refuse = fail }))
      .mockResolvedValue(undefined)
    renderSection({ onSave, items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 3 }] })
    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })

    fireEvent.change(penalty, { target: { value: '25' } })
    fireEvent.blur(penalty)
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    // A mode change while the penalty save is on its way: queued behind it.
    fireEvent.click(screen.getByRole('checkbox', { name: 'Write' }))
    await new Promise(r => setTimeout(r, 500))

    await act(async () => { refuse(new Error('500')) })
    await new Promise(r => setTimeout(r, 100))
    for (const [rows] of onSave.mock.calls.slice(1)) expect(rows[0].penaltyPct).toBe(3)
    await waitFor(() => expect(penalty).toHaveValue(3))
    expect(screen.getByRole('checkbox', { name: 'Write' })).toBeChecked()
  })
})

describe('TMAssignmentSection readOnly (rows belong to a TM profile)', () => {
  const items: TMAssignmentItem[] = [{ id: 1, tmId: 1, mode: 'read', priority: 0, penaltyPct: 12 }]

  it('shows the rows but no add picker, drag handle or remove button', () => {
    renderSection({ items, readOnly: true })
    expect(screen.getByTestId('tm-assignments')).toHaveAttribute('data-readonly', 'true')
    expect(screen.getByTestId('tm-assignment-row-1')).toBeInTheDocument()
    expect(screen.queryByTestId('add-tm-select')).toBeNull()
    expect(screen.queryByTestId('tm-assignment-drag-1')).toBeNull()
    expect(screen.queryByTestId('tm-assignment-remove-1')).toBeNull()
  })

  it('renders mode as locked checkboxes and the penalty as text', () => {
    const { onSave } = renderSection({ items, readOnly: true })
    expect(screen.getByTestId('tm-assignment-read-1')).toBeDisabled()
    expect(screen.getByTestId('tm-assignment-write-1')).toBeDisabled()
    expect(screen.getByTestId('tm-assignment-penalty-1')).toHaveTextContent('12%')
    fireEvent.click(screen.getByTestId('tm-assignment-write-1'))
    expect(onSave).not.toHaveBeenCalled()
  })

  it('embedded variant drops the card title in favour of a sub-heading', () => {
    renderSection({ items, variant: 'embedded' })
    expect(screen.queryByRole('heading', { name: 'Translation memory', level: 3 })).toBeNull()
    expect(screen.getByRole('heading', { name: 'Translation memories', level: 4 })).toBeInTheDocument()
  })
})

describe('TMAssignmentSection sequence column', () => {
  const items: TMAssignmentItem[] = [
    { id: 1, tmId: 1, mode: 'read_write', priority: 0 },
    { id: 2, tmId: 2, mode: 'read', priority: 1 },
  ]

  it('numbers the rows in lookup order and marks the first as Master', () => {
    renderSection({ items, projectTargetLanguages: ['es', 'de'] })
    expect(screen.getByText(/^Sequence/)).toBeInTheDocument()
    expect(screen.getByTestId('tm-assignment-sequence-1')).toHaveTextContent(/1.*Master/)
    expect(screen.getByTestId('tm-assignment-sequence-2')).toHaveTextContent('2')
    expect(screen.getByTestId('tm-assignment-sequence-2')).not.toHaveTextContent(/Master/)
    expect(screen.getAllByTestId('tm-assignment-master')).toHaveLength(1)
  })

  it('keeps numbers + Master in read-only mode (no handle)', () => {
    renderSection({ items, readOnly: true, projectTargetLanguages: ['es', 'de'] })
    expect(screen.queryByTestId('tm-assignment-drag-1')).toBeNull()
    expect(screen.getByTestId('tm-assignment-sequence-1')).toHaveTextContent(/1.*Master/)
  })
})

describe('TMAssignmentSection penalty field can be emptied', () => {
  it('shows empty (not 0) while typing, and blur on empty reverts without saving', async () => {
    const { onSave } = renderSection({ items: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 7 }] })
    const penalty = screen.getByRole('spinbutton', { name: /penalty for/i })
    fireEvent.change(penalty, { target: { value: '' } })
    expect((penalty as HTMLInputElement).value).toBe('')
    fireEvent.blur(penalty)
    expect(penalty).toHaveValue(7)
    await new Promise(r => setTimeout(r, 500))
    expect(onSave).not.toHaveBeenCalled()
  })
})
