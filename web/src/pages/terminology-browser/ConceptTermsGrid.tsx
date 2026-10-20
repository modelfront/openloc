import { createContext, Fragment, useContext, useState } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Combobox } from '@/components/ui/combobox'
import { Button } from '@/components/ui/button'
import { ChipButton } from '@/components/ui/chip'
import { Plus, Check, X, Maximize2 } from 'lucide-react'
import { langPickerProps, toLangOptions } from '../../components/lang-options'
import type { Concept } from '../../api'
import type { EditApi } from './use-inline-edit'
import { TERM_STATUS_COLOR, TERM_STATUS_OPTIONS as STATUS_OPTIONS, termStatusLabel } from '@/lib/term-status'

// Shared by the header and the grid so the Language/Terms/Status sub-columns line up.
export const TERMS_GRID = 'grid grid-cols-[80px_minmax(0,1fr)_120px] gap-x-6'

// Focus the input exactly when it mounts (i.e. when a field enters edit). Stable
// identity → React does NOT re-invoke it on keystroke re-renders, so the caret is
// never yanked. Not `autoFocus`.
export const focusOnMount = (el: HTMLElement | null) => el?.focus()

/** The languages the add-term picker offers. A context, so the memoized
 *  columns that render the grid never rebuild when the list loads. */
export const TermLanguagesContext = createContext<{ code: string; name: string }[]>([])

// InlineStatus opens its dropdown immediately (on entering status-edit); selecting
// commits, dismissing cancels.
function InlineStatus({ value, onChange, onClose }: { value: string; onChange: (v: string) => void; onClose: () => void }) {
  const [open, setOpen] = useState(true)
  return (
    <div onClick={e => e.stopPropagation()}>
      <Combobox
        value={value}
        onChange={onChange}
        options={STATUS_OPTIONS}
        open={open}
        onOpenChange={o => { setOpen(o); if (!o) onClose() }}
        className="h-7"
        aria-label="Term status"
      />
    </div>
  )
}

