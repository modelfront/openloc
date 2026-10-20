import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useInfiniteQuery } from '@tanstack/react-query'
import { createElement } from 'react'
import { useConceptMutations } from './use-concept-mutations'
import { queryKeys } from '../../lib/query-keys'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

// Mock the api layer so we can assert which endpoints the save-diff fires.
vi.mock('../../api', async () => ({
  ApiError: (await vi.importActual<typeof import('../../api/request')>('../../api/request')).ApiError,
  createConcept: vi.fn().mockResolvedValue({ id: 1 }),
  updateConcept: vi.fn().mockResolvedValue(undefined),
  deleteConcept: vi.fn().mockResolvedValue(undefined),
  addConceptTerm: vi.fn().mockResolvedValue({ id: 99 }),
  updateConceptTerm: vi.fn().mockResolvedValue(undefined),
  deleteConceptTerm: vi.fn().mockResolvedValue(undefined),
  listConcepts: vi.fn(),
}))

import {
  createConcept, updateConcept, deleteConcept, addConceptTerm, updateConceptTerm, deleteConceptTerm,
  listConcepts, type Concept,
} from '../../api'
import { ApiError } from '../../api/request'
import { toast } from 'sonner'

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children)
}

describe('useConceptMutations.saveConcept', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('creates a new concept with all its terms when there is no conceptId', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.saveConceptMut.mutate({
        definition: 'greeting', domain: 'general',
        terms: [{ language: 'en', text: 'hi', status: 'approved' }],
        originalTermIds: [],
      })
    })
    await waitFor(() => expect(result.current.saveConceptMut.isSuccess).toBe(true))
    expect(createConcept).toHaveBeenCalledWith(7, expect.objectContaining({
      definition: 'greeting', domain: 'general',
      terms: [expect.objectContaining({ language: 'en', text: 'hi', status: 'approved' })],
    }))
    expect(updateConcept).not.toHaveBeenCalled()
  })

  it('reconciles terms on an existing concept: adds new, updates kept, deletes removed', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.saveConceptMut.mutate({
        conceptId: 42,
        definition: 'greeting', domain: 'general',
        terms: [
          { id: 10, language: 'en', text: 'hello', status: 'approved' }, // kept → update
          { language: 'de', text: 'hallo', status: 'approved' },          // no id → add
        ],
        originalTermIds: [10, 11], // 11 is gone → delete
      })
    })
    await waitFor(() => expect(result.current.saveConceptMut.isSuccess).toBe(true))

    expect(updateConcept).toHaveBeenCalledWith(7, 42, { definition: 'greeting', domain: 'general' })
    expect(updateConceptTerm).toHaveBeenCalledWith(7, 42, 10, expect.objectContaining({ text: 'hello' }))
    expect(addConceptTerm).toHaveBeenCalledWith(7, 42, expect.objectContaining({ language: 'de', text: 'hallo' }))
    expect(deleteConceptTerm).toHaveBeenCalledWith(7, 42, 11)
    // The kept term must NOT be deleted.
    expect(deleteConceptTerm).toHaveBeenCalledTimes(1)
    // Existing-concept path must not create a new concept.
    expect(createConcept).not.toHaveBeenCalled()
  })
})

describe('useConceptMutations inline term/concept mutations', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('updateTermMut calls updateConceptTerm', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.updateTermMut.mutate({ conceptId: 42, termId: 10, term: { language: 'fr', text: 'salut', status: 'approved' } })
    })
    await waitFor(() => expect(result.current.updateTermMut.isSuccess).toBe(true))
    expect(updateConceptTerm).toHaveBeenCalledWith(7, 42, 10, { language: 'fr', text: 'salut', status: 'approved' })
  })

  it('addTermMut calls addConceptTerm', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.addTermMut.mutate({ conceptId: 42, term: { language: 'de', text: 'hallo', status: 'approved' } })
    })
    await waitFor(() => expect(result.current.addTermMut.isSuccess).toBe(true))
    expect(addConceptTerm).toHaveBeenCalledWith(7, 42, { language: 'de', text: 'hallo', status: 'approved' })
  })

  it('updateConceptInfoMut calls updateConcept with definition + domain', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.updateConceptInfoMut.mutate({ conceptId: 42, definition: 'd', domain: 'm' })
    })
    await waitFor(() => expect(result.current.updateConceptInfoMut.isSuccess).toBe(true))
    expect(updateConcept).toHaveBeenCalledWith(7, 42, { definition: 'd', domain: 'm' })
  })
})

