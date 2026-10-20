// Pins that the MT prompts tab lists only MT-purpose prompts (a QE+APE prompt
// leaking in would be editable from two pages) and hides writes from non-admins.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import MTPromptsPage from './MTPromptsPage'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listMTEngines = vi.fn()
const listAIVendors = vi.fn()
const getMe = vi.fn()
const listAIPrompts = vi.fn()
const listAIEngines = vi.fn()
vi.mock('../../api', () => ({
  listMTEngines: () => listMTEngines(),
  updateMTEngine: vi.fn(),
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
      <MemoryRouter><MTPromptsPage /></MemoryRouter>
    </QueryClientProvider>,
  )
}

// The prompt section is headerless (the tab names it); waiting for it to
// render is all "opening" means here.
async function openSection() {
  await screen.findByTestId('ai-prompts-section')
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  getMe.mockResolvedValue({ role: 'owner' })
  listMTEngines.mockResolvedValue([])
  listAIVendors.mockResolvedValue([])
  listAIPrompts.mockResolvedValue([])
  listAIEngines.mockResolvedValue([])
})

describe('MTPromptsPage', () => {

  it('lists only MT-purpose system prompts', async () => {
    listAIPrompts.mockResolvedValue([mtSystemPrompt, qeSystemPrompt])
    renderPage()

    await openSection()
    await waitFor(() => expect(screen.getByText('Default MT')).toBeInTheDocument())
    expect(screen.queryByText('Built-in reviewer')).not.toBeInTheDocument()
  })

  it('hides write actions from non-admin managers', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    renderPage()
    await waitFor(() => expect(screen.getByText('No prompts yet.')).toBeInTheDocument())
    expect(screen.queryByTestId('ai-prompt-add')).not.toBeInTheDocument()
  })
})
