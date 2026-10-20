import { useState, useEffect, useRef, useMemo, type ReactNode } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { baseLanguage } from '@/lib/lang-pair'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { restrictToParentElement, restrictToVerticalAxis } from '@dnd-kit/modifiers'
import { toast } from 'sonner'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { HelpTip } from '@/components/ui/help-tip'
import { Combobox, createAction } from '@/components/ui/combobox'
import { DragHandle } from '@/components/ui/drag-handle'
import { modeHasRead, modeHasWrite, toggleMode } from '@/lib/tm-mode'
import { useDebouncedCallback } from '../../hooks/useDebouncedCallback'
import type { CreateResourceContext, CreateResourceResult } from '../AssignmentSection'
import { LanguagePair } from '@/components/ui/language-code'
import { Chip } from '@/components/ui/chip'
import { TM_LANGS_SHOWN } from '@/lib/tm-display'
import { PenaltyInput } from './PenaltyInput'
import { errorText } from '@/lib/error-text'
import { SaveStatus } from '@/components/ui/save-status'
import { useSavedFlash } from '@/hooks/useSavedFlash'

// TM assignments are per-TM. A TM declares its own source + target
// languages, so the assignment row carries no per-pair languages —
// it's just the TM + mode + priority. This is why TM has a dedicated section
// instead of the shared AssignmentSection (which is per-(source,target) and
// still used for MT / Terminology).
export interface TMAssignmentItem {
  /** 0 for a not-yet-persisted row. */
  id: number
  tmId: number
  mode: string
  priority: number
  // Deducted from every match out of this TM, 0–100. A penalised
  // match is also never auto-confirmed.
  penaltyPct?: number
}

/** A TM collection with its declared languages. */
export interface TMResource {
  id: number
  name: string
  source_language?: string | null
  target_languages?: string[] | null
}

const baseEq = (a?: string | null, b?: string | null) =>
  !!a && !!b && (a === b || baseLanguage(a) === baseLanguage(b))

