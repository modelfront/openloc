import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { useConceptInlineEdit } from './use-inline-edit'
import type { ConceptTerm } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../../api', () => ({
  createConcept: vi.fn().mockResolvedValue({ id: 1 }),
  updateConcept: vi.fn().mockResolvedValue(undefined),
  deleteConcept: vi.fn().mockResolvedValue(undefined),
  addConceptTerm: vi.fn().mockResolvedValue({ id: 99 }),
  updateConceptTerm: vi.fn().mockResolvedValue(undefined),
  deleteConceptTerm: vi.fn().mockResolvedValue(undefined),
}))

import { updateConcept, addConceptTerm, updateConceptTerm } from '../../api'
import { toast } from 'sonner'

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children)
}

const term = (over: Partial<ConceptTerm> = {}): ConceptTerm => ({
  id: 10, language: 'fr', text: 'bonjour', status: 'approved',
  part_of_speech: 'interjection', notes: 'note',
  created_at: '', updated_at: '', created_by: '', updated_by: '', ...over,
})

describe('useConceptInlineEdit', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('commits a text edit as a full term update, preserving language/POS/notes', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startText(42, term()))
    act(() => result.current.setDraft('salut'))
    act(() => result.current.commit())
    await waitFor(() => expect(updateConceptTerm).toHaveBeenCalledWith(7, 42, 10, {
      language: 'fr', text: 'salut', status: 'approved', part_of_speech: 'interjection', notes: 'note',
    }))
    expect(result.current.get()).toBeNull()
  })

  it('does not fire an update when the text is unchanged', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startText(42, term({ text: 'bonjour' })))
    act(() => result.current.commit())
    // give any microtask a chance
    await Promise.resolve()
    expect(updateConceptTerm).not.toHaveBeenCalled()
  })

  it('does not fire an update for empty text (backend rejects it)', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startText(42, term()))
    act(() => result.current.setDraft('   '))
    act(() => result.current.commit())
    await Promise.resolve()
    expect(updateConceptTerm).not.toHaveBeenCalled()
  })

  it('commits a status edit', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startStatus(42, term()))
    act(() => result.current.setDraft('forbidden'))
    act(() => result.current.commit())
    await waitFor(() => expect(updateConceptTerm).toHaveBeenCalledWith(7, 42, 10,
      expect.objectContaining({ status: 'forbidden', text: 'bonjour', language: 'fr' })))
  })

  it('commits a definition edit, keeping the current domain', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startDefinition(42, 'old def', 'sports'))
    act(() => result.current.setDraft('new def'))
    act(() => result.current.commit())
    await waitFor(() => expect(updateConcept).toHaveBeenCalledWith(7, 42, { definition: 'new def', domain: 'sports' }))
  })

  it('commits a domain edit, keeping the current definition', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startDomain(42, 'the def', 'old dom'))
    act(() => result.current.setDraft('new dom'))
    act(() => result.current.commit())
    await waitFor(() => expect(updateConcept).toHaveBeenCalledWith(7, 42, { definition: 'the def', domain: 'new dom' }))
  })

  it('adds a term with the chosen language + text, defaulting status to pending', async () => {
    // A new term starts with no language and status "pending"; the user picks the
    // language explicitly. The committed term reflects that.
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startAdd(42))
    act(() => result.current.setAddDraft({ language: 'en', text: 'hello' }))
    act(() => result.current.commit())
    await waitFor(() => expect(addConceptTerm).toHaveBeenCalledWith(7, 42, { language: 'en', text: 'hello', status: 'pending' }))
  })

  it('warns and keeps the editor open when text is entered but no language is chosen', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startAdd(42))
    act(() => result.current.setAddDraft({ text: 'hello' })) // no language picked
    act(() => result.current.commit())
    await Promise.resolve()
    expect(addConceptTerm).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith('Select a language for the term')
    // The row stays open (still an 'add' editor) so the user can pick a language.
    expect(result.current.get()).toMatchObject({ kind: 'add', conceptId: 42 })
  })

  it('silently dismisses an empty add row (no text) without warning', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startAdd(42))
    act(() => result.current.commit())
    await Promise.resolve()
    expect(addConceptTerm).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
    expect(result.current.get()).toBeNull()
  })

  it('does not add a term without text', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startAdd(42))
    act(() => result.current.setAddDraft({ language: 'en' }))
    act(() => result.current.commit())
    await Promise.resolve()
    expect(addConceptTerm).not.toHaveBeenCalled()
  })

  it('cancel clears the active editor without firing a mutation', async () => {
    const { result } = renderHook(() => useConceptInlineEdit(7), { wrapper: wrapper() })
    act(() => result.current.startText(42, term()))
    act(() => result.current.setDraft('salut'))
    act(() => result.current.cancel())
    await Promise.resolve()
    expect(result.current.get()).toBeNull()
    expect(updateConceptTerm).not.toHaveBeenCalled()
  })
})
