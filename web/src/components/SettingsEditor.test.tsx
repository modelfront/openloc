import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ComponentProps, ReactNode, ReactElement } from 'react'
import type { ComboboxProps } from '@/components/ui/combobox'
import type { AssignmentSection } from './AssignmentSection'
import type { TMAssignmentSection } from './project/TMAssignmentSection'

/** The mocked assignment sections leave the pending create-new promise here. */
type CreateNewWindow = Window & { __lastCreateNewPromise?: ReturnType<NonNullable<ComponentProps<typeof AssignmentSection>['onCreateNewResource']>> }
import { render, screen, within, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { SettingsEditor, type SettingsValues } from './SettingsEditor'

// Render Combobox as a native <select> in tests so selectOptions() keeps working.
// Pinned actions become sibling buttons (a real <option> would make them look
// selectable, which is exactly what they must not be).
vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, actions, disabled, className, clearable, 'data-testid': testId }: ComboboxProps) => (
    <>
      <select
        data-testid={testId}
        value={value}
        disabled={disabled}
        className={className}
        onChange={(e) => onChange(e.target.value)}
      >
        {/* A picker without a "None" row still needs an option for the empty
            value, or jsdom snaps the select to its first option. */}
        {!options.some(o => o.value === '') && <option value="" hidden />}
        {options.map(o => (
          <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>
        ))}
      </select>
      {clearable && value !== '' && (
        <button data-testid={testId ? `${testId}-clear` : undefined} onClick={() => onChange('')}>Clear</button>
      )}
      {(actions ?? []).map(a => (
        <button key={a.key} data-testid={testId ? `${testId}-action-${a.key}` : undefined} onClick={a.onSelect}>
          {a.label}
        </button>
      ))}
    </>
  ),
}))

// Mock sonner
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

// Mock AssignmentSection — expose onDraftChange and onCreateNewResource via
// buttons so tests can trigger them. The onCreateNewResource button calls the
// callback and stores the resulting promise on window for tests to await.
vi.mock('./AssignmentSection', () => ({
  resKey: (kind: string | undefined, id: number) => (kind ? `${kind}:${id}` : String(id)),
  parseResKey: (v: string) => {
    const i = v.indexOf(':')
    return i === -1 ? { id: Number(v) } : { kind: v.slice(0, i), id: Number(v.slice(i + 1)) }
  },
  AssignmentSection: ({ title, onDraftChange, onCreateNewResource, headerContent, resourceActions }: Pick<ComponentProps<typeof AssignmentSection>, 'title' | 'onDraftChange' | 'onCreateNewResource' | 'headerContent' | 'resourceActions'>) => (
    <div data-testid={`assignment-section-${title.toLowerCase().replace(/\s/g, '-')}`}>
      {title}
      {headerContent}
      {(resourceActions ?? []).map(a => (
        <button key={a.key} data-testid={`row-action-${title.toLowerCase().replace(/\s/g, '-')}-${a.key}`} onClick={a.onSelect}>
          {a.label}
        </button>
      ))}
      {onDraftChange && (
        <button data-testid={`draft-${title.toLowerCase().replace(/\s/g, '-')}`}
          onClick={() => onDraftChange([{ id: 0, resourceId: 1, resourceName: 'Test', sourceLang: 'en', targetLang: 'fr' }])}
        >Add Draft</button>
      )}
      {onCreateNewResource && (
        <button data-testid={`create-new-${title.toLowerCase().replace(/\s/g, '-')}`}
          onClick={() => {
            // Store the pending promise on window so tests can inspect it
            ;(window as CreateNewWindow).__lastCreateNewPromise = onCreateNewResource({ sourceLang: 'en', targetLang: 'fr' })
          }}
        >Create New</button>
      )}
    </div>
  ),
}))

// Mock TMAssignmentSection (per-TM). Mirrors the AssignmentSection mock
// so the existing "renders all sections" / draft / create-new tests still work,
// but emits a per-TM item shape.
vi.mock('./project/TMAssignmentSection', () => ({
  TMAssignmentSection: ({ onDraftChange, onCreateNewResource, footer }: Pick<ComponentProps<typeof TMAssignmentSection>, 'onDraftChange' | 'onCreateNewResource' | 'footer'>) => (
    <div data-testid="assignment-section-translation-memory">
      {footer}
      Translation memory
      {onDraftChange && (
        <button data-testid="draft-translation-memory"
          onClick={() => onDraftChange([{ id: 0, tmId: 1, mode: 'read_write', priority: 0 }])}
        >Add Draft</button>
      )}
      {onDraftChange && (
        <button data-testid="draft-two-translation-memories"
          onClick={() => onDraftChange([
            { id: 0, tmId: 1, mode: 'read_write', priority: 0 },
            { id: 0, tmId: 2, mode: 'read', priority: 1 },
          ])}
        >Add Two</button>
      )}
      {onCreateNewResource && (
        <button data-testid="create-new-translation-memory"
          onClick={() => { (window as CreateNewWindow).__lastCreateNewPromise = onCreateNewResource({ sourceLang: 'en', targetLang: '' }) }}
        >Create New</button>
      )}
    </div>
  ),
}))

