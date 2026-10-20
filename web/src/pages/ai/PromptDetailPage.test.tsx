import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import userEvent from '@testing-library/user-event'
import PromptDetailPage from './PromptDetailPage'
import { updateAIPrompt, type AIPromptWithAdditions } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAIPrompts = vi.fn<() => Promise<AIPromptWithAdditions[]>>()
const getMe = vi.fn()

vi.mock('../../api', () => ({
  listAIPrompts: () => listAIPrompts(),
  createAIPrompt: vi.fn(),
  updateAIPrompt: vi.fn(),
  deleteAIPrompt: vi.fn(),
  previewAIPrompt: () => Promise.resolve({ instruction: '', appended: '', system_prompt: '', mode: 'qe', schema_enforced: false }),
  listLanguages: () => Promise.resolve([{ code: 'de', name: 'German' }]),
  getMe: () => getMe(),
}))

function prompt(overrides: Partial<AIPromptWithAdditions> = {}): AIPromptWithAdditions {
  return {
    id: 7,
    tenant_id: 1,
    name: 'Strict reviewer',
    prompt_text: 'Judge strictly.',
    out_status: true,
    out_text: false,
    out_note: true,
    out_comments: false,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    is_default: false,
    language_additions: [],
    segment_context: [],
    ...overrides,
  }
}

function renderAt(path: string, prompts: AIPromptWithAdditions[] = []) {
  listAIPrompts.mockResolvedValue(prompts)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/ai/prompts/:promptId" element={<PromptDetailPage purpose="qe_ape" />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function renderMTAt(path: string, prompts: AIPromptWithAdditions[] = []) {
  listAIPrompts.mockResolvedValue(prompts)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/mt/prompts/:promptId" element={<PromptDetailPage purpose="mt" />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const form = () => screen.findByTestId('ai-prompt-form')
const instructionsText = () => screen.getByTestId('ai-prompt-instructions').textContent ?? ''

beforeEach(() => {
  vi.clearAllMocks()
  getMe.mockResolvedValue({ role: 'owner' })
})

describe('PromptDetailPage', () => {
  it('edits an existing prompt, seeded from its stored text', async () => {
    renderAt('/ai/prompts/7', [prompt()])

    expect(within(await form()).queryByTestId('ai-prompt-name')).not.toBeInTheDocument()
    expect(within(screen.getByRole('navigation', { name: 'Breadcrumb' })).queryByText('QE')).not.toBeInTheDocument()
    expect(within(await form()).getByText('QE')).toBeInTheDocument()
    expect(instructionsText()).toContain('Judge strictly.')
    expect(screen.getByRole('button', { name: /^update$/i })).toBeInTheDocument()
  })

  it.each([
    ['qe', 'quality evaluator', [], 'Quality estimation'],
    ['ape', 'post-editor', [], 'Automatic post-editing'],
    ['qe_ape', 'postedit_approved', [], 'Quality estimation and post-editing'],
    // Names are unique per tenant, so the type's name is suffixed until free.
    ['qe', 'quality evaluator', ['Quality estimation'], 'Quality estimation 2'],
  ] as const)('requires a type, then seeds the %s instructions and a free name', async (mode, text, taken, expectedName) => {
    renderAt('/ai/prompts/new', taken.map((name, i) => prompt({ id: 100 + i, name })))
    const f = await form()
    expect(within(f).queryByTestId('ai-prompt-name')).not.toBeInTheDocument()
    expect(within(f).queryByTestId('ai-prompt-instructions')).not.toBeInTheDocument()
    expect(within(f).queryByTestId('ai-prompt-submit')).not.toBeInTheDocument()
    expect(within(f).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Prompts/ })).toHaveAttribute('href', '/ai/prompts')
    expect(within(f).getByRole('radiogroup', { name: 'What this prompt does' })).toBeInTheDocument()
    expect(within(f).queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-prompt-type-option-mt')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId(`ai-prompt-type-option-${mode}`))
    expect(within(f).getByTestId('ai-prompt-name')).toHaveValue(expectedName)
    expect(instructionsText()).toContain(text)
    expect(within(f).queryByRole('radiogroup', { name: 'What this prompt does' })).not.toBeInTheDocument()
    expect(within(f).queryByTestId(`ai-prompt-type-option-${mode}`)).not.toBeInTheDocument()
    expect(within(f).getByText(mode === 'qe_ape' ? 'QE+APE' : mode.toUpperCase())).toBeInTheDocument()
    expect(within(f).getByTestId('ai-prompt-submit')).toBeEnabled()
  })

  // Duplicating is the customize path for a read-only built-in. The
  // source seeds the form; what gets saved is a new prompt, so the name is
  // suffixed to dodge UNIQUE(tenant_id, name).
  it('seeds a duplicate from ?from= and offers Create, not Update', async () => {
    renderAt('/ai/prompts/new?from=7', [prompt({ is_default: true })])

    const f = await form()
    expect(within(f).getByTestId('ai-prompt-name')).toHaveValue('Strict reviewer (copy)')
    expect(instructionsText()).toContain('Judge strictly.')
    expect(within(f).getByRole('button', { name: /^create$/i })).toBeInTheDocument()
    expect(within(f).queryByRole('button', { name: /^update$/i })).not.toBeInTheDocument()
  })

  it('opens a built-in read-only, with Duplicate as the way forward', async () => {
    renderAt('/ai/prompts/7', [prompt({ is_default: true })])

    const f = await form()
    expect(screen.getByTestId('ai-prompt-instructions')).toHaveAttribute('contenteditable', 'false')
    expect(within(f).queryByRole('button', { name: /^(create|update)$/i })).not.toBeInTheDocument()
    expect(within(f).getByTestId('ai-prompt-duplicate-form')).toBeInTheDocument()
    expect(screen.queryByTestId('rename-prompt')).not.toBeInTheDocument()
  })

  it('renames in the breadcrumb without losing edits or reverting the saved name', async () => {
    const original = prompt()
    renderAt('/ai/prompts/7', [original])
    await form()
    await userEvent.click(screen.getByRole('button', { name: '<target_lang>' }))
    const edited = instructionsText()
    vi.mocked(updateAIPrompt).mockImplementation(async (_id, input) => {
      const saved = { ...original, ...input }
      listAIPrompts.mockResolvedValue([saved])
      return saved
    })
    await userEvent.click(screen.getByTestId('rename-prompt'))
    const rename = screen.getByRole('textbox', { name: 'Rename' })
    await userEvent.clear(rename)
    await userEvent.type(rename, 'Renamed reviewer{Enter}')
    await screen.findByText('Renamed reviewer')
    expect(updateAIPrompt).toHaveBeenLastCalledWith(7, expect.objectContaining({ name: 'Renamed reviewer', prompt_text: original.prompt_text }))
    expect(instructionsText()).toBe(edited)
    await userEvent.click(screen.getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(updateAIPrompt).toHaveBeenCalledTimes(2))
    expect(updateAIPrompt).toHaveBeenLastCalledWith(7, expect.objectContaining({ name: 'Renamed reviewer', prompt_text: expect.stringContaining('<target_lang>') }))
  })

  // A bookmarked link to a prompt someone else deleted. Rendering an empty
  // form there would invite saving a new prompt nobody asked for.
  it('says so when the prompt is gone', async () => {
    renderAt('/ai/prompts/999', [prompt()])

    expect(await screen.findByText(/no longer exists/i)).toBeInTheDocument()
    expect(screen.queryByTestId('ai-prompt-form')).not.toBeInTheDocument()
  })
})

