import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import AIEnginesPage from './AIEnginesPage'
import { AIEnginesSection } from '../../components/ai/AIEnginesSection'
import { MemoryRouter, Routes, Route } from 'react-router-dom'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAIVendors = vi.fn()
const getMe = vi.fn()
const listAIPrompts = vi.fn()
const listAIEngines = vi.fn()
const listLanguages = vi.fn()
const listCustomers = vi.fn()
const createAIEngine = vi.fn()
vi.mock('../../api', () => ({
  listAIVendors: () => listAIVendors(),
  createVendorToken: vi.fn(), updateVendorToken: vi.fn(), deleteVendorToken: vi.fn(),
  getMe: () => getMe(),
  listAIPrompts: () => listAIPrompts(),
  previewAIPrompt: () => Promise.resolve({ instruction: '', appended: '', system_prompt: '', mode: 'qe', schema_enforced: false }),
  listAIEngines: () => listAIEngines(),
  createAIPrompt: vi.fn(), updateAIPrompt: vi.fn(), deleteAIPrompt: vi.fn(),
  createAIEngine: (input: unknown) => createAIEngine(input), updateAIEngine: vi.fn(), deleteAIEngine: vi.fn(),
  listLanguages: () => listLanguages(),
  listCustomers: () => listCustomers(),
  validateEngineTypeOutput: () => '',
}))

function renderPageAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/ai/engines" element={<AIEnginesPage />} />
          <Route path="/ai/engines/new" element={<AIEnginesPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><MemoryRouter><AIEnginesPage /></MemoryRouter></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear() // reset persisted section open/closed state between tests
  getMe.mockResolvedValue({ role: 'owner' })
  listAIVendors.mockResolvedValue([])
  listAIPrompts.mockResolvedValue([])
  listAIEngines.mockResolvedValue([])
  listLanguages.mockResolvedValue([])
  listCustomers.mockResolvedValue([])
  createAIEngine.mockImplementation(async (input: { name: string }) => ({ id: 99, ...input }))
})

