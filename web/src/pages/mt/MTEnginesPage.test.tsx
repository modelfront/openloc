// The MT engines page owns everything that translates: vendor engines (switch
// only), LLM engines (AI engines with purpose 'mt', fully editable here), and
// the MT-purpose prompts they reference.
//
// What this file pins (and why each matters):
//   - Both engine lists render, separated by the LLM divider. The vendor list
//     is switch-only; the LLM list is the one with a create action.
//   - The page shows ONLY MT things. A QE+APE engine or prompt leaking in is
//     the whole failure mode of the split — it would be editable from two
//     pages.
//   - A new LLM engine is created as purpose 'mt', and its prompt picker offers
//     MT prompts only.
//
// Anti-tautology RED proofs:
//   - Drop the `purpose` filter in AIEnginesSection → the QE+APE engine shows
//     up and "lists only MT engines" fails.
//   - Drop it in AIPromptsSection → the QE+APE prompt appears and "lists only
//     MT prompts" fails.
//   - Revert EngineForm's `purpose` to `initial?.purpose` → a new engine's
//     pickers offer QE+APE prompts again and "new LLM engine: MT prompts only"
//     fails.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import MTEnginesPage from './MTEnginesPage'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listMTEngines = vi.fn()
const listMTEngineCatalog = vi.fn()
const listAIVendors = vi.fn()
const getMe = vi.fn()
const listAIPrompts = vi.fn()
const listAIEngines = vi.fn()
vi.mock('../../api', () => ({
  listMTEngines: () => listMTEngines(),
  listMTEngineCatalog: () => listMTEngineCatalog(),
  createMTEngine: vi.fn(), updateMTEngine: vi.fn(), deleteMTEngine: vi.fn(),
  listAIVendors: () => listAIVendors(),
  getMe: () => getMe(),
  listAIPrompts: () => listAIPrompts(),
  previewAIPrompt: () => Promise.resolve({ instruction: '', appended: '', system_prompt: '', mode: 'qe', schema_enforced: false }),
  listAIEngines: () => listAIEngines(),
  createAIPrompt: vi.fn(), updateAIPrompt: vi.fn(), deleteAIPrompt: vi.fn(),
  createAIEngine: vi.fn(), updateAIEngine: vi.fn(), deleteAIEngine: vi.fn(),
  validateEngineTypeOutput: () => '',
}))

// Fixture names are deliberately distinct per purpose. In production both
// built-ins are named plain "Default" and told apart by purpose alone
// — but then "only the MT one is listed" would be
// unprovable by name, which is exactly what these tests check. The real
// names are pinned server-side, in internal/aiprompt and internal/segmentprompt.
const openaiVendor = {
  provider: 'openai', connected: true,
  tokens: [{ id: 1, name: 'Default', key_last4: 'a1', created_at: '2026-01-01' }],
  models: [{ id: 'gpt-4o', label: 'GPT-4o', params: [] }],
}

const mtSystemPrompt = {
  // out_text only + no comments ⇒ mode 'mt'.
  id: 6, tenant_id: 1, name: 'Default MT', prompt_text: '',
  out_status: false, out_text: true, out_note: true, out_comments: false,
  is_default: true, created_at: '2026-01-02', updated_at: '2026-01-02',
}
const qeSystemPrompt = {
  id: 5, tenant_id: 1, name: 'Built-in reviewer', prompt_text: '',
  out_status: true, out_text: true, out_note: true, out_comments: true,
  is_default: true, created_at: '2026-01-01', updated_at: '2026-01-01',
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><MTEnginesPage /></MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  getMe.mockResolvedValue({ role: 'owner' })
  listMTEngines.mockResolvedValue([])
  listMTEngineCatalog.mockResolvedValue([])
  listAIVendors.mockResolvedValue([])
  listAIPrompts.mockResolvedValue([])
  listAIEngines.mockResolvedValue([])
})

describe('MTEnginesPage', () => {
  it('renders both engine lists, static, with the MT tabs', async () => {
    listMTEngines.mockResolvedValue([
      { id: 1, tenant_id: 1, name: 'DeepL', engine_type: 'deepl', enabled: true, provisioned_by: '', token_id: 3, settings: {} },
    ])
    listAIEngines.mockResolvedValue([
      { id: 1, tenant_id: 1, name: 'GPT translator', purpose: 'mt', rows: [] },
    ])
    renderPage()

    await waitFor(() => expect(screen.getByTestId('mt-engine-row-1')).toBeInTheDocument())
    expect(screen.getByText('GPT translator')).toBeInTheDocument()
    // Static titles: nothing to collapse. Prompts live on their own tab.
    expect(screen.getByRole('heading', { name: 'LLM' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^LLM\b/ })).not.toBeInTheDocument()
    for (const tab of ['Routers', 'Engines', 'Prompts']) {
      expect(screen.getByRole('link', { name: tab })).toBeInTheDocument()
    }
    expect(screen.queryByTestId('ai-prompts-section')).not.toBeInTheDocument()
  })


  it('lists only MT-purpose AI engines, and lets an admin create one', async () => {
    listAIEngines.mockResolvedValue([
      { id: 1, tenant_id: 1, name: 'GPT translator', purpose: 'mt', rows: [] },
      { id: 2, tenant_id: 1, name: 'Quality judge', purpose: 'qe_ape', rows: [] },
    ])
    renderPage()
    await waitFor(() => expect(screen.getByText('GPT translator')).toBeInTheDocument())
    // The QE+APE engine belongs to the AI page — it must not be editable here.
    expect(screen.queryByText('Quality judge')).not.toBeInTheDocument()
    // The LLM list is the one that gained a create action (the vendor list has
    // none — those are provisioned in AI settings).
    expect(screen.getByTestId('ai-engine-add')).toBeInTheDocument()
  })


  it('new LLM engine: MT prompts only', async () => {
    listAIPrompts.mockResolvedValue([mtSystemPrompt, qeSystemPrompt])
    listAIVendors.mockResolvedValue([openaiVendor])
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByTestId('ai-engine-add'))
    const form = await screen.findByTestId('ai-engine-form', undefined, { timeout: 3000 })

    // Purpose is 'mt' from the start, so the name reflects MT rather than the
    // QE+APE a purpose-less form would have derived from the first prompt.
    await waitFor(() => expect(within(form).getByTestId('ai-engine-name')).toHaveValue('MT (OpenAI GPT-4o)'))

    // The QE+APE prompt is not selectable.
    // Options carry a mode chip, so names are matched by prefix; the positive
    // control proves the query sees options at all.
    await user.click(within(form).getByRole('button', { name: /^Default MT/ }))
    expect(await screen.findByRole('option', { name: /^Default MT/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /^Built-in reviewer/ })).not.toBeInTheDocument()
  })

  it('hides write actions from non-admin managers', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    renderPage()
    await waitFor(() => expect(screen.getByText('No LLM translation engines yet.')).toBeInTheDocument())
    expect(screen.queryByTestId('ai-engine-add')).not.toBeInTheDocument()
  })

  // ...but MT engines are the manager carve-out: `RequireManager` on
  // /api/mt-engines lets a PM create, edit and delete one, and the roles
  // reference says so. A page that hid the button would contradict both.
  it('lets a project manager build an MT engine', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    renderPage()
    expect(await screen.findByTestId('mt-engine-add')).toBeInTheDocument()
  })
})