// An MT prompt has one type, so the create page has nothing to ask: the form
// opens straight onto the fields, already named and seeded for MT. The QE+APE
// page's own test pins the opposite, which is what makes this branch easy to
// miss.
describe('PromptDetailPage — MT create', () => {
  it('skips the type step and opens on a named MT draft', async () => {
    renderMTAt('/mt/prompts/new')
    const f = await form()

    // No type step at all — not the cards, not a single option.
    expect(within(f).queryByRole('group', { name: 'Type' })).not.toBeInTheDocument()
    expect(within(f).queryByTestId('prompt-mode-mt')).not.toBeInTheDocument()
    expect(screen.queryByText('What this prompt does')).not.toBeInTheDocument()

    // The fields the QE+APE page hides until a type is picked are here already.
    expect(within(f).getByTestId('ai-prompt-name')).toHaveValue('Machine translation')
    expect(instructionsText()).toContain('translator')
    expect(within(f).getByTestId('ai-prompt-submit')).toBeEnabled()
  })

  it('suffixes the name when the MT default is taken', async () => {
    renderMTAt('/mt/prompts/new', [prompt({ id: 3, name: 'Machine translation', out_status: false, out_text: true })])
    const f = await form()

    expect(within(f).getByTestId('ai-prompt-name')).toHaveValue('Machine translation 2')
  })
})
