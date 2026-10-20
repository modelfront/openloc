import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom'
import { AIPromptsSection } from './AIPromptsSection'
import type { AIPromptWithAdditions } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listAIPrompts = vi.fn<() => Promise<AIPromptWithAdditions[]>>()

vi.mock('../../api', () => ({
  listAIPrompts: () => listAIPrompts(),
  deleteAIPrompt: vi.fn(),
}))

function prompt(overrides: Partial<AIPromptWithAdditions> = {}): AIPromptWithAdditions {
  return {
    id: 7,
    tenant_id: 1,
    name: 'Strict reviewer',
    prompt_text: 'Review the translation.',
    // QE: status + note, no target text — keeps it off the MT-purpose list.
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

// Shows where a click landed: the editor is its own route, so "opened the
// prompt" is an assertion about the URL.
function Here() {
  const loc = useLocation()
  return <div data-testid="url">{loc.pathname + loc.search}</div>
}

function renderList(prompts: AIPromptWithAdditions[], purpose: 'mt' | 'qe_ape' = 'qe_ape') {
  listAIPrompts.mockResolvedValue(prompts)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[purpose === 'mt' ? '/mt/prompts' : '/ai/prompts']}>
        <AIPromptsSection canWrite purpose={purpose} />
        <Routes><Route path="*" element={<Here />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const url = () => screen.getByTestId('url').textContent

beforeEach(() => {
  vi.clearAllMocks()
})

describe('AIPromptsSection — the list', () => {
  it('lists only the prompts of its own purpose, your newest first', async () => {
    renderList([
      prompt({ id: 7, name: 'Strict reviewer', created_at: '2026-09-01T00:00:00Z' }),
      // MT: text out, no status and no comment (flagsToMode).
      prompt({ id: 8, name: 'Literal MT', out_status: false, out_text: true, out_comments: false }),
      prompt({ id: 9, name: 'Newest reviewer', created_at: '2026-09-20T00:00:00Z' }),
      prompt({ id: 10, name: 'Built-in judge', is_default: true, created_at: '2026-08-01T00:00:00Z' }),
    ])

    expect(await screen.findByText('Strict reviewer')).toBeInTheDocument()
    expect(screen.queryByText('Literal MT')).not.toBeInTheDocument()
    // Pins newest-first order for your own prompts: the API returns them
    // oldest-first, which would bury a new one between the older customs and the
    // read-only defaults.
    const rows = screen.getAllByTestId(/^ai-prompt-\d+$/)
    expect(rows.map(r => r.getAttribute('data-testid'))).toEqual([
      'ai-prompt-9', 'ai-prompt-7', 'ai-prompt-10',
    ])
  })

  it('opens a prompt on its own page', async () => {
    renderList([prompt({ id: 7 })])

    await userEvent.click(await screen.findByText('Strict reviewer'))

    expect(url()).toBe('/ai/prompts/7')
  })

  // The MT list and the reviewing list are the same component on different
  // sections, so a row has to link into the section it was opened from — the
  // other one would filter the prompt straight back out.
  it('keeps MT prompts under the MT section', async () => {
    renderList([prompt({ id: 9, name: 'Literal MT', out_status: false, out_text: true, out_comments: false })], 'mt')

    await userEvent.click(await screen.findByText('Literal MT'))

    expect(url()).toBe('/mt/prompts/9')
  })

  it('creates from the section action', async () => {
    renderList([])

    await userEvent.click(await screen.findByTestId('ai-prompt-add'))

    expect(url()).toBe('/ai/prompts/new')
  })

  // Duplicating is how a read-only built-in gets customized — it
  // opens the create page seeded from the source.
  it('duplicates into a pre-filled create page', async () => {
    renderList([prompt({ id: 7, is_default: true })])

    await userEvent.click(await screen.findByTestId('ai-prompt-duplicate'))

    expect(url()).toBe('/ai/prompts/new?from=7')
  })

  it('offers no delete on a built-in', async () => {
    renderList([prompt({ id: 7, is_default: true })])
    await screen.findByTestId('ai-prompt-7')

    expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument()
  })

  it('says so when there is nothing yet', async () => {
    renderList([])

    expect(await screen.findByText('No prompts yet.')).toBeInTheDocument()
  })
})

// A failed load must say so: "No prompts yet" for a server error reads as
// "your prompts are gone".
describe('AIPromptsSection when loading fails', () => {
  it('shows the error with a Retry, not "No prompts yet"', async () => {
    listAIPrompts.mockRejectedValueOnce(new Error('server unavailable'))
    renderList([prompt({ id: 7, name: 'Strict reviewer' })])

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the prompts: server unavailable")
    expect(screen.queryByText('No prompts yet.')).not.toBeInTheDocument()
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Strict reviewer')).toBeInTheDocument()
  })
})