// Mock API calls
vi.mock('../api', () => ({
  listTMLanguagePairs: vi.fn().mockResolvedValue([]),
  getTMProfile: vi.fn().mockResolvedValue({ id: 7, name: 'Acme', usage_count: 0, fuzzy_threshold: 0.5, sublanguage_penalty_pct: 1, near_exact_penalty_pct: 1, tm_assignments: [] }),
  createTMProfile: vi.fn().mockResolvedValue({ id: 8 }),
  updateTMProfile: vi.fn().mockResolvedValue({}),
  ApiError: class ApiError extends Error { status: number; constructor(status: number, message: string) { super(message); this.status = status } },
  setTMProfileTMs: vi.fn().mockResolvedValue([]),
  getEngineLanguages: vi.fn().mockResolvedValue([]),
  getTermBase: vi.fn().mockResolvedValue({ term_base: { source_language: 'en', target_language: 'es' }, entry_count: 0 }),
  // The MT pickers decorate AI-as-MT engines with provider/model/mode chips
  // (ai-engine-chips); empty catalogs = no chips, which these tests don't check.
  listAIEngines: vi.fn().mockResolvedValue([]),
  listAIPrompts: vi.fn().mockResolvedValue([]),
  listAIVendors: vi.fn().mockResolvedValue([]),
}))

// Mock inline-create dialogs — they use useMutation which needs a QueryClient.
// SettingsEditor tests don't wrap with a QueryClientProvider, so mock the
// dialogs to expose their onCreated/onClose callbacks via test buttons.
vi.mock('./CreateTMDialog', () => ({
  CreateTMDialog: ({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: number) => void }) =>
    open ? (
      <div data-testid="create-tm-dialog">
        Create TM Dialog
        <button data-testid="tm-dialog-created" onClick={() => onCreated(99)}>Fire onCreated</button>
        <button data-testid="tm-dialog-close" onClick={onClose}>Fire onClose</button>
      </div>
    ) : null,
}))
vi.mock('./CreateTermBaseDialog', () => ({
  CreateTermBaseDialog: ({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: number) => void }) =>
    open ? (
      <div data-testid="create-tb-dialog">
        Create term base Dialog
        <button data-testid="tb-dialog-created" onClick={() => onCreated(77)}>Fire onCreated</button>
        <button data-testid="tb-dialog-close" onClick={onClose}>Fire onClose</button>
      </div>
    ) : null,
}))

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

const baseValues: SettingsValues = {
  fuzzyThreshold: 0.75,
  sublanguagePenaltyPct: 1,
  nearExactPenaltyPct: 1,
  tmLookupOrder: 'match_percent',
  mtOnUpload: false,
  tmOnUpload: false,
  autoComplete: true,
  inlineHTML: true,
  qaProfileId: null,
  tmAssignments: [],
  mtRouterId: null,
  termAssignments: [],
  workflowSteps: [{ step_definition_id: 1, name: 'Translation', step_type: 'human' }],
}

const qaProfiles = [
  { id: 1, name: 'Standard QA' },
  { id: 2, name: 'Strict QA' },
]

// A router whose default engine takes every target language.
const coversAll = { rules: [], default_mt_engine_id: 1 }

function renderEditor(overrides: Partial<Parameters<typeof SettingsEditor>[0]> = {}) {
  const defaults = {
    values: baseValues,
    tmCollections: [{ id: 1, name: 'Default TM' }],
    mtRouters: [{ id: 1, name: 'Default MT router', ...coversAll }],
    termBases: [{ id: 1, name: 'Term base', source_language: 'en', target_languages: ['de'] }],
    qaProfiles,
    mode: 'edit' as const,
    onUpdateScalars: vi.fn().mockResolvedValue(undefined),
    onSaveTMAssignments: vi.fn().mockResolvedValue(undefined),
    onSaveTermAssignments: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  }
  // QueryClient + Router: the MT router picker's "New MT router" entry
  // navigates and invalidates the router caches (useEngineCreateActions).
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SettingsEditor {...defaults} />
        </MemoryRouter>
      </QueryClientProvider>,
    ),
    onUpdateScalars: defaults.onUpdateScalars as ReturnType<typeof vi.fn>,
  }
}

