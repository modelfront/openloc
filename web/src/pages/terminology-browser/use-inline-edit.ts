import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { useLatestRef } from '@/hooks/useLatestRef'
import { useConceptMutations } from './use-concept-mutations'
import type { ConceptTerm, ConceptTermInput } from '../../api'

// One active inline editor at a time. The context needed to commit (the existing
// term, or the concept's sibling field) is captured when the edit starts, so
// commit() never has to read back from the concept list.
type AddDraft = { language: string; text: string; status: string }
export type Active =
  | { kind: 'text'; conceptId: number; term: ConceptTerm; draft: string }
  | { kind: 'status'; conceptId: number; term: ConceptTerm; draft: string }
  | { kind: 'definition'; conceptId: number; definition: string; domain: string; draft: string }
  | { kind: 'domain'; conceptId: number; definition: string; domain: string; draft: string }
  | { kind: 'add'; conceptId: number; draft: AddDraft }
  | null

// EditApi has a STABLE identity (useMemo([])) so it can be passed into the
// []-memoized table columns without churning them. Cells read live state via
// get(); re-renders are driven by the internal useState.
export interface EditApi {
  get(): Active
  startText(conceptId: number, term: ConceptTerm): void
  startStatus(conceptId: number, term: ConceptTerm): void
  startDefinition(conceptId: number, definition: string, domain: string): void
  startDomain(conceptId: number, definition: string, domain: string): void
  startAdd(conceptId: number): void
  setDraft(draft: string): void
  setAddDraft(patch: Partial<AddDraft>): void
  cancel(): void
  commit(): void
}

// A term edit replaces the whole term, so preserve language/POS/notes and change
// only the edited field.
const merge = (t: ConceptTerm, over: Partial<ConceptTermInput>): ConceptTermInput => ({
  language: t.language, text: t.text, status: t.status,
  part_of_speech: t.part_of_speech, notes: t.notes, ...over,
})

export function useConceptInlineEdit(baseId: number): EditApi {
  const { updateTermMut, addTermMut, updateConceptInfoMut } = useConceptMutations(baseId)
  const [, setActive] = useState<Active>(null)
  const activeRef = useRef<Active>(null)
  const set = (a: Active) => { activeRef.current = a; setActive(a) }

  // Mirrored in a ref so the memoized api reads the current .mutate without
  // depending on it.
  const m = useLatestRef({
    updateTerm: updateTermMut.mutate, addTerm: addTermMut.mutate,
    updateInfo: updateConceptInfoMut.mutate,
  })

  return useMemo<EditApi>(() => ({
    get: () => activeRef.current,
    startText: (conceptId, term) => set({ kind: 'text', conceptId, term, draft: term.text }),
    startStatus: (conceptId, term) => set({ kind: 'status', conceptId, term, draft: term.status }),
    startDefinition: (conceptId, definition, domain) => set({ kind: 'definition', conceptId, definition, domain, draft: definition }),
    startDomain: (conceptId, definition, domain) => set({ kind: 'domain', conceptId, definition, domain, draft: domain }),
    // A new term starts with NO language preselected (force an explicit choice)
    // and status "pending" — a freshly-added term isn't approved until reviewed.
    startAdd: (conceptId) => set({ kind: 'add', conceptId, draft: { language: '', text: '', status: 'pending' } }),
    setDraft: (draft) => {
      const a = activeRef.current
      if (a && a.kind !== 'add') set({ ...a, draft })
    },
    setAddDraft: (patch) => {
      const a = activeRef.current
      if (a && a.kind === 'add') set({ ...a, draft: { ...a.draft, ...patch } })
    },
    cancel: () => set(null),
    commit: () => {
      const a = activeRef.current
      if (!a) return
      // The add-term case is validated BEFORE clearing the editor so an invalid
      // submit can warn and keep the row open — rather than silently discarding
      // the typed term (adding with no language was a confusing no-op).
      if (a.kind === 'add') {
        const language = a.draft.language.trim()
        const text = a.draft.text.trim()
        if (!text) { set(null); return } // nothing typed → just dismiss the row
        if (!language) { toast.error('Select a language for the term'); return } // keep the row open
        set(null)
        m.current.addTerm({ conceptId: a.conceptId, term: { language, text, status: a.draft.status } })
        return
      }
      set(null)
      switch (a.kind) {
        case 'text': {
          const text = a.draft.trim()
          if (text && text !== a.term.text) m.current.updateTerm({ conceptId: a.conceptId, termId: a.term.id, term: merge(a.term, { text }) })
          break
        }
        case 'status':
          if (a.draft !== a.term.status) m.current.updateTerm({ conceptId: a.conceptId, termId: a.term.id, term: merge(a.term, { status: a.draft }) })
          break
        case 'definition':
          if (a.draft !== a.definition) m.current.updateInfo({ conceptId: a.conceptId, definition: a.draft, domain: a.domain })
          break
        case 'domain':
          if (a.draft !== a.domain) m.current.updateInfo({ conceptId: a.conceptId, definition: a.definition, domain: a.draft })
          break
      }
    },
  }), [m])
}
