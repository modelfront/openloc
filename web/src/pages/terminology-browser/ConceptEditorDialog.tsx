import { useEffect, useMemo, useRef, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { toast } from 'sonner'
import { useQuery } from '@tanstack/react-query'
import { getConcept, listLanguages } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Combobox } from '@/components/ui/combobox'
import { DeleteButton } from '@/components/ui/delete-button'
import { FormField } from '@/components/ui/form-field'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { Plus } from 'lucide-react'
import { langPickerProps, toLangOptions } from '../../components/lang-options'
import ConfirmDialog from '../../components/ConfirmDialog'
import { useConceptMutations, type EditorTerm } from './use-concept-mutations'
import { LanguageFilter } from './LanguageFilter'
import { formatDate } from '../../utils'
import { TERM_STATUS_OPTIONS as STATUS_OPTIONS } from '@/lib/term-status'
import { drawAttention } from '@/lib/attention'


interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  baseId: number
  /** null = create a new concept. */
  conceptId: number | null
  /** If set, scroll that term's card into view + briefly highlight it on open. */
  focusTermId?: number
  /** Default language for the first term of a brand-new concept. */
  defaultLanguage?: string
}

// A locally-editable term row. Existing terms keep their id (for diff on save);
// new ones have a client-only key so React can track them before they get one.
// The created/updated stamps are display-only (never sent on save).
interface Row extends EditorTerm {
  key: string
  created_at?: string
  updated_at?: string
  created_by?: string
  updated_by?: string
}

let rowSeq = 0
// New term rows default to "pending" (a freshly-added term isn't approved until
// reviewed) with no language preselected (forces an explicit choice).
const newRow = (language = '', status = 'pending'): Row => ({
  key: `new-${rowSeq++}`, language, text: '', status, part_of_speech: '', notes: '',
})