export function TMAssignmentSection({
  items,
  tmCollections,
  projectSourceLanguage,
  projectTargetLanguages,
  initialEditing,
  onDraftChange,
  onSave,
  onCreateNewResource,
  createNewLabel,
  readOnly = false,
  variant = 'card',
  footer,
}: {
  items: TMAssignmentItem[]
  tmCollections: TMResource[]
  /** Filter the picker to TMs whose declared source matches this. Undefined
   *  (e.g. templates, which have no source language) disables the filter. */
  projectSourceLanguage?: string
  /** Used to warn when a TM covers none of the project's targets. */
  projectTargetLanguages?: string[]
  initialEditing?: boolean
  onDraftChange?: (items: TMAssignmentItem[]) => void
  onSave?: (items: TMAssignmentItem[]) => Promise<void>
  onCreateNewResource?: (ctx: CreateResourceContext) => Promise<CreateResourceResult | null>
  createNewLabel?: string
  /** The rows belong to a linked TM profile: shown, not editable. */
  readOnly?: boolean
  /**
   * `embedded` drops the card chrome so the parent's section owns the border,
   * and uses the smaller "Translation memories" heading.
   */
  variant?: 'card' | 'embedded'
  /** Rendered inside the section, under the list (the TM lookup order). */
  footer?: ReactNode
}) {
  const [rows, setRows] = useState<TMAssignmentItem[]>(items)
  const [saving, setSaving] = useState(false)
  const [savedFlash, flashSaved] = useSavedFlash()
  // What the server holds: a refused save puts the rows back to this.
  const savedRowsRef = useRef(items)
  // The rows the last edit asks for. A save sends what this holds when its
  // turn comes, so a save queued behind a refused one never resends the
  // refused change.
  const intendedRowsRef = useRef(items)
  // Counts refused saves. An edit carries the count it was made under; one
  // made before a refusal was built on rows that refusal put back.
  const [refusals, setRefusals] = useState(0)
  const refusalsRef = useRef(0)

  // New items from the server replace the rows and both baselines.
  useOnChange([items], () => setRows(items))
  useEffect(() => {
    savedRowsRef.current = items
    intendedRowsRef.current = items
  }, [items])

  const tmById = useMemo(() => {
    const m = new Map<number, TMResource>()
    for (const t of tmCollections) m.set(t.id, t)
    return m
  }, [tmCollections])

  // Picker: only TMs whose declared source matches the project's source (base-
  // language aware). No project source (template) ⇒ no filter. Already-assigned
  // TMs are excluded so you can't add a duplicate (per-TM UNIQUE).
  const assignedIds = useMemo(() => new Set(rows.map(r => r.tmId)), [rows])
  const pickerResources = useMemo(() => {
    return tmCollections
      .filter(t => !assignedIds.has(t.id))
      .filter(t => !projectSourceLanguage || baseEq(t.source_language, projectSourceLanguage))
      .map(t => ({ id: t.id, name: t.name }))
  }, [tmCollections, assignedIds, projectSourceLanguage])

  // Every save replaces the whole list, so saves run one after another: two in
  // flight could land in either order and leave the older list.
  const saveChain = useRef<Promise<unknown>>(Promise.resolve())
  // Resolves to whether this change is on the server.
  const persist = (rowsWanted: TMAssignmentItem[], madeUnder: number): Promise<boolean> => {
    if (madeUnder !== refusalsRef.current) return Promise.resolve(false)
    intendedRowsRef.current = rowsWanted
    setSaving(true)
    const run = saveChain.current.then(async () => {
      const newRows = intendedRowsRef.current
      // Nothing new to send: an earlier save took this change with it, or a
      // refusal since put it back.
      if (newRows === savedRowsRef.current) return refusalsRef.current === madeUnder
      if (!onSave) return true
      try {
        await onSave(newRows)
        savedRowsRef.current = newRows
        flashSaved()
        return true
      } catch (err) {
        toast.error(errorText(err, 'Failed to save the translation memories'))
        refusalsRef.current++
        setRefusals(refusalsRef.current)
        intendedRowsRef.current = savedRowsRef.current
        setRows(savedRowsRef.current)
        return false
      }
    })
    saveChain.current = run
    run.finally(() => { if (saveChain.current === run) setSaving(false) })
    return run
  }

  const persistToBackend = useDebouncedCallback(
    (rowsWanted: TMAssignmentItem[], madeUnder: number) => { void persist(rowsWanted, madeUnder) },
    400,
  )

  useEffect(() => () => persistToBackend.flush(), [persistToBackend])

  const autoSave = (newRows: TMAssignmentItem[]) => {
    if (initialEditing && onDraftChange) {
      onDraftChange(newRows)
    } else if (onSave) {
      setSaving(true)
      persistToBackend(newRows, refusals)
    }
  }

  const updateRow = (idx: number, field: 'mode', value: string) => {
    const newRows = rows.map((r, i) => (i === idx ? { ...r, [field]: value } : r))
    setRows(newRows)
    autoSave(newRows)
  }

  // A row's penalty, already parsed and clamped by PenaltyInput, which has
  // waited for the pause in typing: it saves at once and reports whether the
  // save went through.
  const commitPenalty = (idx: number, pct: number): Promise<boolean> | undefined => {
    if (pct === (rows[idx]?.penaltyPct ?? 0)) return
    const newRows = rows.map((r, i) => (i === idx ? { ...r, penaltyPct: pct } : r))
    setRows(newRows)
    if (initialEditing && onDraftChange) {
      onDraftChange(newRows)
      return
    }
    if (!onSave) return
    persistToBackend.cancel()
    return persist(newRows, refusals)
  }

  // Toggle a Read/Write checkbox; keeps at least one checked (a TM with neither
  // does nothing, so we ignore a toggle that would clear both).
  const toggleModeRow = (idx: number, which: 'read' | 'write') => {
    const next = toggleMode(rows[idx]?.mode, which)
    if (next === null) return
    updateRow(idx, 'mode', next)
  }

  const removeRow = (idx: number) => {
    // Reindex priority so it stays 0..n-1 (= row order) after a removal.
    const newRows = rows.filter((_, i) => i !== idx).map((r, i) => ({ ...r, priority: i }))
    setRows(newRows)
    autoSave(newRows)
  }

  const addTM = (tmId: number) => {
    if (rows.some(r => r.tmId === tmId)) return
    // New TM appends at the bottom (lowest priority = checked last).
    const newRows = [...rows, { id: 0, tmId, mode: 'read_write', priority: rows.length, penaltyPct: 0 }]
    setRows(newRows)
    autoSave(newRows)
  }

  // Drag-to-sort via dnd-kit (pointer + keyboard). Priority is the row order
  // (0 = top = checked first); the list re-sorts and saves once, on drop.
  // Pointer activation needs a few px of travel so a click on the handle is
  // not a drag; the keyboard sensor gives Space / arrows / Space for free.
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const handleDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const from = rows.findIndex(r => r.tmId === active.id)
    const to = rows.findIndex(r => r.tmId === over.id)
    if (from < 0 || to < 0) return
    const reordered = reorderRows(rows, from, to)
    setRows(reordered)
    autoSave(reordered)
  }

  // Warn when a TM's declared targets don't overlap the project's targets — the
  // TM's source matched (it's in the picker) but it can't help this project's
  // pairs until its targets align. TMs with no declared targets are wildcards,
  // so they never warn.
  const targetOverlapMissing = (tm?: TMResource): boolean => {
    if (!tm || !projectTargetLanguages?.length) return false
    const tgts = tm.target_languages ?? []
    if (tgts.length === 0) return false
    const projBases = new Set(projectTargetLanguages.map(baseLanguage))
    return !tgts.some(t => projBases.has(baseLanguage(t)))
  }

  // Columns: [drag handle] [TM name] [languages] [read] [write] [penalty] [remove].
  // Read/Write are independent checkboxes.
  // Edge columns (handle, remove) get breathing room from the card walls.
  //
  // Name and languages each get a floor + 1fr, so they share the slack and the
  // name (which has `truncate`) gives way first — a fixed-width name column
  // pushes "EN → ES, FR, DE" under the Read checkbox.
  // The first column shows the lookup sequence: handle, 1-based number, and a
  // "Master" chip on the top row, so the order reads as an order and not as a
  // list that happens to be sortable.
  const gridCols = '112px minmax(200px, 1fr) minmax(140px, 1fr) 64px 64px 84px 48px'

  const embedded = variant === 'embedded'
  return (
    <div
      className={embedded ? '' : 'bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4'}
      data-testid="tm-assignments"
      data-readonly={readOnly ? 'true' : undefined}
    >
      <div className="flex max-md:flex-wrap items-center justify-between gap-3 mb-3">
        <div className="flex items-center gap-1">
          {!embedded && <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">Translation memory</h3>}
          {embedded && <h4 className="text-xs font-medium text-gray-700 dark:text-gray-300">Translation memories</h4>}
          <HelpTip text="Assign translation memories to this project. A TM applies to its own declared source → target languages (set on the TM), and only TMs whose source matches the project are offered. Drag to set the sequence — the top TM (Master) is checked first, and where several TMs hold a match of the same score the earlier one wins. Give a memory you trust less a penalty to rank its matches below the others." />
        </div>
        <div className="flex items-center gap-3">
          <SaveStatus saving={saving} saved={savedFlash} />
          {/* Add: pick a source-matching TM (no per-pair languages to choose).
              Hidden while the rows belong to a TM profile. */}
          {!readOnly && (
            <Combobox
              options={pickerResources.map(r => ({ value: String(r.id), label: r.name }))}
              value=""
              onChange={(id) => addTM(Number(id))}
              actions={onCreateNewResource ? [createAction(createNewLabel ?? 'Create new...', async () => {
                const result = await onCreateNewResource({ sourceLang: projectSourceLanguage ?? '', targetLang: '' })
                if (!result) return null
                addTM(result.id)
                return result.id
              }, addTM)] : undefined}
              placeholder="Add translation memory"
              className="w-56"
              data-testid="add-tm-select"
            />
          )}
        </div>
      </div>

      {rows.length > 0 ? (
        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 mb-3 overflow-x-auto overflow-y-hidden" data-testid="tm-assignment-table">
          <div
            className="bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider rounded-t-lg"
            style={{ display: 'grid', gridTemplateColumns: gridCols }}
          >
            <div className="px-3 py-2 flex items-center gap-1">
              Sequence
              <HelpTip text="The order translation memories are checked in. The Master (first) is looked up first; a lower TM is only consulted for segments the ones above it did not match, and ties go to the earlier TM." />
            </div>
            <div className="px-3 py-2">Translation memory</div>
            <div className="px-3 py-2">Languages</div>
            <div className="px-2 py-2 flex items-center justify-center">Read</div>
            <div className="px-2 py-2 flex items-center justify-center">Write</div>
            <div className="px-2 py-2 flex items-center justify-center gap-1">
              Penalty
              <HelpTip text="Subtracted from the score of every match out of this TM, so you can rank a client-provided or legacy memory below your own. A match that loses any points is also filled in for review rather than approved — even when the source text is identical. Leave it at 0% for memories you trust." />
            </div>
            <div className="px-3 py-2"></div>
          </div>

          {/* Locked to the vertical axis and to the list box: a row that follows
              the pointer sideways or past the last row would poke out of the
              scroll wrapper and grow scrollbars mid-drag. */}
          <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis, restrictToParentElement]} onDragEnd={handleDragEnd}>
          <SortableContext items={rows.map(r => r.tmId)} strategy={verticalListSortingStrategy}>
          {rows.map((item, idx) => {
            const tm = tmById.get(item.tmId)
            const warn = targetOverlapMissing(tm)
            return (
              <SortableRow key={item.tmId} id={item.tmId} disabled={readOnly} gridCols={gridCols} testId={`tm-assignment-row-${item.tmId}`}>
                {({ handleRef, handleProps }) => (<>
                <div className="flex items-center gap-2 py-2 pl-2" data-testid={`tm-assignment-sequence-${item.tmId}`}>
                  {!readOnly && <DragHandle
                    ref={handleRef}
                    {...handleProps}
                    title="Drag to reorder (top = checked first). Keyboard: Space, arrows, Space."
                    data-testid={`tm-assignment-drag-${item.tmId}`}
                  />}
                  <span className="w-4 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">{idx + 1}</span>
                  {idx === 0 && (
                    <span
                      className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide bg-primary/10 text-primary"
                      title="Checked first; its matches win ties."
                      data-testid="tm-assignment-master"
                    >
                      Master
                    </span>
                  )}
                </div>
                <div className="px-3 py-2 text-sm text-gray-900 dark:text-gray-100 truncate">
                  {tm?.name ?? `TM #${item.tmId}`}
                </div>
                <div className="px-3 py-2 min-w-0">
                  <TMLangs tm={tm} warn={warn} />
                </div>
                <div className="flex items-center justify-center py-2">
                  <input
                    type="checkbox"
                    checked={modeHasRead(item.mode)}
                    onChange={() => toggleModeRow(idx, 'read')}
                    disabled={readOnly}
                    aria-label="Read"
                    data-testid={`tm-assignment-read-${item.tmId}`}
                  />
                </div>
                <div className="flex items-center justify-center py-2">
                  <input
                    type="checkbox"
                    checked={modeHasWrite(item.mode)}
                    onChange={() => toggleModeRow(idx, 'write')}
                    disabled={readOnly}
                    aria-label="Write"
                    data-testid={`tm-assignment-write-${item.tmId}`}
                  />
                </div>
                <div className="flex items-center justify-center py-2">
                  {readOnly ? (
                    <span className="text-sm text-gray-700 dark:text-gray-300" data-testid={`tm-assignment-penalty-${item.tmId}`}>{item.penaltyPct ?? 0}%</span>
                  ) : (
                  <PenaltyInput
                    pct={item.penaltyPct ?? 0}
                    label={`Penalty for ${tm?.name ?? `TM #${item.tmId}`}`}
                    testId={`tm-assignment-penalty-${item.tmId}`}
                    onSave={pct => commitPenalty(idx, pct)}
                  />
                  )}
                </div>
                <div className="flex items-center justify-center py-2 pr-3">
                  {!readOnly && <DeleteButton size="icon-xs" onClick={() => removeRow(idx)} aria-label="Remove" data-testid={`tm-assignment-remove-${item.tmId}`} />}
                </div>
                </>)}
              </SortableRow>
            )
          })}
          </SortableContext>
          </DndContext>
        </div>
      ) : (
        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 mb-3 overflow-x-auto overflow-y-hidden">
          <div
            className="bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider rounded-t-lg"
            style={{ display: 'grid', gridTemplateColumns: gridCols }}
          >
            <div className="px-3 py-2">Sequence</div>
            <div className="px-3 py-2">Translation memory</div>
            <div className="px-3 py-2">Languages</div>
            <div className="px-2 py-2 flex items-center justify-center">Read</div>
            <div className="px-2 py-2 flex items-center justify-center">Write</div>
            <div className="px-2 py-2 flex items-center justify-center">Penalty</div>
            <div className="px-3 py-2"></div>
          </div>
          <div className="flex flex-wrap items-center gap-3 px-3 py-3 text-sm text-gray-400 dark:text-gray-500" data-testid="tm-assignments-empty">
            <span>{readOnly ? 'The TM profile has no translation memories yet.' : 'No translation memories yet.'}</span>
          </div>
        </div>
      )}

      {!readOnly && projectSourceLanguage && pickerResources.length === 0 && (
        <p data-testid="tm-assignment-add-row" className="text-xs text-gray-400 dark:text-gray-500">
          No unassigned TMs with source <span className="font-mono uppercase">{projectSourceLanguage}</span>.
        </p>
      )}
      {footer && <div className="mt-1">{footer}</div>}
    </div>
  )
}

