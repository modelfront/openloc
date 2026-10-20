import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { Concept, TermBase } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))

// jsdom measures every element as zero, so the real virtualized table mounts
// no rows. One element per concept is all these tests read.
vi.mock('@/components/ui/virtual-data-table', () => ({
  VirtualDataTable: ({ data, emptyMessage }: { data: Concept[]; emptyMessage?: string }) => (
    data.length === 0
      ? <p>{emptyMessage}</p>
      : <ul>{data.map(c => <li key={c.id} data-testid={`concept-${c.id}`}>{c.definition}</li>)}</ul>
  ),
}))

const mockListTermBases = vi.fn()
const mockListTermBaseLanguages = vi.fn()
const mockListConcepts = vi.fn()

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  listTermBases: (...a: unknown[]) => mockListTermBases(...a),
  listTermBaseLanguages: (...a: unknown[]) => mockListTermBaseLanguages(...a),
  listConcepts: (...a: unknown[]) => mockListConcepts(...a),
  listLanguages: () => Promise.resolve([]),
}))

import TerminologyBrowser from './TerminologyBrowser'

const BASE: TermBase = {
  id: 3, name: 'Glossary', description: '', source_language: 'en', target_languages: ['de'],
  entry_count: 1, languages: ['en', 'de'], created_at: '', updated_at: '',
}
const CONCEPT: Concept = {
  id: 9, definition: 'a greeting', domain: '', created_at: '', updated_at: '',
  created_by: '', updated_by: '', terms: [],
}
const PAGE = { data: [CONCEPT], cursor: 0, has_more: false, total: 1 }

function renderBrowser() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/terminology/3']}>
        <Routes>
          <Route path="/terminology/:terminologyId" element={<TerminologyBrowser />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockListTermBases.mockResolvedValue([BASE])
  mockListTermBaseLanguages.mockResolvedValue(['en', 'de'])
  mockListConcepts.mockResolvedValue(PAGE)
})

afterEach(() => cleanup())

describe('TerminologyBrowser load failures', () => {
  it('a failed concept list shows the error with a Retry that loads the concepts', async () => {
    mockListConcepts.mockRejectedValue(new Error('failed to list concepts'))
    renderBrowser()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the concepts: failed to list concepts")
    expect(screen.queryByText(/No concepts yet/)).toBeNull()

    mockListConcepts.mockResolvedValue(PAGE)
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('concept-9')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('a failed language list, which the concept query waits on, shows the error instead of loading forever', async () => {
    mockListTermBaseLanguages.mockRejectedValue(new Error('failed to list languages'))
    renderBrowser()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the concepts: failed to list languages")
    expect(mockListConcepts).not.toHaveBeenCalled()

    mockListTermBaseLanguages.mockResolvedValue(['en', 'de'])
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('concept-9')).toBeInTheDocument()
  })
})
