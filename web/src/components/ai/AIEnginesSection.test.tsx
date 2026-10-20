import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AIEnginesSection } from './AIEnginesSection'
import type { EnginePurpose } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAIEngines = vi.fn()
const listAIPrompts = vi.fn()
const listAIVendors = vi.fn()
vi.mock('../../api', () => ({
  listAIEngines: () => listAIEngines(),
  listAIPrompts: () => listAIPrompts(),
  listAIVendors: () => listAIVendors(),
  createAIEngine: vi.fn(), updateAIEngine: vi.fn(), deleteAIEngine: vi.fn(),
}))

const engine = (purpose: EnginePurpose) => ({
  id: 3, tenant_id: 1, name: 'Tuned', purpose,
  created_at: '2026-01-01', updated_at: '2026-01-01',
  rows: [{
    id: 1, source_lang: '*', target_langs: ['*'], system_prompt_id: 5,
    bad_output_action: 'reject', token_id: 1, model: 'gpt-4o', settings: {},
  }],
})

function renderSection(purpose: EnginePurpose) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><AIEnginesSection canWrite purpose={purpose} title="Engines" layout="static" /></MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  listAIPrompts.mockResolvedValue([])
  listAIVendors.mockResolvedValue([])
})

// A failed load must say so: "No AI engines yet" for a server error reads as
// "your engines are gone".
describe('AIEnginesSection when loading fails', () => {
  it.each([
    ['qe_ape' as const, 'AI engines', 'No AI engines yet.'],
    ['mt' as const, 'LLM translation engines', 'No LLM translation engines yet.'],
  ])('a %s list shows the error with a Retry, not its empty state', async (purpose, what, emptyText) => {
    listAIEngines.mockRejectedValueOnce(new Error('server unavailable')).mockResolvedValue([engine(purpose)])
    renderSection(purpose)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(`Couldn't load the ${what}: server unavailable`)
    expect(screen.queryByText(emptyText)).not.toBeInTheDocument()
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('ai-engine-3')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // Prompt and provider names only label the engine rows, so the list still
  // shows without them.
  it('still lists the engines when the prompt and provider names fail to load', async () => {
    listAIEngines.mockResolvedValue([engine('qe_ape')])
    listAIPrompts.mockRejectedValue(new Error('server unavailable'))
    listAIVendors.mockRejectedValue(new Error('server unavailable'))
    renderSection('qe_ape')

    expect(await screen.findByTestId('ai-engine-3')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
