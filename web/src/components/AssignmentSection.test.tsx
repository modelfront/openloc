import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode, ReactElement } from 'react'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { AssignmentSection, type AssignmentItem } from './AssignmentSection'

// The autoSave flow in edit mode is debounced via useDebouncedCallback (~400ms).
// Tests use real timers + waitFor() to poll for the assertion rather than
// fiddling with fake timers, which has known interaction issues with
// @testing-library/user-event's internal scheduling.

// Mock sonner toast
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

// Pickers → native <select>s (cmdk popovers are jsdom-hostile). The resource
// picker keeps `data-testid="resource-select"`; language pickers are reached by
// their placeholder (`getByLabelText('Target')`); the create action is the
// option valued `__action__:create`.
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

// Tooltip mock (base-ui needs portal/positioning unavailable in jsdom)
// NOTE: vi.mock factories are hoisted — can't import shared mocks. Keep in sync with test/mocks.ts.
vi.mock('@/components/ui/tooltip', async () => {
  const { cloneElement } = await import('react')
  return {
    Tooltip: ({ children }: { children?: ReactNode }) => children,
    TooltipTrigger: ({ children, render }: { children?: ReactNode; render?: ReactElement }) => render ? cloneElement(render, {}, children) : children,
    TooltipContent: ({ children }: { children?: ReactNode }) => children,
    TooltipProvider: ({ children }: { children?: ReactNode }) => children,
  }
})

const resources = [
  { id: 1, name: 'TM-English' },
  { id: 2, name: 'TM-French' },
]

const sampleItems: AssignmentItem[] = [
  { id: 10, resourceId: 1, resourceName: 'TM-English', sourceLang: 'en', targetLang: 'es' },
]

function renderSection(overrides: Partial<Parameters<typeof AssignmentSection>[0]> = {}) {
  const defaults = {
    title: 'Translation memory',
    items: sampleItems,
    resources,
    resourceLabel: 'TM',
    onSave: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  }
  return { ...render(<AssignmentSection {...defaults} />), onSave: defaults.onSave as ReturnType<typeof vi.fn> }
}