describe('AIEnginesPage', () => {
  // Providers live on /ai-settings (AISettingsPage.test.tsx covers them).
  it('renders only the engines section', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: true, tokens: [], models: [] }])
    renderPage()
    await screen.findByTestId('ai-engines-section')
    expect(screen.queryByText('Providers')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-vendor-openai')).not.toBeInTheDocument()
  })

  it('renders the AI tabs and the AI engines section', async () => {
    renderPage()
    await screen.findByTestId('ai-engines-section')
    for (const tab of ['Routers', 'Engines', 'Prompts']) {
      expect(screen.getByRole('link', { name: tab })).toBeInTheDocument()
    }
    // Prompts live on their own tab.
    expect(screen.queryByTestId('ai-prompts-section')).not.toBeInTheDocument()
  })

  it('shows write actions to admins', async () => {
    getMe.mockResolvedValue({ role: 'owner' })
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await waitFor(() => expect(screen.getByTestId('ai-engine-add')).toBeInTheDocument())
  })


  // Autofilled names: the form knows the provider/purpose, so the user
  // shouldn't have to retype them. (Provider-form autofill is covered in
  // AISettingsPage.test.tsx.)
  describe('name autofill', () => {
    const openaiVendor = {
      provider: 'openai', connected: true,
      tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }],
      models: [
        { id: 'gpt-4o', label: 'GPT-4o', params: [] },
        { id: 'gpt-4o-mini', label: 'GPT-4o mini', params: [] },
      ],
    }

    it('system: name is the prompt mode plus the provider, and follows a prompt swap', async () => {
      listAIPrompts.mockResolvedValue([
        {
          id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
          out_status: true, out_text: true, out_note: true, out_comments: true,
          is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
        },
        {
          // out_text only + no comments ⇒ mode 'mt' ⇒ an MT-purpose system.
          id: 6, tenant_id: 1, name: 'Default MT', prompt_text: '',
          out_status: false, out_text: true, out_note: true, out_comments: false,
          is_default: true, created_at: '2026-01-02', updated_at: '2026-01-02',
        },
        {
          // out_status, no out_text ⇒ mode 'qe'. Still a qe_ape-*purpose*
          // system, which is why the name has to read the mode.
          id: 7, tenant_id: 1, name: 'Judge only', prompt_text: '',
          out_status: true, out_text: false, out_note: true, out_comments: true,
          is_default: false, created_at: '2026-01-03', updated_at: '2026-01-03',
        },
      ])
      listAIVendors.mockResolvedValue([openaiVendor])
      const user = userEvent.setup()
      renderPage()
      await screen.findByTestId('ai-engines-section')
      await user.click(await screen.findByTestId('ai-engine-add'))
      const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

      // The default system prompt is QE+APE-mode, so that's the derived purpose.
      await waitFor(() => expect(within(form).getByTestId('ai-engine-name')).toHaveValue('QE+APE (OpenAI GPT-4o)'))

      // A QE prompt makes a qe_ape-purpose system, but the name says QE — the
      // name carries the prompt's mode, not the coarse purpose.
      // Options AND the trigger are matched by prefix, not exact name: each
      // carries a mode chip, so the accessible name is "<prompt name> <MODE>".
      // The chip is left IN the name deliberately — the mode is information a
      // screen reader should hear.
      await user.click(within(form).getByRole('button', { name: /^Built-in reviewer/ }))
      await user.click(await screen.findByRole('option', { name: /Judge only/ }))
      await waitFor(() => expect(within(form).getByTestId('ai-engine-name')).toHaveValue('QE (OpenAI GPT-4o)'))

      // The MT prompt is not offered at all: this page owns QE+APE engines, so
      // picking it would build an engine that immediately vanishes from the list
      // it was created in. mt/MTEnginesPage.test.tsx covers the MT side.
      await user.click(within(form).getByRole('button', { name: /^Judge only/ }))
      expect(screen.queryByRole('option', { name: /Default MT/ })).not.toBeInTheDocument()
      expect(await screen.findByRole('option', { name: /Built-in reviewer/ })).toBeInTheDocument()

      // Same order as the prompts list page: your own newest first, then the
      // built-ins. A prompt must not move depending on
      // which surface is showing it.
      // Text runs the name into the mode chip, which is why these read oddly.
      expect(screen.getAllByRole('option').map(o => o.textContent)).toEqual([
        'New prompt',           // the pinned create action stays on top
        'Judge onlyQE',         // then your own, newest first
        'Built-in reviewerQE+APE', // then the read-only built-ins
      ])
    })
  })


  it('new AI engine prefills the built-in defaults', async () => {
    listAIPrompts.mockResolvedValue([{
      id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
      out_status: true, out_text: true, out_note: true, out_comments: true,
      is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
    }])
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }], models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [] }] },
    ])
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await user.click(await screen.findByTestId('ai-engine-add'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
    // Token + model prefilled from the only connected vendor (both live
    // on the row), plus the built-in system prompt. A form that
    // prefills a token but leaves "Select…" on the model can't be saved.
    await waitFor(() => expect(within(form).getByTestId('ai-engine-row-0-token')).toHaveTextContent('OpenAI · Default'))
    expect(within(form).getByTestId('ai-engine-row-0-model')).toHaveTextContent('GPT-4o')
    expect(within(form).getByText('Built-in reviewer')).toBeInTheDocument()
    // The prompt opens in a NEW TAB: this form is a dialog full of unsaved
    // engine config, and navigating to the prompt in place would discard it.
    // The href follows the PROMPT's own flags — a QE+APE prompt lives under
    // /ai, and linking by the engine's section could land on a list that
    // filters it out.
    const openPrompt = within(form).getByTestId('ai-engine-row-0-open-prompt')
    expect(openPrompt).toHaveAttribute('href', '/ai/prompts/5')
    expect(openPrompt).toHaveAttribute('target', '_blank')
    expect(openPrompt).toHaveAttribute('rel', expect.stringContaining('noopener'))

    // Nothing fits? Creating is pinned inside the picker, and opens a new tab
    // so the half-filled engine survives the trip. No 'noopener' — it
    // suppresses the window handle, which the shared hook reads as "popup
    // blocked".
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    await user.click(within(form).getByTestId('ai-engine-row-0-system-prompt'))
    await user.click(await screen.findByTestId('ai-engine-row-0-system-prompt-action-new-prompt'))
    expect(open).toHaveBeenCalledWith('/ai/prompts/new', '_blank')
    open.mockRestore()

    // …and the same link in the prompt preview, which is where you land when
    // you open the panel to read the prompt and decide you want to change it.
    await user.click(within(form).getByRole('button', { name: /prompt preview/i }))
    const fromPreview = await within(form).findByTestId('ai-engine-row-0-preview-open-prompt')
    expect(fromPreview).toHaveAttribute('href', '/ai/prompts/5')
    expect(fromPreview).toHaveAttribute('target', '_blank')

    // Advanced holds only the bad-output action, which defaults to Ignore.
    await user.click(within(form).getByRole('button', { name: /advanced/i }))
    expect(within(form).getByText('Ignore (leave unchanged)')).toBeInTheDocument()
  })


  // Model settings collapse behind a toggle so the common case (model defaults)
  // isn't six empty inputs — but a row that HAS overrides must not hide them.
  describe('model settings disclosure', () => {
    const openaiVendor = {
      provider: 'openai', connected: true,
      tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }],
      models: [{
        id: 'gpt-4o', label: 'GPT-4o',
        params: [{ key: 'temperature', label: 'Temperature', type: 'number', description: 'x' }],
      }],
    }

    it('starts collapsed for a new engine', async () => {
      listAIVendors.mockResolvedValue([openaiVendor])
      const user = userEvent.setup()
      renderPage()
      await user.click(await screen.findByTestId('ai-engine-add'))
      const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

      const toggle = within(form).getByTestId('ai-engine-row-0-settings-toggle')
      await waitFor(() => expect(toggle).toHaveAttribute('aria-expanded', 'false'))
      expect(within(form).queryByLabelText(/temperature/i)).not.toBeInTheDocument()

      await user.click(toggle)
      expect(toggle).toHaveAttribute('aria-expanded', 'true')
    })

    it('starts open, and counts the overrides, when the row already has settings', async () => {
      listAIVendors.mockResolvedValue([openaiVendor])
      listAIEngines.mockResolvedValue([{
        id: 3, tenant_id: 1, name: 'Tuned', purpose: 'qe_ape',
        created_at: '2026-01-01', updated_at: '2026-01-01',
        rows: [{
          id: 1, source_lang: '*', target_langs: ['*'], system_prompt_id: 5,
          bad_output_action: 'reject',
          token_id: 1, model: 'gpt-4o', settings: { temperature: 0.3 },
        }],
      }])
      listAIPrompts.mockResolvedValue([{
        id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
        out_status: true, out_text: true, out_note: true, out_comments: true,
        is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
      }])
      const user = userEvent.setup()
      renderPage()
      await screen.findByTestId('ai-engines-section')
      await user.click(await screen.findByText('Tuned'))
      const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

      const toggle = within(form).getByTestId('ai-engine-row-0-settings-toggle')
      expect(toggle).toHaveAttribute('aria-expanded', 'true')
      expect(within(form).getByDisplayValue('0.3')).toBeInTheDocument()

      // Collapsing it leaves a marker that overrides exist.
      await user.click(toggle)
      expect(within(form).getByText('1 set')).toBeInTheDocument()
    })
  })


  // DeepL/Google are translation endpoints projected into mt_engines,
  // not models an AI engine drives — they can't follow the
  // QE+APE output contract and take no prompt. The backend rejects such a save,
  // so the picker must not offer them at all.
  //
  // Anti-tautology RED proof: drop the vendorGroupOf check from tokenFilter in
  // AIEnginesSection → the DeepL token appears and this fails.
  it('AI engine form: MT providers are not offered', async () => {
    listAIVendors.mockResolvedValue([
      {
        provider: 'openai', connected: true,
        tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }],
        models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [] }],
      },
      {
        provider: 'deepl', connected: true,
        tokens: [{ id: 2, name: 'Default', key_last4: 'b2', created_at: '2026-01-01' }],
        models: [],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await user.click(await screen.findByTestId('ai-engine-add'))
    await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(screen.getByTestId('ai-engine-row-0-token'))
    expect(await screen.findByRole('option', { name: /openai/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /deepl/i })).not.toBeInTheDocument()
  })


  // The same rule applies to the PREFILL, not just the picker: an MT vendor is
  // never a valid provider for a QE+APE engine, so a tenant whose only token is
  // DeepL must open the form with nothing selected — not with a token its own
  // picker refuses to show, a blank Model, and a name reading "QE+APE (DeepL)".
  //
  // Anti-tautology RED proof: drop `tokenFilter` from the prefill's vendor
  // search in AIEnginesSection → the name reads "QE+APE (DeepL)" and this fails.
  it('AI engine form: an MT-only tenant prefills no provider', async () => {
    listAIPrompts.mockResolvedValue([
      {
        id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
        out_status: true, out_text: true, out_note: true, out_comments: true,
        is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
      },
    ])
    listAIVendors.mockResolvedValue([
      // Connected but keyless — the realistic shape of the bug: the LLM vendors
      // exist with no token, so the only token in the tenant is the MT one.
      { provider: 'openai', connected: false, tokens: [], models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [] }] },
      { provider: 'claude', connected: false, tokens: [], models: [] },
      {
        provider: 'deepl', connected: true,
        tokens: [{ id: 2, name: 'Default', key_last4: 'b2', created_at: '2026-01-01' }],
        models: [],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await user.click(await screen.findByTestId('ai-engine-add'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    expect(within(form).getByTestId('ai-engine-name')).toHaveValue('New AI engine')
    expect(within(form).queryByText(/deepl/i)).not.toBeInTheDocument()
    expect(within(form).getByTestId('ai-engine-row-0-token')).toHaveTextContent('Select provider…')
  })


  // A QE+APE engine accepts qe, ape and qe_ape prompts, and those behave
  // differently at runtime. With only a name in the picker, a custom prompt gave
  // no way to tell which one it was.
  //
  // Anti-tautology RED proof: drop `meta` from systemOptions in
  // AIEnginesSection → the QE chip disappears and this fails.
  it('AI engine form: the system prompt picker shows each prompt\'s output mode', async () => {
    listAIPrompts.mockResolvedValue([
      {
        id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
        out_status: true, out_text: true, out_note: true, out_comments: true,
        is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
      },
      {
        // out_status, no out_text ⇒ mode 'qe'. Name says nothing about that.
        id: 7, tenant_id: 1, name: 'House style', prompt_text: '',
        out_status: true, out_text: false, out_note: true, out_comments: true,
        is_default: false, created_at: '2026-01-03', updated_at: '2026-01-03',
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await user.click(await screen.findByTestId('ai-engine-add'))
    await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(screen.getByTestId('ai-engine-row-0-system-prompt'))
    const qeOption = await screen.findByRole('option', { name: /House style/ })
    expect(within(qeOption).getByText('QE')).toBeInTheDocument()
    expect(
      within(await screen.findByRole('option', { name: /Built-in reviewer/ })).getByText('QE+APE'),
    ).toBeInTheDocument()
  })


  it('AI engine form: Esc cancels', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await user.click(await screen.findByTestId('ai-engine-add'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
    fireEvent.keyDown(form, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('ai-engine-form')).not.toBeInTheDocument())
  })

  // Anti-tautology RED proof: drop `setShowFull(false)` from toggleAdvanced →
  // both panels stay open and this fails.
  it('Advanced and Prompt preview start closed and only one opens at a time', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-engine-add'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
    const advanced = within(form).getByRole('button', { name: /advanced/i })
    const preview = within(form).getByRole('button', { name: /prompt preview/i })
    expect(advanced).toHaveAttribute('aria-expanded', 'false')
    expect(preview).toHaveAttribute('aria-expanded', 'false')
    await user.click(preview)
    expect(preview).toHaveAttribute('aria-expanded', 'true')
    await user.click(advanced)
    expect(advanced).toHaveAttribute('aria-expanded', 'true')
    expect(preview).toHaveAttribute('aria-expanded', 'false')
    await user.click(preview)
    expect(preview).toHaveAttribute('aria-expanded', 'true')
    expect(advanced).toHaveAttribute('aria-expanded', 'false')
  })

  // Anti-tautology RED proof: drop `!isDirty` from the submit's disabled
  // expression → Update is enabled before any change and this fails.
  it('Update is disabled until something changed', async () => {
    listAIEngines.mockResolvedValue([{
      id: 3, tenant_id: 1, name: 'Tuned', purpose: 'qe_ape',
      created_at: '2026-01-01', updated_at: '2026-01-01',
      rows: [{ id: 1, source_lang: '*', target_langs: ['*'], system_prompt_id: 5, bad_output_action: 'reject', token_id: 1, model: 'gpt-4o', settings: {} }],
    }])
    listAIPrompts.mockResolvedValue([{
      id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
      out_status: true, out_text: true, out_note: true, out_comments: true,
      is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
    }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByText('Tuned'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
    const submit = within(form).getByRole('button', { name: /^update$/i })
    expect(submit).toBeDisabled()
    await user.clear(within(form).getByTestId('ai-engine-name'))
    await user.type(within(form).getByTestId('ai-engine-name'), 'Tuned v2')
    expect(submit).toBeEnabled()
  })

  it('hides write actions from non-admin managers', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    renderPage()
    await screen.findByTestId('ai-engines-section')
    await waitFor(() => expect(screen.getByText('No AI engines yet.')).toBeInTheDocument())
    expect(screen.queryByTestId('ai-engine-add')).not.toBeInTheDocument()
  })
})

// /ai/engines/new is what a router's engine picker links to. Landing on the
// list with a button to find would waste the trip the link exists to save.
describe('AIEnginesPage — /new', () => {
  it('opens the create dialog straight away', async () => {
    listAIVendors.mockResolvedValue([])
    renderPageAt('/ai/engines/new')

    expect(await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })).toBeInTheDocument()
  })

  it('stays a plain list on the ordinary route', async () => {
    listAIVendors.mockResolvedValue([])
    renderPageAt('/ai/engines')

    await screen.findByTestId('ai-engines-section')
    expect(screen.queryByTestId('ai-engine-form')).not.toBeInTheDocument()
  })

  // Closing has to put the URL back, or the route still says /new and
  // reopening the dialog from the page's own button would be a no-op.
  it('returns to the list when the dialog is dismissed', async () => {
    listAIVendors.mockResolvedValue([])
    const user = userEvent.setup()
    renderPageAt('/ai/engines/new')
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(within(form).getByRole('button', { name: /^cancel$/i }))

    await waitFor(() => expect(screen.queryByTestId('ai-engine-form')).not.toBeInTheDocument())
    expect(screen.getByTestId('ai-engines-section')).toBeInTheDocument()
  })
})

// One press outside dismisses the topmost layer and only that one. A picker
// inside the form portals its popup to the document body, so the same press is
// "outside" for the picker AND for the form — taking both would close an
// engine somebody was still configuring.
describe('AIEnginesPage — one press, one layer', () => {
  const backdrop = () => document.querySelector('[data-slot="dialog-overlay"]') as HTMLElement

  beforeEach(() => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }], models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [] }] },
    ])
  })

  it('closes only the picker while one is open', async () => {
    const user = userEvent.setup()
    renderPageAt('/ai/engines/new')
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(within(form).getByTestId('ai-engine-row-0-token'))
    expect(await screen.findByPlaceholderText('Search…')).toBeInTheDocument()

    await user.click(backdrop())

    expect(screen.queryByPlaceholderText('Search…')).not.toBeInTheDocument()
    expect(screen.getByTestId('ai-engine-form')).toBeInTheDocument()
  })

  it('closes the form when nothing is stacked on it', async () => {
    const user = userEvent.setup()
    renderPageAt('/ai/engines/new')
    await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(backdrop())

    await waitFor(() => expect(screen.queryByTestId('ai-engine-form')).not.toBeInTheDocument())
  })

  // The picker's press must not leave the count stuck above zero, or the form
  // becomes undismissable from then on.
  it('still closes on the press after the picker is dismissed', async () => {
    const user = userEvent.setup()
    renderPageAt('/ai/engines/new')
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    await user.click(within(form).getByTestId('ai-engine-row-0-token'))
    await screen.findByPlaceholderText('Search…')
    await user.click(backdrop())
    expect(screen.getByTestId('ai-engine-form')).toBeInTheDocument()

    await user.click(backdrop())

    await waitFor(() => expect(screen.queryByTestId('ai-engine-form')).not.toBeInTheDocument())
  })

  // An extension token runs the extension's own review: no prompt, no model
  // to pick, no settings, and the row's model is the extension id.
  describe('extension engines', () => {
    const qeApePrompt = {
      id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
      out_status: true, out_text: true, out_note: true, out_comments: true,
      is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
    }
    const mtPrompt = {
      id: 6, tenant_id: 1, name: 'Default MT', prompt_text: '',
      out_status: false, out_text: true, out_note: true, out_comments: false,
      is_default: true, created_at: '2026-01-02', updated_at: '2026-01-02',
    }
    const openaiVendor = {
      provider: 'openai', display_name: 'OpenAI', connected: true,
      tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }],
      models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [{ key: 'temperature', label: 'Temperature', type: 'number', description: 'x' }] }],
    }
    const extVendor = {
      provider: 'ext:mockqe', display_name: 'Mock QE', connected: true,
      tokens: [{ id: 3, name: 'Default', key_last4: 'c3', created_at: '2026-01-01' }],
      models: [],
      extension: { id: 'mockqe', version: '1.0.0', capabilities: ['qe', 'ape'], settings: [], allowed_hosts: [] },
    }

    it('creates a QE+APE engine on an extension key with no prompt or model', async () => {
      listAIPrompts.mockResolvedValue([qeApePrompt])
      listAIVendors.mockResolvedValue([openaiVendor, extVendor])
      const user = userEvent.setup()
      renderPage()
      await user.click(await screen.findByTestId('ai-engine-add'))
      const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
      // The LLM is preferred for the prefill; the extension is a pick away.
      await waitFor(() => expect(within(form).getByTestId('ai-engine-row-0-token')).toHaveTextContent('OpenAI · Default'))

      await user.click(within(form).getByTestId('ai-engine-row-0-token'))
      await user.click(await screen.findByRole('option', { name: /Mock QE · Default/ }))

      expect(within(form).queryByTestId('ai-engine-row-0-model')).not.toBeInTheDocument()
      expect(within(form).queryByTestId('ai-engine-row-0-system-prompt')).not.toBeInTheDocument()
      expect(within(form).queryByTestId('ai-engine-row-0-settings-toggle')).not.toBeInTheDocument()
      expect(within(form).getByTestId('ai-engine-row-0-extension-note')).toHaveTextContent('Mock QE')
      // Named by the extension's display name and the mode its capabilities give it.
      await waitFor(() => expect(within(form).getByTestId('ai-engine-name')).toHaveValue('QE+APE (Mock QE)'))

      await user.click(within(form).getByTestId('ai-engine-submit'))
      await waitFor(() => expect(createAIEngine).toHaveBeenCalled())
      expect(createAIEngine.mock.calls[0][0]).toMatchObject({
        name: 'QE+APE (Mock QE)',
        purpose: 'qe_ape',
        rows: [{
          source_lang: '*', target_langs: ['*'], token_id: 3, model: 'mockqe', settings: {},
          system_prompt_id: null, bad_output_action: null,
        }],
      })
    })

    // An extension can't machine-translate, so an MT engine never offers it.
    //
    // Anti-tautology RED proof: drop the `purpose !== 'mt'` check from
    // tokenFilter in AIEnginesSection → the Mock QE option appears and this fails.
    it('is not offered to an MT engine', async () => {
      listAIPrompts.mockResolvedValue([mtPrompt])
      listAIVendors.mockResolvedValue([openaiVendor, extVendor])
      const user = userEvent.setup()
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter><AIEnginesSection canWrite purpose="mt" title="LLM engines" layout="static" /></MemoryRouter>
        </QueryClientProvider>,
      )
      await user.click(await screen.findByTestId('ai-engine-add'))
      await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
      await user.click(screen.getByTestId('ai-engine-row-0-token'))
      expect(await screen.findByRole('option', { name: /OpenAI/ })).toBeInTheDocument()
      expect(screen.queryByRole('option', { name: /Mock QE/ })).not.toBeInTheDocument()
    })

    // An extension that can neither estimate quality nor post-edit has nothing
    // to do in a QE+APE engine.
    it('is not offered when the extension cannot review translations', async () => {
      listAIPrompts.mockResolvedValue([qeApePrompt])
      listAIVendors.mockResolvedValue([openaiVendor, { ...extVendor, extension: { ...extVendor.extension, capabilities: [] } }])
      const user = userEvent.setup()
      renderPage()
      await user.click(await screen.findByTestId('ai-engine-add'))
      await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })
      await user.click(screen.getByTestId('ai-engine-row-0-token'))
      expect(await screen.findByRole('option', { name: /OpenAI/ })).toBeInTheDocument()
      expect(screen.queryByRole('option', { name: /Mock QE/ })).not.toBeInTheDocument()
    })

    it('names an extension engine by the extension in the list', async () => {
      listAIVendors.mockResolvedValue([extVendor])
      listAIEngines.mockResolvedValue([{
        id: 4, tenant_id: 1, name: 'Reviewer', purpose: 'qe_ape', enabled: true, unusable: '',
        created_at: '2026-01-01', updated_at: '2026-01-01',
        rows: [{ id: 1, source_lang: '*', target_langs: ['*'], system_prompt_id: null, token_id: 3, model: 'mockqe', settings: {} }],
      }])
      renderPage()
      const row = await screen.findByTestId('ai-engine-4')
      await waitFor(() => expect(within(row).getByText('Mock QE')).toBeInTheDocument())
      expect(within(row).getByText('QE+APE')).toBeInTheDocument()
    })
  })
})
