import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import ConceptEditorDialog from './ConceptEditorDialog'
import { useConceptMutations } from './use-concept-mutations'
import type { Concept, ConceptTerm } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api')>()),
  getConcept: vi.fn(),
  listLanguages: vi.fn().mockResolvedValue([{ code: 'en', name: 'English' }]),
  updateConcept: vi.fn().mockResolvedValue(undefined),
  updateConceptTerm: vi.fn().mockResolvedValue(undefined),
  deleteConceptTerm: vi.fn().mockResolvedValue(undefined),
  addConceptTerm: vi.fn(),
}))

const api = await import('../../api')

const concept = (definition: string, terms: ConceptTerm[] = []): Concept => ({
  id: 42, definition, domain: '', created_at: '', updated_at: '', created_by: '', updated_by: '',
  terms,
})

const term = (id: number, language: string, text: string): ConceptTerm => ({
  id, language, text, status: 'approved', part_of_speech: '', notes: '',
  created_at: '', updated_at: '', created_by: '', updated_by: '',
})

describe('ConceptEditorDialog', () => {
  beforeEach(() => { vi.clearAllMocks() })

  // The dialog reads the concept through its own query, so a change made with
  // useConceptMutations (an inline edit in the browser) has to reach that query.
  it('shows a definition changed through the concept mutations while it is open', async () => {
    vi.mocked(api.getConcept).mockResolvedValue(concept('old definition'))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    let mutations!: ReturnType<typeof useConceptMutations>
    function Harness({ onMutations }: { onMutations: (m: ReturnType<typeof useConceptMutations>) => void }) {
      onMutations(useConceptMutations(7))
      return <ConceptEditorDialog open onOpenChange={() => {}} baseId={7} conceptId={42} />
    }
    render(<QueryClientProvider client={qc}><Harness onMutations={m => { mutations = m }} /></QueryClientProvider>)
    expect(await screen.findByDisplayValue('old definition')).toBeInTheDocument()

    vi.mocked(api.getConcept).mockResolvedValue(concept('new definition'))
    await act(async () => {
      await mutations.updateConceptInfoMut.mutateAsync({ conceptId: 42, definition: 'new definition', domain: '' })
    })

    expect(await screen.findByDisplayValue('new definition')).toBeInTheDocument()
  })

  // A save of an existing concept is several requests. When one fails after
  // others went through, the dialog must reload what the server now holds;
  // otherwise a retry re-sends a delete for a term that is already gone.
  it('reloads the concept after a partly failed save, so a retry does not repeat the done steps', async () => {
    const user = userEvent.setup()
    vi.mocked(api.getConcept).mockResolvedValue(concept('d', [term(10, 'en', 'hello'), term(11, 'de', 'hallo')]))
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <ConceptEditorDialog open onOpenChange={() => {}} baseId={7} conceptId={42} />
      </QueryClientProvider>,
    )
    expect(await screen.findAllByTestId('concept-term-row')).toHaveLength(2)

    // Remove the German term, then save: its delete succeeds, the English
    // term's update fails.
    const german = screen.getAllByTestId('concept-term-row').find(r => r.dataset.termId === '11')!
    await user.click(within(german).getByRole('button', { name: 'Remove term' }))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    vi.mocked(api.updateConceptTerm).mockRejectedValueOnce(new Error('conflict'))
    vi.mocked(api.getConcept).mockResolvedValue(concept('d', [term(10, 'en', 'hello')]))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.getConcept).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getAllByTestId('concept-term-row')).toHaveLength(1))

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateConceptTerm).toHaveBeenCalledTimes(2))
    expect(api.deleteConceptTerm).toHaveBeenCalledTimes(1)
    expect(api.deleteConceptTerm).toHaveBeenCalledWith(7, 42, 11)
  })
})