describe('SettingsEditor', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('puts each pre-translation switch in the card it controls', () => {
    renderEditor()
    const tmCard = screen.getByTestId('tm-section')
    const mtCard = screen.getByTestId('mt-section')
    expect(within(tmCard).getByRole('checkbox', { name: /translation memory.*start or upload/i })).toBeInTheDocument()
    expect(within(mtCard).getByRole('checkbox', { name: /machine translation.*start or upload/i })).toBeInTheDocument()
    // Project settings keeps only the project-wide switches.
    const projectCard = screen.getByText('Project settings').closest('div')!
    expect(within(projectCard).queryByRole('checkbox', { name: /start or upload/i })).not.toBeInTheDocument()
    expect(within(projectCard).getByTestId('settings-auto-complete')).toBeInTheDocument()
  })

  // JSON, PO, TXT and Excel strings are HTML unless this is off.
  describe('Strings contain HTML', () => {
    const htmlSwitch = () => screen.getByRole('checkbox', { name: /strings contain html/i })

    it('shows the stored value in the project settings card', () => {
      renderEditor({ values: { ...baseValues, inlineHTML: false } })
      const projectCard = screen.getByText('Project settings').closest('div')!
      expect(within(projectCard).getByTestId('settings-inline-html')).not.toBeChecked()
    })

    it('edit mode: turning it off saves inline_html false', async () => {
      const { onUpdateScalars } = renderEditor()
      expect(htmlSwitch()).toBeChecked()
      await userEvent.click(htmlSwitch())
      expect(onUpdateScalars).toHaveBeenCalledWith({ inline_html: false })
    })

    it('create mode: turning it off reaches the draft and keeps the other values', async () => {
      const onChange = vi.fn()
      renderEditor({ mode: 'create', values: { ...baseValues, autoComplete: false }, onChange, onUpdateScalars: undefined })
      await userEvent.click(htmlSwitch())
      expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ inlineHTML: false, autoComplete: false }))
      expect(htmlSwitch()).not.toBeChecked()
    })
  })

  it('renders all sections', () => {
    renderEditor()

    expect(screen.getByText('Project settings')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })).toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: /fuzzy match threshold/i })).toBeInTheDocument()
    expect(screen.getByText(/qa profile/i)).toBeInTheDocument()

    expect(screen.getByTestId('assignment-section-translation-memory')).toBeInTheDocument()
    expect(screen.getByTestId('mt-section')).toBeInTheDocument()
    expect(screen.getByTestId('mt-router-select')).toBeInTheDocument()
    expect(screen.getByTestId('assignment-section-terminology')).toBeInTheDocument()
  })

  // The New project page's Workflow card names the MT router before anything
  // is saved, so the editor hands it the values it holds.
  it('gives afterProjectSettings the live MT router and MT-on-upload values', () => {
    renderEditor({
      mode: 'create',
      onChange: vi.fn(),
      values: { ...baseValues, mtRouterId: null, mtOnUpload: true },
      mtRouters: [{ id: 1, name: 'Default MT router', ...coversAll }, { id: 2, name: 'Backup MT router', ...coversAll }],
      afterProjectSettings: ({ mtRouterName, mtOnUpload }) => (
        <span data-testid="live">{mtRouterName ?? 'none'}|{String(mtOnUpload)}</span>
      ),
    })
    expect(screen.getByTestId('live')).toHaveTextContent('none|true')
    fireEvent.change(screen.getByTestId('mt-router-select'), { target: { value: '2' } })
    expect(screen.getByTestId('live')).toHaveTextContent('Backup MT router|true')
  })

  // Pre-translation fills files in with MT, so its MT switch is on and
  // cannot be turned off (the server refuses it too).
  describe('Pre-translation workflow', () => {
    const preValues: SettingsValues = {
      ...baseValues,
      mtOnUpload: false,
      mtRouterId: 1,
      workflowSteps: [{ step_definition_id: 9, name: 'Pre-translation', step_type: 'pretranslate' }],
    }
    const mtSwitch = () => screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })

    it('shows MT on upload on and locked', async () => {
      const { onUpdateScalars } = renderEditor({ values: preValues })
      expect(mtSwitch()).toBeChecked()
      expect(mtSwitch()).toBeDisabled()
      await userEvent.click(mtSwitch())
      expect(onUpdateScalars).not.toHaveBeenCalled()
      expect(screen.getByTestId('settings-mt-locked')).toHaveTextContent('Always on with Raw MT.')
      expect(screen.queryByTestId('settings-mt-router-required')).not.toBeInTheDocument()
    })

    it('asks for an MT router when none is chosen', () => {
      renderEditor({ values: { ...preValues, mtRouterId: null } })
      expect(screen.getByTestId('settings-mt-router-required')).toHaveTextContent('Choose an MT router: the project cannot start without one.')
    })

    it('leaves the switch alone for any other workflow', () => {
      renderEditor()
      expect(mtSwitch()).not.toBeChecked()
      expect(mtSwitch()).toBeEnabled()
      expect(screen.queryByTestId('settings-mt-locked')).not.toBeInTheDocument()
    })
  })

  // A new project asks where the TMs and the MT come from before it lets the
  // user switch the pre-translation on or off.
  describe('create mode: the unanswered TM / MT source', () => {
    const tmSwitch = () => screen.getByRole('checkbox', { name: /translation memory.*start or upload/i })
    const mtSwitch = () => screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })

    it('starts with no TM source chosen: no memory list, and the TM switch is locked on', () => {
      renderEditor({ mode: 'create', values: { ...baseValues, tmOnUpload: true }, onChange: vi.fn(), onUpdateScalars: undefined })
      expect(screen.getByTestId('tm-profile-select')).toHaveValue('')
      expect(screen.queryByTestId('assignment-section-translation-memory')).toBeNull()
      expect(tmSwitch()).toBeChecked()
      expect(tmSwitch()).toBeDisabled()
    })

    it('choosing "From scratch" reveals the memory list and unlocks the TM switch', async () => {
      const user = userEvent.setup()
      const onChange = vi.fn()
      renderEditor({ mode: 'create', values: { ...baseValues, tmOnUpload: true }, onChange, onUpdateScalars: undefined })

      await user.selectOptions(screen.getByTestId('tm-profile-select'), 'custom')

      expect(screen.getByTestId('assignment-section-translation-memory')).toBeInTheDocument()
      expect(tmSwitch()).not.toBeDisabled()
      await user.click(tmSwitch())
      expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ tmOnUpload: false }))
    })

    // The wizard never feeds edits back through `values` (it keeps them in a
    // ref), so the lookup order toggle has to track both its own choice and the
    // TM rows being added.
    it('"From scratch": the TM lookup order appears at two TMs, then switches and reaches the draft', async () => {
      const user = userEvent.setup()
      const onChange = vi.fn()
      renderEditor({ mode: 'create', values: { ...baseValues, tmOnUpload: true }, onChange, onUpdateScalars: undefined })
      const orderSelect = () => screen.getByTestId('tm-lookup-order-select')
      const field = () => screen.queryByTestId('tm-lookup-order-field')
      await user.selectOptions(screen.getByTestId('tm-profile-select'), 'custom')
      expect(field()).toBeNull()
      await user.click(screen.getByTestId('draft-translation-memory'))
      expect(field()).toBeNull()
      await user.click(screen.getByTestId('draft-two-translation-memories'))
      expect(orderSelect()).not.toBeDisabled()
      expect(orderSelect()).toHaveValue('match_percent')

      await user.selectOptions(orderSelect(), 'tm_order')
      expect(orderSelect()).toHaveValue('tm_order')
      expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ tmLookupOrder: 'tm_order' }))

      await user.selectOptions(orderSelect(), 'match_percent')
      expect(orderSelect()).toHaveValue('match_percent')
      expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ tmLookupOrder: 'match_percent' }))
    })

    it('a prefilled TM list counts as an answer (a template brings its own)', () => {
      renderEditor({
        mode: 'create',
        values: { ...baseValues, tmAssignments: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0 }] },
        onChange: vi.fn(),
        onUpdateScalars: undefined,
      })
      expect(screen.getByTestId('assignment-section-translation-memory')).toBeInTheDocument()
      expect(tmSwitch()).not.toBeDisabled()
    })

    it('the MT switch is locked on until a router is picked', async () => {
      const user = userEvent.setup()
      renderEditor({ mode: 'create', values: { ...baseValues, mtOnUpload: true }, onChange: vi.fn(), onUpdateScalars: undefined })
      expect(mtSwitch()).toBeChecked()
      expect(mtSwitch()).toBeDisabled()

      await user.selectOptions(screen.getByTestId('mt-router-select'), '1')
      expect(mtSwitch()).not.toBeDisabled()
    })

    it('a saved project always shows its setup, even with nothing assigned', () => {
      renderEditor({ values: baseValues })
      expect(screen.getByTestId('tm-profile-select')).toHaveValue('custom')
      expect(screen.getByTestId('assignment-section-translation-memory')).toBeInTheDocument()
      expect(tmSwitch()).not.toBeDisabled()
      expect(mtSwitch()).not.toBeDisabled()
    })
  })

  it('create mode: picking an MT router sticks in the picker and reaches the draft', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor({
      mode: 'create',
      values: baseValues,
      mtRouters: [{ id: 1, name: 'Default MT router', ...coversAll }],
      onChange,
    })

    const select = screen.getByTestId('mt-router-select')
    expect(select).toHaveValue('') // starts at "None"
    await user.selectOptions(select, '1')
    // Create mode has no server round-trip: the picker must reflect the draft,
    // not the frozen `values` prop.
    expect(select).toHaveValue('1')
    expect(onChange.mock.calls.at(-1)![0].mtRouterId).toBe(1)
  })

  it('edit mode: the picker shows the router, and its clear X saves null', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor({ values: { ...baseValues, mtRouterId: 1 } })
    const select = screen.getByTestId('mt-router-select')
    expect(select).toHaveValue('1')
    expect(within(select).queryByRole('option', { name: 'None' })).not.toBeInTheDocument()
    await user.click(screen.getByTestId('mt-router-select-clear'))
    expect(onUpdateScalars).toHaveBeenCalledWith({ mt_router_id: null })
  })

  it('hides the clear X while no router is picked', () => {
    renderEditor({ values: { ...baseValues, mtRouterId: null } })
    expect(screen.queryByTestId('mt-router-select-clear')).not.toBeInTheDocument()
  })

  // An empty router list must not be a dead end: the picker offers the MT page.
  it('offers "New MT router" on the router picker', async () => {
    const user = userEvent.setup()
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    const onChange = vi.fn()
    renderEditor({ mode: 'create', mtRouters: [], onChange })

    await user.click(screen.getByTestId('mt-router-select-action-new-mt-router'))
    expect(open).toHaveBeenCalledWith('/mt', '_blank')

    // An action isn't a selection — the router must stay untouched.
    expect(screen.getByTestId('mt-router-select')).toHaveValue('')
    expect(onChange).not.toHaveBeenCalled()
    open.mockRestore()
  })

  it('keeps the create actions off the terminology section', () => {
    renderEditor({ mode: 'create' })
    expect(screen.queryByTestId('row-action-terminology-new-mt-router')).not.toBeInTheDocument()
  })

  it('displays current fuzzy threshold as percentage', () => {
    renderEditor()
    expect(screen.getByRole('spinbutton', { name: /fuzzy match threshold/i })).toHaveValue(75)
  })

  it('toggles MT-on-upload checkbox and calls onUpdateScalars', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor()

    const checkbox = screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })
    expect(checkbox).not.toBeChecked()

    await user.click(checkbox)

    expect(onUpdateScalars).toHaveBeenCalledWith({ mt_on_upload: true })
  })

  it('TM-on-upload checkbox toggles independently of MT-on-upload', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor()

    const mtCheckbox = screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })
    const tmOnUpload = screen.getByRole('checkbox', { name: /translation memory.*start or upload/i })

    // Both start unchecked. TM is NOT disabled — independent.
    expect(mtCheckbox).not.toBeChecked()
    expect(tmOnUpload).not.toBeChecked()
    expect(tmOnUpload).not.toBeDisabled()

    // Toggle TM on while MT-on-upload stays off — single-key PATCH, no cascade.
    await user.click(tmOnUpload)
    expect(onUpdateScalars).toHaveBeenCalledWith({ tm_on_upload: true })
    expect(onUpdateScalars).not.toHaveBeenCalledWith({ mt_on_upload: expect.anything() })
  })

  it('disabling MT-on-upload does NOT cascade tm_on_upload', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor({
      values: { ...baseValues, mtOnUpload: true, tmOnUpload: true },
    })

    const mtCheckbox = screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })
    expect(mtCheckbox).toBeChecked()

    await user.click(mtCheckbox)

    // Single-key PATCH only. The (mt=false, tm=true) combo is valid.
    expect(onUpdateScalars).toHaveBeenCalledWith({ mt_on_upload: false })
    expect(onUpdateScalars).not.toHaveBeenCalledWith({ mt_on_upload: false, tm_on_upload: false })
  })

  // Pins that there is no predict-on-upload toggle: QP+APE runs as a QE+APE
  // workflow step, not an upload phase.
  it('has no predict-on-upload toggle', () => {
    renderEditor({ values: { ...baseValues, mtOnUpload: true } })
    expect(screen.queryByRole('checkbox', { name: /predict on upload/i })).toBeNull()
  })

  it('changes QA profile and calls onUpdateScalars', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor()

    const qaSelect = screen.getByTestId('qa-profile-select')
    await user.selectOptions(qaSelect, '2')

    expect(onUpdateScalars).toHaveBeenCalledWith({ qa_profile_id: 2 })
  })

  it('clears QA profile when "None" selected', async () => {
    const user = userEvent.setup()
    const { onUpdateScalars } = renderEditor({
      values: { ...baseValues, qaProfileId: 1 },
    })

    const qaSelect = screen.getByTestId('qa-profile-select')
    await user.selectOptions(qaSelect, '')

    expect(onUpdateScalars).toHaveBeenCalledWith({ qa_profile_id: null })
  })

  it('in create mode calls onChange instead of onUpdateScalars', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()

    renderEditor({
      mode: 'create',
      values: { ...baseValues, mtRouterId: 1 },
      onChange,
      onUpdateScalars: undefined,
    })

    const checkbox = screen.getByRole('checkbox', { name: /machine translation.*start or upload/i })
    await user.click(checkbox)

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ mtOnUpload: true }),
    )
  })

  it('flags a missing TM and MT router beside their own card titles, text in the tooltip only', () => {
    renderEditor({
      sourceLanguage: 'en',
      targetLanguages: ['es', 'fr'],
      values: { ...baseValues, tmAssignments: [], mtRouterId: null },
    })

    const tmCard = screen.getByTestId('tm-section')
    const mtCard = screen.getByTestId('mt-section')
    expect(within(tmCard).getByTestId('tm-coverage-warning')).toBeInTheDocument()
    expect(tmCard).toHaveTextContent('No translation memory for en→es, en→fr.')
    expect(tmCard).not.toHaveTextContent('No MT router.')
    expect(within(mtCard).getByTestId('mt-coverage-warning')).toBeInTheDocument()
    expect(mtCard).toHaveTextContent('No MT router.')
    expect(mtCard).not.toHaveTextContent(/no translation memory/i)
    expect(screen.queryByText(/missing configuration/i)).not.toBeInTheDocument()
  })

  it('shows no warning icons when the TMs cover every language and there is an MT router', () => {
    renderEditor({
      sourceLanguage: 'en',
      targetLanguages: ['de'],
      tmCollections: [{ id: 1, name: 'Default TM', source_language: 'en', target_languages: ['de'] }],
      values: { ...baseValues, tmAssignments: [{ id: 1, tmId: 1, mode: 'read_write', priority: 0, penaltyPct: 0 }], mtRouterId: 1 },
    })

    expect(screen.queryByTestId('tm-coverage-warning')).not.toBeInTheDocument()
    expect(screen.queryByTestId('mt-coverage-warning')).not.toBeInTheDocument()
  })

  it('hides missing configuration warning in create mode', () => {
    renderEditor({
      mode: 'create',
      sourceLanguage: 'en',
      targetLanguages: ['es', 'fr'],
      values: { ...baseValues, tmAssignments: [], mtRouterId: null },
      onChange: vi.fn(),
      onUpdateScalars: undefined,
    })

    expect(screen.queryByTestId('tm-coverage-warning')).not.toBeInTheDocument()
    expect(screen.queryByTestId('mt-coverage-warning')).not.toBeInTheDocument()
  })

  describe('MT router language coverage', () => {
    const esOnly = { id: 1, name: 'Spanish router', rules: [{ target_lang: 'es' }] }

    it('names the target languages the chosen router has no engine for, beside the picker', () => {
      renderEditor({
        sourceLanguage: 'en',
        targetLanguages: ['es', 'de', 'fr'],
        mtRouters: [esOnly],
        values: { ...baseValues, mtRouterId: 1 },
      })
      const warning = within(screen.getByTestId('mt-section')).getByTestId('mt-router-coverage-warning')
      expect(warning.parentElement).toContainElement(screen.getByTestId('mt-router-select'))
      expect(screen.getByTestId('mt-section')).toHaveTextContent(
        'This router has no engine for DE, FR. Files in these languages are not machine translated.',
      )
    })

    it('stays quiet when the router has a default engine for every other language', () => {
      renderEditor({
        sourceLanguage: 'en',
        targetLanguages: ['es', 'de', 'fr'],
        mtRouters: [{ ...esOnly, default_ai_engine_id: 7 }],
        values: { ...baseValues, mtRouterId: 1 },
      })
      expect(screen.queryByTestId('mt-router-coverage-warning')).not.toBeInTheDocument()
    })

    it('shows on the New project form once the router is picked', () => {
      renderEditor({
        mode: 'create',
        sourceLanguage: 'en',
        targetLanguages: ['es', 'de'],
        mtRouters: [esOnly],
        onChange: vi.fn(),
        onUpdateScalars: undefined,
      })
      expect(screen.queryByTestId('mt-router-coverage-warning')).not.toBeInTheDocument()

      fireEvent.change(screen.getByTestId('mt-router-select'), { target: { value: '1' } })

      expect(screen.getByTestId('mt-router-coverage-warning')).toBeInTheDocument()
      expect(screen.getByTestId('mt-section')).toHaveTextContent(
        'This router has no engine for DE. Files in this language are not machine translated.',
      )
    })

    // The template page has default targets but no project languages: the
    // router is checked against them, and the other setup gaps stay off.
    it('checks routerCoverageLanguages without switching on the other gap warnings', () => {
      renderEditor({
        routerCoverageLanguages: ['es', 'de'],
        mtRouters: [esOnly],
        values: { ...baseValues, mtRouterId: 1, tmAssignments: [] },
      })
      expect(screen.getByTestId('mt-router-coverage-warning')).toBeInTheDocument()
      expect(screen.queryByTestId('tm-coverage-warning')).not.toBeInTheDocument()
      expect(screen.queryByTestId('mt-coverage-warning')).not.toBeInTheDocument()
    })
  })

  it('offers 100% as the exact-match fuzzy threshold', () => {
    renderEditor({
      values: { ...baseValues, fuzzyThreshold: 1.0 },
    })
    expect(screen.getByRole('spinbutton', { name: /fuzzy match threshold/i })).toHaveValue(100)
  })

  it('edit mode: accepts and commits any fuzzy percentage from 0 to 100', async () => {
    const { onUpdateScalars } = renderEditor()
    const percentage = screen.getByRole('spinbutton', { name: /fuzzy match threshold/i })

    fireEvent.change(percentage, { target: { value: '37' } })
    fireEvent.blur(percentage)
    expect(onUpdateScalars).toHaveBeenCalledWith({ fuzzy_threshold: 0.37 })

    // Sent once the 37 save has finished: one save per field at a time.
    fireEvent.change(percentage, { target: { value: '0' } })
    fireEvent.blur(percentage)
    await waitFor(() => expect(onUpdateScalars).toHaveBeenCalledWith({ fuzzy_threshold: 0 }))
  })

  it('edit mode: commits the penalty percentages on blur', () => {
    const { onUpdateScalars } = renderEditor()

    const sublanguage = screen.getByRole('spinbutton', { name: /language-variant mismatch penalty/i })
    fireEvent.change(sublanguage, { target: { value: '5' } })
    fireEvent.blur(sublanguage)
    // Sent as a percentage, not a fraction — the column stores 0-100.
    expect(onUpdateScalars).toHaveBeenCalledWith({ sublanguage_penalty_pct: 5 })

    const nearExact = screen.getByRole('spinbutton', { name: /near-exact penalty/i })
    fireEvent.change(nearExact, { target: { value: '0' } })
    fireEvent.blur(nearExact)
    expect(onUpdateScalars).toHaveBeenCalledWith({ near_exact_penalty_pct: 0 })
  })

  it('shows the stored penalties', () => {
    renderEditor({ values: { ...baseValues, sublanguagePenaltyPct: 7, nearExactPenaltyPct: 3 } })
    expect(screen.getByRole('spinbutton', { name: /language-variant mismatch penalty/i })).toHaveValue(7)
    expect(screen.getByRole('spinbutton', { name: /near-exact penalty/i })).toHaveValue(3)
  })

  // 0 is a meaningful setting ("these locales are equivalent"), so it must
  // survive the commit rather than being swallowed as falsy.
  it('edit mode: a zero penalty is committed, not dropped', () => {
    const { onUpdateScalars } = renderEditor({
      values: { ...baseValues, sublanguagePenaltyPct: 5 },
    })
    const sublanguage = screen.getByRole('spinbutton', { name: /language-variant mismatch penalty/i })
    fireEvent.change(sublanguage, { target: { value: '0' } })
    fireEvent.blur(sublanguage)
    expect(onUpdateScalars).toHaveBeenCalledWith({ sublanguage_penalty_pct: 0 })
  })

  it('create mode: toggling MT-on-upload after adding TM does not clobber TM', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()

    renderEditor({
      mode: 'create',
      values: { ...baseValues, mtRouterId: 1 },
      onChange,
      onUpdateScalars: undefined,
    })

    // 1. Choose where the TMs come from, then add a TM draft
    await user.selectOptions(screen.getByTestId('tm-profile-select'), 'custom')
    await user.click(screen.getByTestId('draft-translation-memory'))

    // 2. Toggle MT-on-upload checkbox
    await user.click(screen.getByRole('checkbox', { name: /machine translation.*start or upload/i }))

    // The last onChange call should contain BOTH the TM assignment AND the toggled flag.
    // Bug: scalar change would spread stale `values` and clobber TM.
    const lastCall = onChange.mock.calls[onChange.mock.calls.length - 1][0]
    expect(lastCall.tmAssignments.length).toBe(1)
    expect(lastCall.mtOnUpload).toBe(true)
  })

  // ─── Inline-create integration (SettingsEditor owns dialog state) ──────

  it('inline-create TM: clicking "Create New" in TM AssignmentSection opens the TM dialog', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(screen.queryByTestId('create-tm-dialog')).not.toBeInTheDocument()

    await user.click(screen.getByTestId('create-new-translation-memory'))

    expect(screen.getByTestId('create-tm-dialog')).toBeInTheDocument()
  })

  it('inline-create TM: firing onCreated resolves the promise with { id }', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(screen.getByTestId('create-new-translation-memory'))
    expect(screen.getByTestId('create-tm-dialog')).toBeInTheDocument()

    // Capture the pending promise from window
    const pendingPromise = (window as CreateNewWindow).__lastCreateNewPromise
    expect(pendingPromise).toBeDefined()

    // Fire onCreated via the mock dialog
    await user.click(screen.getByTestId('tm-dialog-created'))

    const result = await pendingPromise
    expect(result).toEqual({ id: 99 })

    // Dialog should be closed now
    expect(screen.queryByTestId('create-tm-dialog')).not.toBeInTheDocument()
  })

  it('inline-create TM: firing onClose resolves the promise with null (cancelled)', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(screen.getByTestId('create-new-translation-memory'))
    const pendingPromise = (window as CreateNewWindow).__lastCreateNewPromise

    await user.click(screen.getByTestId('tm-dialog-close'))

    const result = await pendingPromise
    expect(result).toBeNull()
    expect(screen.queryByTestId('create-tm-dialog')).not.toBeInTheDocument()
  })

  // Re-entry guard: double-click or rapid re-open while the first dialog is
  // still in flight should not leak the first promise. The second call
  // resolves immediately with null.
  it('inline-create TM: second call while dialog already open resolves immediately with null (re-entry guard)', async () => {
    const user = userEvent.setup()
    renderEditor()

    // First click opens the dialog
    await user.click(screen.getByTestId('create-new-translation-memory'))
    const firstPromise = (window as CreateNewWindow).__lastCreateNewPromise
    expect(screen.getByTestId('create-tm-dialog')).toBeInTheDocument()

    // Second click while dialog still open — should resolve immediately with null
    await user.click(screen.getByTestId('create-new-translation-memory'))
    const secondPromise = (window as CreateNewWindow).__lastCreateNewPromise
    expect(secondPromise).not.toBe(firstPromise)

    const secondResult = await secondPromise
    expect(secondResult).toBeNull()

    // First dialog still open, first promise still pending
    expect(screen.getByTestId('create-tm-dialog')).toBeInTheDocument()

    // Fire onCreated for the first dialog — the first promise resolves
    await user.click(screen.getByTestId('tm-dialog-created'))
    const firstResult = await firstPromise
    expect(firstResult).toEqual({ id: 99 })
  })

  it('inline-create Term base: clicking "Create New" in Terminology AssignmentSection opens the TB dialog', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(screen.queryByTestId('create-tb-dialog')).not.toBeInTheDocument()

    await user.click(screen.getByTestId('create-new-terminology'))

    expect(screen.getByTestId('create-tb-dialog')).toBeInTheDocument()
  })

  it('inline-create TM section has the callback but MT section does not (MT is excluded by design)', () => {
    renderEditor()

    // TM has the button (onCreateNewResource was passed)
    expect(screen.getByTestId('create-new-translation-memory')).toBeInTheDocument()
    // Term base has the button
    expect(screen.getByTestId('create-new-terminology')).toBeInTheDocument()
    // MT does NOT — inline-create is not supported for MT engines (too complex)
    expect(screen.queryByTestId('create-new-machine-translation')).not.toBeInTheDocument()
  })
})