describe('useConceptMutations invalidation', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('a concept edit invalidates the concept editor\'s detail query', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const detailKey = queryKeys.concepts.detail(7, 42)
    const otherBaseKey = queryKeys.concepts.detail(8, 42)
    qc.setQueryData(detailKey, { id: 42, definition: 'old' })
    qc.setQueryData(otherBaseKey, { id: 42, definition: 'other base' })
    const wrap = ({ children }: { children: React.ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children)

    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrap })
    await act(async () => {
      result.current.updateConceptInfoMut.mutate({ conceptId: 42, definition: 'new', domain: 'm' })
    })
    await waitFor(() => expect(result.current.updateConceptInfoMut.isSuccess).toBe(true))

    expect(qc.getQueryState(detailKey)?.isInvalidated).toBe(true)
    expect(qc.getQueryState(otherBaseKey)?.isInvalidated).toBe(false)
  })
})

describe('useConceptMutations error notices', () => {
  beforeEach(() => { vi.clearAllMocks() })

  const deleteRejectedWith = async (err: Error) => {
    vi.mocked(deleteConcept).mockRejectedValueOnce(err)
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => { result.current.deleteConceptMut.mutate(42) })
    await waitFor(() => expect(result.current.deleteConceptMut.isError).toBe(true))
  }

  it('a demo organization refusal shows no error toast of its own', async () => {
    // The query client shows the Playground notice for every refused write.
    await deleteRejectedWith(new ApiError(403, 'read only', { error: { code: 'demo_read_only' } }))
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('any other failure shows the error toast', async () => {
    await deleteRejectedWith(new ApiError(403, 'forbidden', { error: { code: 'forbidden' } }))
    expect(toast.error).toHaveBeenCalledWith('Delete failed: forbidden')
  })

  it('a save reports whether it created or saved the concept', async () => {
    const { result } = renderHook(() => useConceptMutations(7), { wrapper: wrapper() })
    await act(async () => {
      result.current.saveConceptMut.mutate({ definition: 'd', domain: 'm', terms: [], originalTermIds: [] })
    })
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Concept created'))
    await act(async () => {
      result.current.saveConceptMut.mutate({ conceptId: 42, definition: 'd', domain: 'm', terms: [], originalTermIds: [] })
    })
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Concept saved'))
  })
})

describe('useConceptMutations inline edits and the loaded browser list', () => {
  beforeEach(() => { vi.clearAllMocks() })

  const concept = (id: number, text: string): Concept => ({
    id, definition: 'def', domain: '', created_at: '', updated_at: '', created_by: '', updated_by: '',
    terms: [{ id: id * 10, language: 'en', text, status: 'approved', part_of_speech: '', notes: '', created_at: '', updated_at: '', created_by: '', updated_by: '' }],
  })

  // Mounts the browser's list query next to the mutations, as the page does.
  async function renderWithList() {
    vi.mocked(listConcepts).mockResolvedValue({ data: [concept(42, 'hello'), concept(43, 'other')], cursor: 43, has_more: false, total: 2 })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const listKey = queryKeys.concepts.byBase(7, '', [])
    const wrap = ({ children }: { children: React.ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children)
    const { result } = renderHook(() => ({
      list: useInfiniteQuery({
        queryKey: listKey,
        queryFn: ({ pageParam }) => listConcepts(7, { cursor: pageParam }),
        getNextPageParam: () => undefined,
        initialPageParam: 0,
      }),
      m: useConceptMutations(7),
    }), { wrapper: wrap })
    await waitFor(() => expect(result.current.list.isSuccess).toBe(true))
    const rows = () => result.current.list.data!.pages.flatMap(p => p.data)
    return { result, rows }
  }

  it('an inline term edit shows in the loaded list without reloading it', async () => {
    const { result, rows } = await renderWithList()
    await act(async () => {
      await result.current.m.updateTermMut.mutateAsync({ conceptId: 42, termId: 420, term: { language: 'en', text: 'hi there', status: 'pending' } })
    })

    await waitFor(() => expect(rows()[0].terms[0]).toMatchObject({ id: 420, text: 'hi there', status: 'pending' }))
    expect(rows()[1].terms[0].text, 'other concepts are untouched').toBe('other')
    expect(listConcepts).toHaveBeenCalledTimes(1)
    expect(result.current.list.isFetching).toBe(false)
  })

  it('an inline definition edit shows in the loaded list without reloading it', async () => {
    const { result, rows } = await renderWithList()
    await act(async () => {
      await result.current.m.updateConceptInfoMut.mutateAsync({ conceptId: 43, definition: 'new def', domain: 'law' })
    })

    await waitFor(() => expect(rows()[1]).toMatchObject({ definition: 'new def', domain: 'law' }))
    expect(rows()[0].definition).toBe('def')
    expect(listConcepts).toHaveBeenCalledTimes(1)
  })

  it('adding a term reloads the list (it can add a language)', async () => {
    const { result } = await renderWithList()
    await act(async () => {
      await result.current.m.addTermMut.mutateAsync({ conceptId: 42, term: { language: 'de', text: 'hallo', status: 'pending' } })
    })
    await waitFor(() => expect(listConcepts).toHaveBeenCalledTimes(2))
  })
})