// ConceptTermsGrid: Language | Terms | Status as one grid, one row per term, with
// click-to-edit text + status, hover-delete, and an add-term row. See
// use-inline-edit.ts for the edit-state controller and the safe-edit recipe.
export function ConceptTermsGrid({ concept, editApi, onOpenConcept }: { concept: Concept; editApi: EditApi; onOpenConcept: (conceptId: number, termId?: number) => void }) {
  const languages = useContext(TermLanguagesContext)
  const terms = concept.terms
  const active = editApi.get()
  const addingHere = active?.kind === 'add' && active.conceptId === concept.id

  const commitKeys = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') { e.preventDefault(); editApi.commit() }
    else if (e.key === 'Escape') { e.preventDefault(); editApi.cancel() }
  }

  return (
    <div className={`${TERMS_GRID} -my-2`}>
      {terms.length === 0 && !addingHere && (
        <div className="col-span-3 py-2 text-gray-400 italic">no terms</div>
      )}
      {terms.map((t, i) => {
        const firstOfLang = i === 0 || terms[i - 1].language !== t.language
        const editingText = active?.kind === 'text' && active.term.id === t.id
        const editingStatus = active?.kind === 'status' && active.term.id === t.id
        return (
          <Fragment key={t.id}>
            {i > 0 && firstOfLang && <div className="col-span-3 border-t border-gray-200 dark:border-gray-700" />}
            <div className="py-2 min-w-0">
              {firstOfLang && <span className="text-xs uppercase font-mono text-gray-400 dark:text-gray-500">{t.language}</span>}
            </div>
            {/* The whole cell is the click target so any empty space next to a
                short term still opens the editor. */}
            <div
              className={`py-2 min-w-0 ${editingText ? '' : 'cursor-text rounded -mx-1 px-1 hover:bg-gray-100 dark:hover:bg-gray-800'}`}
              onClick={editingText ? undefined : e => { e.stopPropagation(); editApi.startText(concept.id, t) }}
            >
              {editingText ? (
                // Textarea (not Input) so long terms wrap instead of scrolling
                // horizontally. Enter still commits (a term is one value); Shift+
                // Enter is ignored — terms don't span lines. Grows with content.
                <Textarea
                  ref={focusOnMount}
                  aria-label="Edit term"
                  // The terms column is a narrow grid cell, so the box padding
                  // is tightened to leave room to type.
                  className="text-sm px-1 py-1"
                  inputClassName="min-h-0 resize-none"
                  rows={Math.min(4, Math.max(1, Math.ceil((active.draft.length || 1) / 55)))}
                  value={active.draft}
                  onChange={e => editApi.setDraft(e.target.value)}
                  onKeyDown={commitKeys}
                  onBlur={() => editApi.commit()}
                  onClick={e => e.stopPropagation()}
                />
              ) : (
                <span className={`block break-words text-sm text-gray-800 dark:text-gray-200 ${t.status === 'forbidden' ? 'line-through' : ''}`}>{t.text}</span>
              )}
            </div>
            {/* Status + the "open in concept view" action, right-aligned together
                so the badge sits next to the expand icon (not stranded at the
                left of a wide column). */}
            <div className="py-2 min-w-0 flex items-center justify-end gap-2">
              {editingStatus ? (
                <div className="flex-1"><InlineStatus value={active.draft} onChange={v => { editApi.setDraft(v); editApi.commit() }} onClose={() => editApi.cancel()} /></div>
              ) : (
                <>
                  <ChipButton
                    color={TERM_STATUS_COLOR[t.status] ?? 'grey'}
                    onClick={e => { e.stopPropagation(); editApi.startStatus(concept.id, t) }}
                  >
                    {termStatusLabel(t.status)}
                  </ChipButton>
                  {/* Deleting a term happens in the concept view (with a
                      confirmation), not inline — so a stray click can't silently
                      drop a term from the list. */}
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    type="button"
                    aria-label="Open in concept view"
                    title="Open in concept view"
                    className="shrink-0 text-gray-400 hover:text-primary"
                    onClick={e => { e.stopPropagation(); onOpenConcept(concept.id, t.id) }}
                  >
                    <Maximize2 className="w-3.5 h-3.5" />
                  </Button>
                </>
              )}
            </div>
          </Fragment>
        )
      })}

      {addingHere ? (
        <>
          {terms.length > 0 && <div className="col-span-3 border-t border-gray-200 dark:border-gray-700" />}
          <div className="col-span-3 py-2 flex items-center gap-2" onClick={e => e.stopPropagation()}>
            <div className="w-40 shrink-0">
              <Combobox
                options={toLangOptions(languages)}
                value={active.draft.language}
                onChange={v => editApi.setAddDraft({ language: v })}
                placeholder="Language"
                className="w-full"
                {...langPickerProps}
              />
            </div>
            <Input
              ref={focusOnMount}
              className="h-7 text-sm flex-1"
              placeholder="Term"
              value={active.draft.text}
              onChange={e => editApi.setAddDraft({ text: e.target.value })}
              onKeyDown={commitKeys}
            />
            <div className="w-32 shrink-0">
              <Combobox value={active.draft.status} onChange={v => editApi.setAddDraft({ status: v })} options={STATUS_OPTIONS} className="h-7" />
            </div>
            <Button variant="ghost" size="icon-xs" type="button" aria-label="Save term" onClick={editApi.commit} className="text-green-600 hover:text-green-700">
              <Check className="w-4 h-4" />
            </Button>
            <Button variant="ghost" size="icon-xs" type="button" aria-label="Cancel add term" onClick={editApi.cancel} className="text-gray-400 hover:text-gray-600">
              <X className="w-4 h-4" />
            </Button>
          </div>
        </>
      ) : (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={e => { e.stopPropagation(); editApi.startAdd(concept.id) }}
          startIcon={<Plus className="size-3" />}
          className="col-span-3 mt-1 justify-start text-xs text-gray-400 hover:text-primary"
        >
          Add term
        </Button>
      )}
    </div>
  )
}