describe('SettingsEditor — followed TM profile', () => {
  const tmProfiles = [{ id: 7, name: 'Acme' }]
  // A followed profile's settings start collapsed; the toggle reveals them.
  const expandProfileSettings = () => {
    expect(screen.queryByTestId('tm-profile-settings')).toBeNull()
    fireEvent.click(screen.getByTestId('tm-profile-settings-toggle'))
    expect(screen.getByTestId('tm-profile-settings')).toBeInTheDocument()
  }

  it('collapses the profile settings by default and summarises them on the toggle', () => {
    renderEditor({
      tmProfiles,
      values: { ...baseValues, fuzzyThreshold: 0.6, tmProfileId: 7, tmProfileName: 'Acme', tmProfileUsageCount: 2,
        tmAssignments: [{ id: 1, tmId: 1, mode: 'read', priority: 0 }] },
    })
    expect(screen.queryByTestId('tm-scoring')).toBeNull()
    expect(screen.getByTestId('tm-profile-settings-toggle')).toHaveTextContent('Show profile settings — 1 TM · 60% threshold')
    expandProfileSettings()
    expect(screen.getByTestId('tm-scoring')).toBeInTheDocument()
    expect(screen.getByTestId('tm-profile-settings-toggle')).toHaveTextContent(/Hide profile settings/)
  })

  it('custom (unlinked) settings are never collapsed', () => {
    renderEditor({ tmProfiles })
    expect(screen.queryByTestId('tm-profile-settings-toggle')).toBeNull()
    expect(screen.getByTestId('tm-scoring')).toBeInTheDocument()
  })

  it('edits the profile in place when nothing else follows it, and routes scoring commits to the profile', async () => {
    const api = await import('../api')
    const { onUpdateScalars } = renderEditor({
      tmProfiles,
      values: { ...baseValues, tmProfileId: 7, tmProfileName: 'Acme', tmProfileUsageCount: 1 },
    })
    expect(screen.getByTestId('tm-profile-editable')).toBeInTheDocument()
    expandProfileSettings()
    expect(screen.getByTestId('tm-scoring')).not.toHaveAttribute('data-readonly', 'true')
    const fuzzy = screen.getByLabelText('Fuzzy match threshold')
    expect(fuzzy).toBeEnabled()
    fireEvent.change(fuzzy, { target: { value: '60' } })
    fireEvent.blur(fuzzy, { target: { value: '60' } })
    await waitFor(() => expect(api.updateTMProfile).toHaveBeenCalledWith(7, { fuzzy_threshold: 0.6 }, { maxFollowers: 1 }))
    expect(onUpdateScalars).not.toHaveBeenCalled()
  })

  it('a refused profile save puts the stored value back, and the same value can be saved again', async () => {
    const api = await import('../api')
    vi.mocked(api.updateTMProfile).mockClear().mockRejectedValueOnce(new Error('server unavailable'))
    renderEditor({ tmProfiles, values: { ...baseValues, tmProfileId: 7, tmProfileName: 'Acme', tmProfileUsageCount: 1 } })
    expandProfileSettings()
    const field = screen.getByLabelText('Near-exact penalty')
    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(field).toHaveValue(1))

    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(api.updateTMProfile).toHaveBeenCalledTimes(2))
    expect(api.updateTMProfile).toHaveBeenLastCalledWith(7, { near_exact_penalty_pct: 3 }, { maxFollowers: 1 })
  })

  it('shows the profile read-only once another project or template follows it', () => {
    renderEditor({
      tmProfiles,
      values: { ...baseValues, tmProfileId: 7, tmProfileName: 'Acme', tmProfileUsageCount: 2 },
    })
    expect(screen.queryByTestId('tm-profile-editable')).toBeNull()
    expandProfileSettings()
    expect(screen.getByTestId('tm-scoring')).toHaveAttribute('data-readonly', 'true')
    expect(screen.getByLabelText('Fuzzy match threshold')).toBeDisabled()
  })

  it('treats an unknown follower count as shared', () => {
    renderEditor({ tmProfiles, values: { ...baseValues, tmProfileId: 7, tmProfileName: 'Acme' } })
    expandProfileSettings()
    expect(screen.getByTestId('tm-scoring')).toHaveAttribute('data-readonly', 'true')
  })
})

