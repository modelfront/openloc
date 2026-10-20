import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import {
  createConcept, updateConcept, deleteConcept,
  addConceptTerm, updateConceptTerm, deleteConceptTerm,
  type Concept, type ConceptTermInput,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { patchInfiniteRows } from '../../lib/query-invalidation'

// A term as held in the editor: no id = a term to create.
export interface EditorTerm extends ConceptTermInput {
  id?: number
}

export interface SaveConceptInput {
  conceptId?: number // absent = create a new concept
  definition: string
  domain: string
  terms: EditorTerm[]
  originalTermIds: number[] // term ids present when the dialog opened (for diffing deletes)
}

const toInput = (t: EditorTerm): ConceptTermInput => ({
  language: t.language,
  text: t.text,
  status: t.status,
  part_of_speech: t.part_of_speech ?? '',
  notes: t.notes ?? '',
})

// An inline edit changes one field of one concept. Patching it into the cached
// browser pages, instead of invalidating the list, avoids reloading every page
// the user scrolled through; only the open editor's copy is refetched.
function patchConcept(queryClient: QueryClient, baseId: number, conceptId: number, edit: (c: Concept) => Concept) {
  patchInfiniteRows<Concept>(queryClient, queryKeys.concepts.allByBase(baseId), rows =>
    rows.some(c => c.id === conceptId) ? rows.map(c => (c.id === conceptId ? edit(c) : c)) : rows,
  )
  queryClient.invalidateQueries({ queryKey: queryKeys.concepts.detail(baseId, conceptId) })
}

export function useConceptMutations(baseId: number) {
  const queryClient = useQueryClient()
  const invalidateKeys = [
    queryKeys.concepts.allByBase(baseId),
    queryKeys.termBaseLanguages(baseId),
    queryKeys.terminology.all,
  ]

  // saveConcept creates a new concept, or updates an existing one and reconciles
  // its terms (add new, update changed, delete removed) in one action. Resolves
  // to true when it created the concept.
  const saveConceptMut = useMutationWithToast({
    mutationFn: async (input: SaveConceptInput) => {
      if (input.conceptId == null) {
        await createConcept(baseId, {
          definition: input.definition,
          domain: input.domain,
          terms: input.terms.map(toInput),
        })
        return true
      }
      const conceptId = input.conceptId
      await updateConcept(baseId, conceptId, { definition: input.definition, domain: input.domain })

      const keptIds = new Set(input.terms.filter(t => t.id != null).map(t => t.id as number))
      const ops: Promise<unknown>[] = []
      for (const id of input.originalTermIds) {
        if (!keptIds.has(id)) ops.push(deleteConceptTerm(baseId, conceptId, id))
      }
      for (const t of input.terms) {
        if (t.id == null) ops.push(addConceptTerm(baseId, conceptId, toInput(t)))
        else ops.push(updateConceptTerm(baseId, conceptId, t.id, toInput(t)))
      }
      await Promise.all(ops)
      return false
    },
    successMessage: created => (created ? 'Concept created' : 'Concept saved'),
    errorMessage: 'Save failed',
    invalidateKeys,
    // A save is several requests, and some may have gone through before one
    // failed. Reload, so the table and the open editor (and the term ids its
    // next save diffs against) show what the server now holds.
    onError: () => {
      for (const key of invalidateKeys) queryClient.invalidateQueries({ queryKey: key })
    },
  })

  const deleteConceptMut = useMutationWithToast({
    mutationFn: (conceptId: number) => deleteConcept(baseId, conceptId),
    successMessage: 'Concept deleted',
    errorMessage: 'Delete failed',
    invalidateKeys,
  })

  // Inline edits from the table view — one field/term at a time (not a full
  // reconcile like saveConcept). Toast only on error to keep quick edits quiet.
  const updateTermMut = useMutationWithToast({
    mutationFn: ({ conceptId, termId, term }: { conceptId: number; termId: number; term: ConceptTermInput }) =>
      updateConceptTerm(baseId, conceptId, termId, term),
    successMessage: () => null,
    errorMessage: 'Update failed',
    onSuccess: (_, { conceptId, termId, term }) => patchConcept(queryClient, baseId, conceptId, c => ({
      ...c,
      terms: c.terms.map(t => (t.id === termId ? { ...t, ...term } : t)),
    })),
  })

  // A new term can add a language to the base, so adding one invalidates in full.
  const addTermMut = useMutationWithToast({
    mutationFn: ({ conceptId, term }: { conceptId: number; term: ConceptTermInput }) =>
      addConceptTerm(baseId, conceptId, term),
    successMessage: 'Term added',
    errorMessage: 'Add failed',
    invalidateKeys,
  })

  const updateConceptInfoMut = useMutationWithToast({
    mutationFn: ({ conceptId, definition, domain }: { conceptId: number; definition: string; domain: string }) =>
      updateConcept(baseId, conceptId, { definition, domain }),
    successMessage: () => null,
    errorMessage: 'Update failed',
    onSuccess: (_, { conceptId, definition, domain }) =>
      patchConcept(queryClient, baseId, conceptId, c => ({ ...c, definition, domain })),
  })

  return { saveConceptMut, deleteConceptMut, updateTermMut, addTermMut, updateConceptInfoMut }
}
