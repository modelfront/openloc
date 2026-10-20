import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import AIPromptsPage from './AIPromptsPage'
import { MemoryRouter } from 'react-router-dom'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAIVendors = vi.fn()
const getMe = vi.fn()
const listAIPrompts = vi.fn()
const listAIEngines = vi.fn()
const listLanguages = vi.fn()
const listCustomers = vi.fn()
vi.mock('../../api', () => ({
  listAIVendors: () => listAIVendors(),
  createVendorToken: vi.fn(), updateVendorToken: vi.fn(), deleteVendorToken: vi.fn(),
  getMe: () => getMe(),
  listAIPrompts: () => listAIPrompts(),
  previewAIPrompt: () => Promise.resolve({ instruction: '', appended: '', system_prompt: '', mode: 'qe', schema_enforced: false }),
  listAIEngines: () => listAIEngines(),
  createAIPrompt: vi.fn(), updateAIPrompt: vi.fn(), deleteAIPrompt: vi.fn(),
  createAIEngine: vi.fn(), updateAIEngine: vi.fn(), deleteAIEngine: vi.fn(),
  listLanguages: () => listLanguages(),
  listCustomers: () => listCustomers(),
  validateEngineTypeOutput: () => '',
}))

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><MemoryRouter><AIPromptsPage /></MemoryRouter></QueryClientProvider>)
}

// The prompt section is headerless (the tab names it); waiting for it to
// render is all "opening" means here.
async function openSection() {
  await screen.findByTestId('ai-prompts-section')
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
})

describe('AIPromptsPage', () => {
  it('renders the AI tabs and the prompt section, not the engines', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('ai-prompts-section')).toBeInTheDocument())
    expect(screen.getByRole('link', { name: 'Engines' })).toHaveAttribute('href', '/ai/engines')
    expect(screen.queryByTestId('ai-engines-section')).not.toBeInTheDocument()
  })

  it('shows write actions to admins', async () => {
    getMe.mockResolvedValue({ role: 'owner' })
    renderPage()
    await openSection()
    await waitFor(() => expect(screen.getByTestId('ai-prompt-add')).toBeInTheDocument())
  })


  it('hides write actions from non-admin managers', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    renderPage()
    await openSection()
    await waitFor(() => expect(screen.getByText('No prompts yet.')).toBeInTheDocument())
    expect(screen.queryByTestId('ai-prompt-add')).not.toBeInTheDocument()
  })
})