describe('AssignmentSection', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('renders title and existing rows', () => {
    renderSection()
    expect(screen.getByText('Translation memory')).toBeInTheDocument()
    // The resource select should show TM-English selected
    const selects = screen.getAllByRole('combobox')
    const resourceSelect = selects[0]
    expect(resourceSelect).toHaveValue('1')
  })

  it('shows empty-state message when no items', () => {
    renderSection({ items: [] })
    expect(screen.getByText(/no translation memory assigned/i)).toBeInTheDocument()
  })

  it('renders Add button disabled when form fields are empty', () => {
    renderSection()
    const addBtn = screen.getByRole('button', { name: /add/i })
    expect(addBtn).toBeDisabled()
  })

  it('adds a new row and calls onSave (after debounce)', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({ items: [] })

    // Fill the add-row form (no `languages` prop → plain inputs)
    const allSelects = screen.getAllByRole('combobox')
    const resourceSelect = allSelects[0]
    await user.selectOptions(resourceSelect, '1')

    const srcInput = screen.getByPlaceholderText('src')
    const tgtInput = screen.getByPlaceholderText('tgt')
    await user.type(srcInput, 'en')
    await user.type(tgtInput, 'es')

    const addBtn = screen.getByRole('button', { name: /add/i })
    expect(addBtn).not.toBeDisabled()
    await user.click(addBtn)

    // Debounced: onSave fires ~400ms later with the final state.
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    const savedItems = onSave.mock.calls[0][0]
    expect(savedItems).toHaveLength(1)
    expect(savedItems[0]).toMatchObject({
      resourceId: 1,
      resourceName: 'TM-English',
      sourceLang: 'en',
      targetLang: 'es',
    })
  })

  it('removes a row and calls onSave (after debounce)', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection()

    const removeBtn = screen.getAllByRole('button', { name: /remove/i })[0]
    expect(removeBtn).toBeTruthy()
    await user.click(removeBtn!)

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave.mock.calls[0][0]).toHaveLength(0)
    expect(await screen.findByText('Saved ✓')).toBeInTheDocument()
  })

  it('a refused save shows the server\'s message and never "Saved ✓"', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({ onSave: vi.fn().mockRejectedValue(new Error('The TM was deleted.')) })
    await user.click(screen.getAllByRole('button', { name: /remove/i })[0]!)

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('The TM was deleted.'))
    expect(screen.queryByText('Saved ✓')).not.toBeInTheDocument()
  })

  it('calls onDraftChange instead of onSave in create mode', async () => {
    const user = userEvent.setup()
    const onDraftChange = vi.fn()

    renderSection({
      items: [],
      initialEditing: true,
      onDraftChange,
      onSave: undefined,
    })

    const allSelects = screen.getAllByRole('combobox')
    await user.selectOptions(allSelects[0], '2')
    await user.type(screen.getByPlaceholderText('src'), 'fr')
    await user.type(screen.getByPlaceholderText('tgt'), 'de')
    await user.click(screen.getByRole('button', { name: /add/i }))

    expect(onDraftChange).toHaveBeenCalled()
    const lastCall = onDraftChange.mock.calls[onDraftChange.mock.calls.length - 1][0]
    expect(lastCall).toHaveLength(1)
    expect(lastCall[0]).toMatchObject({ resourceId: 2, sourceLang: 'fr', targetLang: 'de' })
  })

  it('renders Read/Write checkboxes (not a dropdown) and a drag handle when enabled', () => {
    renderSection({ hasMode: true, hasPriority: true })
    // Mode is two independent checkboxes now — no "Mode" dropdown.
    expect(screen.queryByText('Mode')).not.toBeInTheDocument()
    expect(screen.getAllByLabelText(/^read$/i).length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByLabelText(/^write$/i).length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByRole('checkbox').length).toBeGreaterThanOrEqual(2)
    // Priority is positional (drag-to-sort) — a reorder handle, no numeric input.
    expect(screen.getByRole('button', { name: /drag to reorder/i })).toBeInTheDocument()
    expect(screen.queryByText(/^Pri/)).not.toBeInTheDocument()
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
  })

  it('does not render mode/priority columns by default', () => {
    renderSection()
    expect(screen.queryByText('Mode')).not.toBeInTheDocument()
    expect(screen.queryByText('Pri')).not.toBeInTheDocument()
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: /drag to reorder/i })).not.toBeInTheDocument()
  })

  it('unchecking the Write checkbox saves mode = read', async () => {
    const user = userEvent.setup()
    const items: AssignmentItem[] = [
      { id: 10, resourceId: 1, resourceName: 'TM-English', sourceLang: 'en', targetLang: 'es', mode: 'read_write' },
    ]
    const { onSave } = renderSection({ hasMode: true, items })
    expect(screen.getByTestId('assignment-write-1-en-es')).toBeChecked()
    await user.click(screen.getByTestId('assignment-write-1-en-es'))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0][0]).toMatchObject({ mode: 'read' })
  })

  it('drag-reorder moves a row and reindexes priority (order = priority)', async () => {
    const twoRows: AssignmentItem[] = [
      { id: 10, resourceId: 1, resourceName: 'TM-English', sourceLang: 'en', targetLang: 'es', mode: 'read_write', priority: 0 },
      { id: 11, resourceId: 2, resourceName: 'TM-French', sourceLang: 'en', targetLang: 'fr', mode: 'read_write', priority: 1 },
    ]
    const { onSave } = renderSection({ hasMode: true, hasPriority: true, items: twoRows })
    const handles = screen.getAllByRole('button', { name: /drag to reorder/i })
    // Arm + drag the 2nd row's handle over the 1st row (dragEnter reflows), then drop.
    fireEvent.mouseDown(handles[1])
    const row2 = handles[1].closest('[style*="grid"]')!
    const row1 = handles[0].closest('[style*="grid"]')!
    fireEvent.dragStart(row2, { dataTransfer: {} })
    fireEvent.dragEnter(row1, { dataTransfer: {} })
    fireEvent.dragEnd(row2, { dataTransfer: {} })
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0]).toEqual([
      expect.objectContaining({ id: 11, priority: 0 }),
      expect.objectContaining({ id: 10, priority: 1 }),
    ])
  })

  // ── MT mode: source auto-filled from project, targets restricted ──
  const MT_LANGS = [
    { code: 'en', name: 'English' },
    { code: 'es', name: 'Spanish' },
    { code: 'fr', name: 'French' },
    { code: 'de', name: 'German' },
  ]

  it('autoSourceLanguage hides the Source column (header + add-row)', () => {
    renderSection({ languages: MT_LANGS, autoSourceLanguage: 'en', items: [] })
    // No Source header and no source picker anywhere.
    expect(screen.queryByText('Source')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Source')).not.toBeInTheDocument()
    // The Target picker is still present.
    expect(screen.getByLabelText('Target')).toBeInTheDocument()
  })

  it('restrictTargetsTo offers only project targets that are not already assigned', () => {
    const items: AssignmentItem[] = [
      { id: 10, resourceId: 1, resourceName: 'X', sourceLang: 'en', targetLang: 'es' },
    ]
    renderSection({ languages: MT_LANGS, autoSourceLanguage: 'en', restrictTargetsTo: ['es', 'fr'], items })
    // The last "Target" select is the add-row's.
    const addTarget = screen.getAllByLabelText('Target').at(-1) as HTMLSelectElement
    const opts = [...addTarget.options].map(o => o.value).filter(Boolean)
    // 'es' already assigned → excluded; 'de' not a project target → excluded.
    expect(opts).toEqual(['fr'])
  })

  it('hides the add-row once every project target has an engine', () => {
    const items: AssignmentItem[] = [
      { id: 1, resourceId: 1, resourceName: 'X', sourceLang: 'en', targetLang: 'es' },
      { id: 2, resourceId: 1, resourceName: 'X', sourceLang: 'en', targetLang: 'fr' },
    ]
    renderSection({ languages: MT_LANGS, autoSourceLanguage: 'en', restrictTargetsTo: ['es', 'fr'], items })
    expect(screen.queryByTestId('assignment-add-row-tm')).not.toBeInTheDocument()
  })

  it('shows the add-row while a project target still needs an engine', () => {
    const items: AssignmentItem[] = [
      { id: 1, resourceId: 1, resourceName: 'X', sourceLang: 'en', targetLang: 'es' },
    ]
    renderSection({ languages: MT_LANGS, autoSourceLanguage: 'en', restrictTargetsTo: ['es', 'fr'], items })
    expect(screen.getByTestId('assignment-add-row-tm')).toBeInTheDocument()
  })

  it('auto-fills source from the project when adding an MT row', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({ languages: MT_LANGS, autoSourceLanguage: 'en', restrictTargetsTo: ['es', 'fr'], items: [] })
    await user.selectOptions(screen.getByTestId('resource-select'), '1')
    await user.selectOptions(screen.getByLabelText('Target'), 'fr')
    await user.click(screen.getByRole('button', { name: /add/i }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0][0]).toMatchObject({ sourceLang: 'en', targetLang: 'fr' })
  })

  // ── Resource mode (Terminology): resource declares its languages ──
  const TB_RESOURCES = [{ id: 1, name: 'TB-en-de' }, { id: 2, name: 'TB-fr-es' }]
  const TB_LANGS = {
    1: { source_language: 'en', target_languages: ['de'] },
    2: { source_language: 'fr', target_languages: ['es'] },
  }

  it('resource mode: add control in the title, source-filtered, no in-table add-row', () => {
    renderSection({
      resourceLabel: 'Term base', resources: TB_RESOURCES, resourceLangs: TB_LANGS,
      autoSourceLanguage: 'en', hasMode: true, hasPriority: true, languages: MT_LANGS, items: [],
    })
    const select = screen.getByTestId('resource-select') as HTMLSelectElement
    const labels = [...select.options].map(o => o.textContent)
    expect(labels).toContain('TB-en-de')
    expect(labels).not.toContain('TB-fr-es') // source 'fr' ≠ project source 'en'
    // No in-table add-row — adding happens via the title control.
    expect(screen.queryByTestId('assignment-add-row-term-base')).not.toBeInTheDocument()
  })

  it('resource mode: rows show read-only languages, no Source/Target columns', () => {
    renderSection({
      resourceLabel: 'Term base', resources: TB_RESOURCES, resourceLangs: TB_LANGS,
      autoSourceLanguage: 'en', hasMode: true, hasPriority: true, languages: MT_LANGS,
      items: [{ id: 10, resourceId: 1, resourceName: 'TB-en-de', sourceLang: 'en', targetLang: 'de', mode: 'read_write', priority: 0 }],
    })
    expect(screen.getByText('en → de')).toBeInTheDocument()
    expect(screen.getByText('Languages')).toBeInTheDocument()
    expect(screen.queryByText('Source')).not.toBeInTheDocument()
    expect(screen.queryByText('Target')).not.toBeInTheDocument()
  })

  it('resource mode: adding via the picker uses the resource\'s declared languages', async () => {
    const user = userEvent.setup()
    const { onSave } = renderSection({
      resourceLabel: 'Term base', resources: TB_RESOURCES, resourceLangs: TB_LANGS,
      autoSourceLanguage: 'en', hasMode: true, hasPriority: true, languages: MT_LANGS, items: [],
    })
    await user.selectOptions(screen.getByTestId('resource-select'), '1')
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls.at(-1)![0]).toEqual([
      expect.objectContaining({ resourceId: 1, sourceLang: 'en', targetLang: 'de', mode: 'read_write', priority: 0 }),
    ])
  })

  it('resource mode: fans out one row per project target the base also covers', async () => {
    const user = userEvent.setup()
    // Base covers de+fr; project translates into de, fr and es. The base has no
    // es terms, so only de + fr fan out — es is skipped.
    const resources = [{ id: 5, name: 'TB-multi' }]
    const resourceLangs = { 5: { source_language: 'en', target_languages: ['de', 'fr'] } }
    const { onSave } = renderSection({
      resourceLabel: 'Term base', resources, resourceLangs,
      autoSourceLanguage: 'en', restrictTargetsTo: ['de', 'fr', 'es'],
      hasMode: true, hasPriority: true, languages: MT_LANGS, items: [],
    })
    await user.selectOptions(screen.getByTestId('resource-select'), '5')
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls.at(-1)![0]
    expect(saved).toEqual([
      expect.objectContaining({ resourceId: 5, sourceLang: 'en', targetLang: 'de', priority: 0 }),
      expect.objectContaining({ resourceId: 5, sourceLang: 'en', targetLang: 'fr', priority: 1 }),
    ])
  })

  it('resource mode: locale-variant base targets fan out onto the matching project targets', async () => {
    const user = userEvent.setup()
    // Base targets are locale variants (de-de, fr-fr); project targets are the
    // base codes (de, fr). They must still overlap (base-subtag match), and the
    // stored target is the project's own code.
    const resources = [{ id: 6, name: 'TB-locale' }]
    const resourceLangs = { 6: { source_language: 'en-us', target_languages: ['de-de', 'fr-fr'] } }
    const { onSave } = renderSection({
      resourceLabel: 'Term base', resources, resourceLangs,
      autoSourceLanguage: 'en', restrictTargetsTo: ['de', 'fr', 'es'],
      languages: MT_LANGS, items: [],
    })
    await user.selectOptions(screen.getByTestId('resource-select'), '6')
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls.at(-1)![0]
    expect(saved.map((r: AssignmentItem) => r.targetLang)).toEqual(['de', 'fr'])
    // Source stored lower-cased from the base's declared source.
    expect(saved.every((r: AssignmentItem) => r.sourceLang === 'en-us')).toBe(true)
  })

  it('resource mode: a brand-new base with no terms yet fans out to ALL project targets', async () => {
    const user = userEvent.setup()
    const resources = [{ id: 7, name: 'TB-empty' }]
    const resourceLangs = { 7: { source_language: 'en', target_languages: [] as string[] } }
    const { onSave } = renderSection({
      resourceLabel: 'Term base', resources, resourceLangs,
      autoSourceLanguage: 'en', restrictTargetsTo: ['de', 'fr', 'es'],
      languages: MT_LANGS, items: [],
    })
    await user.selectOptions(screen.getByTestId('resource-select'), '7')
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    const saved = onSave.mock.calls.at(-1)![0]
    // No terms yet → could cover any target once filled → one row per project target.
    expect(saved.map((r: AssignmentItem) => r.targetLang)).toEqual(['de', 'fr', 'es'])
    expect(saved.every((r: AssignmentItem) => r.resourceId === 7 && r.sourceLang === 'en')).toBe(true)
  })

  it('calls onSave with updated rows when removing', async () => {
    const user = userEvent.setup()
    const twoItems: AssignmentItem[] = [
      { id: 10, resourceId: 1, resourceName: 'TM-English', sourceLang: 'en', targetLang: 'es' },
      { id: 11, resourceId: 2, resourceName: 'TM-French', sourceLang: 'fr', targetLang: 'de' },
    ]
    const { onSave } = renderSection({ items: twoItems })

    // Remove the first item
    const removeBtn = screen.getAllByRole('button', { name: /remove/i })[0]
    await user.click(removeBtn!)

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith([
        expect.objectContaining({ resourceId: 2, sourceLang: 'fr' }),
      ]),
    )
  })

  // Section 5.3: The main value of debouncing — rapid successive removes should
  // collapse into a single PUT with the final state. Before the fix, each
  // remove triggered a full-replace PUT and overlapping requests could race.
  it('collapses rapid successive removes into a single onSave call with the final state', async () => {
    const threeItems: AssignmentItem[] = [
      { id: 10, resourceId: 1, resourceName: 'TM-A', sourceLang: 'en', targetLang: 'es' },
      { id: 11, resourceId: 2, resourceName: 'TM-B', sourceLang: 'en', targetLang: 'fr' },
      { id: 12, resourceId: 1, resourceName: 'TM-A', sourceLang: 'en', targetLang: 'de' },
    ]
    const { onSave } = renderSection({ items: threeItems })

    // Rapid-fire: remove the first row three times (each click removes the then-first row).
    // Without debounce this would be 3 overlapping PUTs; with debounce it's 1 PUT with [].
    //
    // fireEvent, not userEvent: three `await user.click()` round trips can take
    // longer than the 400ms debounce under full-suite load, flushing a PARTIAL
    // save and failing on a payload that was merely early. fireEvent is
    // synchronous, so all three removes land inside one debounce window
    // deterministically. And the button is asserted, never `if`-guarded — a
    // skipped click here reproduces the exact same failure silently.
    for (let i = 0; i < 3; i++) {
      const removeButtons = screen.getAllByRole('button', { name: /remove/i })
      expect(removeButtons).toHaveLength(3 - i)
      fireEvent.click(removeButtons[0]!)
    }

    // Exactly ONE call, containing the final state (empty list), after the debounce settles.
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave.mock.calls[0][0]).toHaveLength(0)
  })

  // ─── Inline create (+ Create new) flow ─────────────────────────────────
  //
  // When `onCreateNewResource` is provided, the resource dropdown gains a
  // "+ Create new..." entry. Clicking it calls the callback, which opens a
  // modal dialog in the parent. On success, the new resource is auto-assigned
  // to the Add-row form. If the result includes source/target langs (e.g.
  // a Term base has them baked in), those pre-fill the lang fields too.

  it('does NOT show "+ Create new" option when onCreateNewResource is not provided', () => {
    renderSection({ items: [] })
    // The mocked picker renders as a <select> — no __action__:create option
    expect(screen.queryByRole('option', { name: /create new/i })).not.toBeInTheDocument()
  })

  it('shows "+ Create new" option when onCreateNewResource is provided', () => {
    renderSection({
      items: [],
      onCreateNewResource: vi.fn(),
      createNewLabel: 'Create new TM...',
    })
    expect(screen.getByRole('option', { name: /create new tm/i })).toBeInTheDocument()
  })

  // MT is the one section whose picker is kind-aware, so it uses
  // `resourceActions` (link-out entries) instead of the create action
  // "+ Create new" dialog flow the other sections share.
  it('renders resourceActions on the kind-aware (MT) add-row picker', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    renderSection({
      items: [],
      resources: [{ id: 1, name: 'DeepL', kind: 'mt' }],
      resourceLabel: 'Engine',
      resourceActions: [{ key: 'new-mt-engine', label: 'New MT engine', onSelect }],
    })

    // The picker is the native-select mock: an action is the option valued `__action__:<key>`.
    await user.selectOptions(screen.getByTestId('assignment-add-resource-engine'), '__action__:new-mt-engine')
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('renders no actions on the MT picker when resourceActions is omitted', () => {
    renderSection({
      items: [],
      resources: [{ id: 1, name: 'DeepL', kind: 'mt' }],
      resourceLabel: 'Engine',
    })
    expect(screen.getByTestId('assignment-add-resource-engine')).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /new mt engine/i })).not.toBeInTheDocument()
  })

  it('calls onCreateNewResource with current row langs as context', async () => {
    const onCreateNewResource = vi.fn().mockResolvedValue({ id: 42 })
    const user = userEvent.setup()
    renderSection({
      items: [],
      onCreateNewResource,
      createNewLabel: 'Create new TM...',
    })

    // Fill the lang fields first so we can verify they're passed as context
    await user.type(screen.getByPlaceholderText('src'), 'en')
    await user.type(screen.getByPlaceholderText('tgt'), 'es')

    // Click "+ Create new" in the resource dropdown
    const select = screen.getByTestId('resource-select')
    await user.selectOptions(select, '__action__:create')

    await waitFor(() => {
      expect(onCreateNewResource).toHaveBeenCalledWith({ sourceLang: 'en', targetLang: 'es' })
    })
  })

  it('auto-assigns the newly-created resource id to the Add-row', async () => {
    const onCreateNewResource = vi.fn().mockResolvedValue({ id: 99 })
    const user = userEvent.setup()
    renderSection({
      items: [],
      onCreateNewResource,
      createNewLabel: 'Create new TM...',
      // Also pass a resource list so the select has options
      resources: [{ id: 99, name: 'Newly Created TM' }],
    })

    const select = screen.getByTestId('resource-select')
    await user.selectOptions(select, '__action__:create')

    // After onCreateNewResource resolves, the select value should be 99
    await waitFor(() => {
      expect((select as HTMLSelectElement).value).toBe('99')
    })
  })

  it('auto-fills source/target langs when onCreateNewResource returns them', async () => {
    const onCreateNewResource = vi.fn().mockResolvedValue({ id: 77, sourceLang: 'en', targetLang: 'de' })
    const user = userEvent.setup()
    renderSection({
      items: [],
      onCreateNewResource,
      createNewLabel: 'Create new term base...',
      resources: [{ id: 77, name: 'Legal TB' }],
    })

    const select = screen.getByTestId('resource-select')
    await user.selectOptions(select, '__action__:create')

    // Both src and tgt inputs should be pre-filled from the result
    await waitFor(() => {
      expect(screen.getByPlaceholderText('src')).toHaveValue('en')
      expect(screen.getByPlaceholderText('tgt')).toHaveValue('de')
    })
  })

  it('does NOT auto-assign when onCreateNewResource resolves with null (user cancelled)', async () => {
    const onCreateNewResource = vi.fn().mockResolvedValue(null)
    const user = userEvent.setup()
    renderSection({
      items: [],
      onCreateNewResource,
      createNewLabel: 'Create new TM...',
      resources: [{ id: 1, name: 'TM-English' }],
    })

    const select = screen.getByTestId('resource-select')
    await user.selectOptions(select, '__action__:create')

    // Select value should remain empty (no assignment)
    await waitFor(() => expect(onCreateNewResource).toHaveBeenCalled())
    expect((select as HTMLSelectElement).value).toBe('')
  })

  // Section 5.3 unmount safety: the pending save must be flushed, not dropped,
  // so a user who navigates away mid-debounce doesn't lose their last change.
  it('flushes the pending save on unmount so the last change is not lost', async () => {
    const user = userEvent.setup()
    const { onSave, unmount } = renderSection()

    const removeBtn = screen.getAllByRole('button', { name: /remove/i })[0]
    await user.click(removeBtn!)

    // Within the debounce window — onSave has not yet fired.
    expect(onSave).not.toHaveBeenCalled()

    // Unmount BEFORE the debounce timer naturally fires. The flush-on-unmount
    // effect should fire the pending save synchronously.
    unmount()

    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0][0]).toHaveLength(0)
  })
})