// Read-only display of a TM's declared source → targets (first 5 + "+N"),
// mirroring the TM list. Shows an amber warning when the TM covers none of the
// project's target languages.
function TMLangs({ tm, warn }: { tm?: { source_language?: string | null; target_languages?: string[] | null }; warn: boolean }) {
  const src = tm?.source_language
  const tgts = tm?.target_languages ?? []
  if (!src || tgts.length === 0) {
    return <span className="text-xs text-gray-400 dark:text-gray-500">no declared languages</span>
  }
  return (
    <span className="inline-flex items-center gap-1 min-w-0">
      <span className="text-xs text-gray-500 dark:text-gray-400 truncate">
        <LanguagePair source={src} targets={tgts} max={TM_LANGS_SHOWN} />
      </span>
      {warn && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Chip
                color="yellow"
                data-testid="tm-assignment-no-overlap"
                className="shrink-0 cursor-help px-1"
                tabIndex={0}
              >
                ⚠
              </Chip>
            }
          />
          <TooltipContent className="max-w-64 text-xs leading-relaxed">
            No target language in common with this project — this TM won't be used until its targets align.
          </TooltipContent>
        </Tooltip>
      )}
    </span>
  )
}


/** Moves the row at `from` to `to` and renumbers priority to match the order. */
export function reorderRows(rows: TMAssignmentItem[], from: number, to: number): TMAssignmentItem[] {
  return arrayMove(rows, from, to).map((r, i) => ({ ...r, priority: i }))
}

// One sortable grid row. dnd-kit owns the transform/transition (rows slide
// out of the way while dragging) and the activator is the grip button, so a
// click anywhere else in the row never starts a drag. The dragged row is the
// placeholder: tinted + outlined, fully opaque.
function SortableRow({ id, disabled, gridCols, testId, children }: {
  id: number
  disabled: boolean
  gridCols: string
  testId: string
  children: (h: { handleRef: (el: HTMLElement | null) => void; handleProps: Record<string, unknown> }) => ReactNode
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id, disabled })
  return (
    <div
      ref={setNodeRef}
      data-testid={testId}
      data-dragging={isDragging ? 'true' : undefined}
      className={`border-b border-gray-100 dark:border-gray-800 items-center ${
        isDragging ? 'relative z-10 bg-primary/10 ring-1 ring-inset ring-primary/50' : ''
      }`}
      style={{ display: 'grid', gridTemplateColumns: gridCols, transform: CSS.Transform.toString(transform), transition }}
    >
      {children({ handleRef: setActivatorNodeRef, handleProps: { ...attributes, ...listeners } })}
    </div>
  )
}