// ConceptEditorDialog edits a full multilingual concept: definition, domain, and
// its terms across any languages (synonyms allowed), each with status + POS. On
// save it creates a new concept or reconciles an existing one's terms.
export default function ConceptEditorDialog({ open, onOpenChange, baseId, conceptId, focusTermId, defaultLanguage }: Props) {
  const { saveConceptMut, deleteConceptMut } = useConceptMutations(baseId)
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Key of an EXISTING term row pending delete-confirmation. New (unsaved) rows
  // are removed outright — there's nothing to "delete", they were never saved.
  const [confirmRemoveKey, setConfirmRemoveKey] = useState<string | null>(null)

  const { data: allLanguages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const langOptions = useMemo(() => allLanguages ?? [], [allLanguages])
  const nameOf = (code: string) => langOptions.find(l => l.code === code)?.name ?? code

  // Load the concept when editing an existing one.
  const { data: loaded, isLoading } = useQuery({
    queryKey: queryKeys.concepts.detail(baseId, conceptId),
    queryFn: () => getConcept(baseId, conceptId as number),
    enabled: open && conceptId != null,
  })

  const [definition, setDefinition] = useState('')
  const [domain, setDomain] = useState('')
  const [rows, setRows] = useState<Row[]>([])
  const [originalTermIds, setOriginalTermIds] = useState<number[]>([])
  const [termSearch, setTermSearch] = useState('')
  const [langFilter, setLangFilter] = useState<string[]>([]) // empty = all languages
  // Scroll-to-term-on-open (the "zoom into this term" affordance).
  const scrollRef = useRef<HTMLDivElement>(null)
  // Scroll to the focused term ONCE per open. Without this guard the scroll
  // effect (which depends on `rows` so it waits for the cards to render) re-runs
  // on every `rows` change — e.g. clicking "Add term" would yank the view back
  // to the originally-focused term instead of showing the new row at the top.
  const scrolledToFocusRef = useRef(false)

  // Reset local state whenever the dialog opens (or the loaded concept arrives).
  useOnChange([open, conceptId, loaded, defaultLanguage], () => {
    if (!open) return
    setTermSearch(''); setLangFilter([])
    if (conceptId == null) {
      setDefinition(''); setDomain(''); setRows([newRow(defaultLanguage)]); setOriginalTermIds([])
    } else if (loaded) {
      setDefinition(loaded.definition)
      setDomain(loaded.domain)
      setRows(loaded.terms.map(t => ({
        key: `t-${t.id}`, id: t.id, language: t.language, text: t.text,
        status: t.status, part_of_speech: t.part_of_speech, notes: t.notes,
        created_at: t.created_at, updated_at: t.updated_at,
        created_by: t.created_by, updated_by: t.updated_by,
      })))
      setOriginalTermIds(loaded.terms.map(t => t.id))
    }
  }, { onMount: true })

  // Reset the "already scrolled" guard each time the dialog opens or the focus
  // target changes, so the next open scrolls again. Runs before the scroll
  // effect below; a plain `rows` change (Add term) doesn't re-run it.
  useEffect(() => { if (open) scrolledToFocusRef.current = false }, [open, focusTermId])

  // Once the target term's card is in the DOM, draw the eye to it — but only
  // the FIRST time after open. `rows` stays in the deps so this waits for the
  // cards to render; the ref guard stops it from re-scrolling when the term
  // list later changes (Add term, edits, deletes).
  useEffect(() => {
    if (!open || focusTermId == null || scrolledToFocusRef.current) return
    const raf = requestAnimationFrame(() => {
      const el = scrollRef.current?.querySelector<HTMLElement>(`[data-term-id="${focusTermId}"]`)
      if (!el) return
      scrolledToFocusRef.current = true
      drawAttention({ target: el })
    })
    return () => cancelAnimationFrame(raf)
  }, [open, focusTermId, rows])

  const setRow = (key: string, patch: Partial<Row>) =>
    setRows(rs => rs.map(r => r.key === key ? { ...r, ...patch } : r))
  const removeRow = (key: string) => setRows(rs => rs.filter(r => r.key !== key))
  // Clear the filters + PREPEND the new card so it appears right under the
  // "Add term" button, then scroll the term list back to the top so the new row
  // is actually in view (the dialog may have been opened scrolled to a specific
  // term via focusTermId).
  const addRow = () => {
    setTermSearch(''); setLangFilter([]); setRows(rs => [newRow(), ...rs])
    requestAnimationFrame(() => scrollRef.current?.scrollTo({ top: 0, behavior: 'smooth' }))
  }

  // Distinct languages present in this concept — drives the language filter.
  const rowLangs = [...new Set(rows.map(r => r.language).filter(Boolean))]

  // Filter which term cards show (by language filter, then by text/lang search).
  // rows stays intact — only the display is filtered, so save is unaffected.
  const q = termSearch.trim().toLowerCase()
  const shownRows = rows
    .filter(r => langFilter.length === 0 || langFilter.includes(r.language))
    .filter(r => !q || r.text.toLowerCase().includes(q) || r.language.toLowerCase().includes(q) || nameOf(r.language).toLowerCase().includes(q))

  const validRows = rows.filter(r => r.language.trim() && r.text.trim())
  const canSave = validRows.length > 0 && !saveConceptMut.isPending

  const save = () => {
    // A row with text but no language is an incomplete term the user clearly
    // meant to add — don't silently drop it on save (which looked like a no-op).
    // Make them pick a language (or clear the row) first, and scroll+focus the
    // offending term so it's obvious which one needs attention.
    const incomplete = rows.find(r => r.text.trim() && !r.language.trim())
    if (incomplete) {
      // Clear filters so the offending row is guaranteed to be rendered.
      setTermSearch(''); setLangFilter([])
      toast.error('Select a language for every term (or clear the empty one).')
      // Draw the eye to that row and focus its language picker (its popover
      // trigger, else the first focusable control).
      const row = `[data-row-key="${incomplete.key}"]`
      drawAttention({ target: row, focus: `${row} [data-slot="popover-trigger"], ${row} button, ${row} input` })
      return
    }
    saveConceptMut.mutate({
      conceptId: conceptId ?? undefined,
      definition: definition.trim(),
      domain: domain.trim(),
      terms: validRows.map(({ key, created_at, updated_at, created_by, updated_by, ...t }) => t),
      originalTermIds,
    }, { onSuccess: () => onOpenChange(false) })
  }

  const remove = () => {
    if (conceptId == null) return
    deleteConceptMut.mutate(conceptId, { onSuccess: () => onOpenChange(false) })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* Don't auto-focus a field on open — it isn't needed here, and focusing
          the Domain input both looked like an edit prompt and scrolled the term
          list back to the top (fighting the scroll-to-term below). */}
      <DialogContent className="sm:max-w-2xl" initialFocus={false}>
        <DialogHeader>
          <DialogTitle>{conceptId == null ? 'New concept' : 'Edit concept'}</DialogTitle>
        </DialogHeader>

        {conceptId != null && isLoading ? (
          <LoadingPlaceholder size="sm" />
        ) : (
          <div ref={scrollRef} className="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <FormField label="Domain">
                <Input value={domain} onChange={e => setDomain(e.target.value)} placeholder="e.g. software" />
              </FormField>
            </div>
            <FormField label="Definition">
              <Textarea value={definition} onChange={e => setDefinition(e.target.value)} placeholder="What does this concept mean?" rows={2} />
            </FormField>

            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="text-sm font-medium">Terms <span className="text-gray-400 font-normal">({rows.length})</span></label>
                <Button type="button" variant="outline" size="xs" onClick={addRow}>
                  <Plus className="w-3 h-3 mr-1" /> Add term
                </Button>
              </div>
              {(rows.length > 3 || rowLangs.length > 1) && (
                <div className="flex items-center gap-2 mb-2">
                  {rows.length > 3 && (
                    <Input
                      className="h-8 text-sm flex-1"
                      placeholder="Search terms…"
                      value={termSearch}
                      onChange={e => setTermSearch(e.target.value)}
                    />
                  )}
                  {rowLangs.length > 1 && (
                    <LanguageFilter languages={rowLangs} selected={langFilter} onChange={setLangFilter} nameOf={nameOf} />
                  )}
                </div>
              )}
              <div className="space-y-3">
                {shownRows.map(row => (
                  <div
                    key={row.key}
                    data-term-id={row.id ?? undefined}
                    data-row-key={row.key}
                    className="rounded-lg border border-gray-200 p-3 space-y-3 dark:border-gray-800"
                    data-testid="concept-term-row"
                  >
                    {/* Language heading — read-only for existing terms (its language
                        can't be changed), a picker for newly added ones. */}
                    <div className="flex items-center justify-between gap-2">
                      {row.id != null ? (
                        <div className="flex items-baseline gap-2">
                          <span className="text-xs uppercase font-mono text-gray-400 dark:text-gray-500">{row.language}</span>
                          <span className="text-sm font-medium">{nameOf(row.language)}</span>
                        </div>
                      ) : (
                        <div className="w-56">
                          <Combobox options={toLangOptions(langOptions)} value={row.language} onChange={v => setRow(row.key, { language: v })} placeholder="Language" className="w-full" {...langPickerProps} />
                        </div>
                      )}
                      <DeleteButton type="button" onClick={() => { if (row.id != null) setConfirmRemoveKey(row.key); else removeRow(row.key) }} aria-label="Remove term" />
                    </div>

                    <FormField label="Term">
                      <Textarea aria-label="Term" value={row.text} onChange={e => setRow(row.key, { text: e.target.value })} placeholder="Term" rows={2} inputClassName="min-h-0 resize-y" />
                    </FormField>

                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                      <FormField label="Status">
                        <Combobox value={row.status} onChange={v => setRow(row.key, { status: v })} options={STATUS_OPTIONS} />
                      </FormField>
                      <FormField label="Part of speech">
                        <Input value={row.part_of_speech ?? ''} onChange={e => setRow(row.key, { part_of_speech: e.target.value })} placeholder="e.g. noun" />
                      </FormField>
                    </div>

                    <FormField label="Notes">
                      <Textarea value={row.notes ?? ''} onChange={e => setRow(row.key, { notes: e.target.value })} placeholder="Optional" rows={2} inputClassName="min-h-0 resize-y" />
                    </FormField>

                    {/* Read-only audit stamps for existing terms (new ones have none). */}
                    {row.id != null && (row.created_at || row.updated_at) && (
                      <div className="flex flex-wrap gap-x-6 gap-y-0.5 pt-1 text-xs text-gray-400 dark:text-gray-500 wrap-anywhere">
                        {row.created_at && (
                          <span>Created {formatDate(row.created_at)}{row.created_by && ` · ${row.created_by}`}</span>
                        )}
                        {row.updated_at && (
                          <span>Updated {formatDate(row.updated_at)}{row.updated_by && ` · ${row.updated_by}`}</span>
                        )}
                      </div>
                    )}
                  </div>
                ))}
                {rows.length === 0 && (
                  <p className="text-sm text-gray-400 italic">No terms — add at least one.</p>
                )}
                {rows.length > 0 && shownRows.length === 0 && (
                  <p className="text-sm text-gray-400 italic wrap-anywhere">
                    {q ? `No terms match “${termSearch}”.` : 'No terms in the selected languages.'}
                  </p>
                )}
              </div>
            </div>
          </div>
        )}

        <DialogFooter className="mt-4 sm:justify-between">
          <div>
            {conceptId != null && (
              <Button type="button" variant="ghost" onClick={() => setConfirmDelete(true)} disabled={deleteConceptMut.isPending}
                className="text-red-600 hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-900/20">
                Delete concept
              </Button>
            )}
          </div>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="button" onClick={save} disabled={!canSave}>
              {saveConceptMut.isPending ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete this concept?"
        description="This removes the concept and all its terms across every language. This can't be undone from here."
        onConfirm={remove}
      />

      <ConfirmDialog
        open={confirmRemoveKey != null}
        onOpenChange={(o) => { if (!o) setConfirmRemoveKey(null) }}
        title="Delete this term?"
        description="The term is removed from the concept when you save."
        onConfirm={() => { if (confirmRemoveKey) removeRow(confirmRemoveKey); setConfirmRemoveKey(null) }}
      />
    </Dialog>
  )
}