// The scoring fields save while typing: each save confirms with one toast that
// the next replaces, and a save the server refuses is undone in the field.
describe('SettingsEditor scoring saves', () => {
  it('confirm with the shared scoring toast id', async () => {
    const { toast } = await import('sonner')
    const { onUpdateScalars } = renderEditor()
    const field = screen.getByLabelText('Near-exact penalty')
    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(onUpdateScalars).toHaveBeenCalledWith({ near_exact_penalty_pct: 3 }))
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith(expect.any(String), { id: 'tm-scoring-saved' }))
  })

  // The field saves after a pause while it still has focus: disabling it for
  // that save would drop the focus, and the keys typed next with it.
  it('a scoring field stays usable while its own save is on its way', async () => {
    let finish: () => void = () => {}
    const onUpdateScalars = vi.fn(() => new Promise<void>(r => { finish = r }))
    renderEditor({ onUpdateScalars })
    const field = screen.getByLabelText('Near-exact penalty')
    act(() => field.focus())
    fireEvent.change(field, { target: { value: '3' } })
    await waitFor(() => expect(onUpdateScalars).toHaveBeenCalledWith({ near_exact_penalty_pct: 3 }))
    expect(field).toBeEnabled()
    expect(field).toHaveFocus()
    await act(async () => { finish() })
  })

  it('a refused save puts the stored value back, and the same value can be saved again', async () => {
    const onUpdateScalars = vi.fn().mockRejectedValueOnce(new Error('server unavailable')).mockResolvedValueOnce(undefined)
    renderEditor({ onUpdateScalars })
    const field = screen.getByLabelText('Near-exact penalty')
    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(field).toHaveValue(1))

    fireEvent.change(field, { target: { value: '3' } })
    fireEvent.blur(field)
    await waitFor(() => expect(onUpdateScalars).toHaveBeenCalledTimes(2))
  })
})
