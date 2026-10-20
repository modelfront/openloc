import { useState, useEffect, useRef, useMemo, type ReactNode } from 'react'
import { baseLanguage } from '@/lib/lang-pair'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DeleteButton } from '@/components/ui/delete-button'
import { Combobox, createAction, type ComboboxAction } from '@/components/ui/combobox'
import { HelpTip } from '@/components/ui/help-tip'
import { langPickerProps, toLangOptions } from './lang-options'
import { DragHandle } from '@/components/ui/drag-handle'
import { modeHasRead, modeHasWrite, toggleMode } from '@/lib/tm-mode'
import { useDebouncedCallback } from '../hooks/useDebouncedCallback'
import { LanguagePair } from '@/components/ui/language-code'
import { SaveStatus } from '@/components/ui/save-status'
import { errorText } from '@/lib/error-text'
import { useSavedFlash } from '@/hooks/useSavedFlash'

/**
 * Context passed to `onCreateNewResource` so the parent can pre-fill dialogs
 * (e.g. a Term base dialog pre-fills source/target from the row being edited).
 */
export interface CreateResourceContext {
  sourceLang: string
  targetLang: string
}

/**
 * Result returned by `onCreateNewResource`. When the result includes langs
 * (e.g. a Term base has them baked in), AssignmentSection pre-fills the
 * Add-row form with those langs too.
 */
export interface CreateResourceResult {
  id: number
  sourceLang?: string
  targetLang?: string
}

export interface AssignmentItem {
  id: number
  resourceId: number
  resourceName: string
  sourceLang: string
  targetLang: string
  mode?: string
  priority?: number
  /** MT only: distinguishes a classic MT engine ('mt') from an AI
   *  engine used as MT ('ai'), since the two id-spaces overlap. Undefined for
   *  TM / terminology sections, which fall back to the plain numeric id. */
  engineKind?: string
}

const baseEq = (a?: string, b?: string) => !!a && !!b && baseLanguage(a) === baseLanguage(b)

// Resources may carry a `kind` (MT: 'mt' | 'ai'). When they do, the picker keys
// options by `kind:id` so a classic engine and an AI engine with the same
// numeric id don't collide. Without a kind it degrades to the plain id string,
// keeping TM / terminology behaviour byte-for-byte identical.
export const resKey = (kind: string | undefined, id: number) => (kind ? `${kind}:${id}` : String(id))
export function parseResKey(v: string): { kind?: string; id: number } {
  const i = v.indexOf(':')
  if (i === -1) return { id: Number(v) }
  return { kind: v.slice(0, i), id: Number(v.slice(i + 1)) }
}

export function AssignmentSection({ title, helpText, items, resources, resourceActions, resourceLabel, hasMode, hasPriority, onSave, initialEditing, onDraftChange, languages, fetchLanguagePairs, onCreateNewResource, createNewLabel, autoSourceLanguage, restrictTargetsTo, resourceLangs, headerContent, emptyStateText }: {
  title: string
  helpText?: string
  items: AssignmentItem[]
  /** `meta` is the option's trailing chip (e.g. an AI engine's provider/model/mode). */
  resources: { id: number; name: string; kind?: string; meta?: ReactNode }[]
  /** Pinned "New …" rows for the kind-aware (MT) add-row picker. The
   *  numeric-id sections use `onCreateNewResource` instead. */
  resourceActions?: ComboboxAction[]
  resourceLabel: string
  /** Extra content rendered inside the section, between the title and the table
   *  (e.g. the MT "Default engine" selector, so it sits under the block heading). */
  headerContent?: ReactNode
  /** Overrides the "No {title} assigned yet." empty-state line. Used by MT to say
   *  the default engine already covers everything when there are no overrides. */
  emptyStateText?: string
  hasMode?: boolean
  hasPriority?: boolean
  onSave?: (items: AssignmentItem[]) => Promise<void>
  initialEditing?: boolean
  onDraftChange?: (items: AssignmentItem[]) => void
  languages?: { code: string; name: string }[]
  fetchLanguagePairs?: (resourceId: number) => Promise<{ source_language: string; target_language: string }[]>
  /**
   * When provided, the Add-row resource dropdown shows a "+ Create new..." entry.
   * Called with the current Add-row lang context (source/target). Should return
   * the newly-created resource (and optionally its languages, for pre-filling),
   * or null if the user cancelled.
   */
  onCreateNewResource?: (ctx: CreateResourceContext) => Promise<CreateResourceResult | null>
  /** Label shown on the "+ Create new..." item (e.g. "Create new TM..."). */
  createNewLabel?: string
  /** MT: when set, hide the Source column entirely and auto-fill new rows with
   *  this language (the project's source). Falsy = show/edit source. */
  autoSourceLanguage?: string
  /** MT: limit the add-row Target options to these language codes (the project's
   *  targets), minus any already assigned. */
  restrictTargetsTo?: string[]
  /** Terminology: the source + derived target languages of each resource (term
   *  base). When set, the section switches to "resource mode" — the add/create
   *  control moves to the title (like TM), languages are shown read-only, and
   *  there's no in-table add-row. Picker is filtered to resources whose source
   *  matches `autoSourceLanguage` (project source) AND whose target_languages
   *  overlap the project targets (`restrictTargetsTo`); a just-created base with
   *  no terms yet is still offered. */
  resourceLangs?: Record<number, { source_language: string; target_languages: string[] }>
}) {
  const [rows, setRows] = useState<AssignmentItem[]>(items)
  const [saving, setSaving] = useState(false)
  const prevItemsRef = useRef(items)

  // "Add new row" form state
  const [newResourceId, setNewResourceId] = useState('')
  const [newSrc, setNewSrc] = useState('')
  const [newTgt, setNewTgt] = useState('')
  // Picking a resource in the add-row. When the pair comes from the resource,
  // the previous pick's pair is cleared so it can't be submitted with the new one.
  const selectAddResource = (id: string) => {
    setNewResourceId(id)
    if (fetchLanguagePairs) { setNewSrc(''); setNewTgt('') }
  }
  const [newMode, setNewMode] = useState('read_write')

  // Refs for tab flow (source → target)
  const tgtComboRef = useRef<HTMLDivElement>(null)

  // Whether the resource list carries a kind discriminator (MT: classic vs AI).
  const hasKinds = useMemo(() => resources.some(r => r.kind), [resources])

  // TM language filter: fetch language pairs when resource is selected. AI-as-MT
  // engines have no per-pair catalog (LLMs translate any pair), so skip the fetch
  // for them and leave the target list unrestricted.
  const [langPairs, setLangPairs] = useState<{ source_language: string; target_language: string }[]>([])
  useEffect(() => {
    if (!fetchLanguagePairs || !newResourceId) {
      setLangPairs([])
      return
    }
    const { kind, id } = parseResKey(newResourceId)
    if (kind === 'ai') {
      setLangPairs([])
      return
    }
    fetchLanguagePairs(id)
      .then(setLangPairs)
      .catch(() => setLangPairs([]))
  }, [fetchLanguagePairs, newResourceId])

  const filteredSourceLangs = useMemo(() => {
    if (!languages || langPairs.length === 0) return languages
    const codes = new Set(langPairs.map(p => p.source_language))
    return languages.filter(l => codes.has(l.code))
  }, [languages, langPairs])

  const filteredTargetLangs = useMemo(() => {
    if (!languages || langPairs.length === 0) return languages
    const codes = new Set(langPairs.map(p => p.target_language))
    return languages.filter(l => codes.has(l.code))
  }, [languages, langPairs])

  // MT: source comes from the project (no per-row source picker).
  const hideSource = !!autoSourceLanguage

  // MT add-row: offer exactly the project's target languages that don't yet have
  // an engine — mapped to their own codes (not locale variants), and intersected
  // with the engine-supported targets (via filteredTargetLangs) once an
  // engine is picked. Non-MT sections pass no restriction and keep the full list.
  const addTargetLangs = useMemo(() => {
    if (!restrictTargetsTo) return filteredTargetLangs ?? languages ?? []
    const byCode = new Map((languages ?? []).map(l => [l.code, l]))
    const assigned = new Set(rows.map(r => baseLanguage(r.targetLang)))
    // Engine-supported set (only meaningful once a resource is selected).
    const supported = fetchLanguagePairs && langPairs.length > 0
      ? new Set(langPairs.map(p => baseLanguage(p.target_language)))
      : null
    return restrictTargetsTo
      .filter(code => !assigned.has(baseLanguage(code)))
      .filter(code => !supported || supported.has(baseLanguage(code)))
      .map(code => byCode.get(code) ?? { code, name: code })
  }, [restrictTargetsTo, filteredTargetLangs, languages, rows, fetchLanguagePairs, langPairs])

  // MT: once every project target has an engine there's nothing left to add, so
  // hide the add-row entirely. Based on assigned targets only (not the engine
  // typed into the add-row), so it doesn't flicker while picking. Non-MT
  // sections (no restriction) always show the add-row.
  const canAddRow = useMemo(() => {
    if (!restrictTargetsTo) return true
    const assigned = new Set(rows.map(r => baseLanguage(r.targetLang)))
    return restrictTargetsTo.some(code => !assigned.has(baseLanguage(code)))
  }, [restrictTargetsTo, rows])

  // Terminology "resource mode": the resource declares its own languages, so the
  // add/create control lives in the title (like TM) and rows show languages
  // read-only. Picker offers source-matching, not-yet-assigned resources.
  const resourceMode = !!resourceLangs
  const assignedResourceIds = useMemo(() => new Set(rows.map(r => r.resourceId)), [rows])
  const pickerResources = useMemo(() => {
    if (!resourceMode) return resources
    return resources
      .filter(r => !assignedResourceIds.has(r.id))
      // Source must match the project source…
      .filter(r => !autoSourceLanguage || baseEq(resourceLangs![r.id]?.source_language, autoSourceLanguage))
      // …and at least one of the base's target languages must be a project
      // target. A brand-new base with no terms yet (empty target_languages) is
      // still offered so the create-then-assign flow works.
      .filter(r => {
        if (!restrictTargetsTo) return true
        const tbTargets = resourceLangs![r.id]?.target_languages ?? []
        if (tbTargets.length === 0) return true
        return tbTargets.some(t => restrictTargetsTo.some(p => baseEq(p, t)))
      })
  }, [resourceMode, resources, assignedResourceIds, autoSourceLanguage, restrictTargetsTo, resourceLangs])

  // Languages for just-created resources: they aren't in the `resourceLangs`
  // prop yet (the list query hasn't refetched), so the create flow stashes them
  // here for addResource to read. (The create action hands the new id to
  // addResource itself, so the add happens once — not inside the create flow.)
  const createdLangsRef = useRef<Record<number, { source_language: string; target_languages: string[] }>>({})

  const addResource = (id: number) => {
    if (rows.some(r => r.resourceId === id)) return
    const lang = createdLangsRef.current[id] ?? resourceLangs?.[id]
    const resource = resources.find(r => r.id === id)
    const src = (lang?.source_language ?? autoSourceLanguage ?? '').toLowerCase()
    // Fan out: one assignment row per project target this base also covers, so a
    // multilingual base enforces terminology across EVERY shared target — not just
    // the first (a base covering de+fr assigned to an en→{de,fr,es} project gets
    // both an en→de and an en→fr row). Targets are stored as the project's codes
    // (lookup base-subtag-matches them against the job). A brand-new base with no
    // terms yet could hold any of the project's targets once filled, so it fans
    // out to ALL of them (create-then-fill); if a base has terms but none overlap,
    // fall back to the base's first target.
    const tbTargets = lang?.target_languages ?? []
    const projTargets = restrictTargetsTo ?? []
    let targets: string[]
    if (tbTargets.length === 0) {
      targets = projTargets.length ? projTargets : ['']
    } else {
      const overlap = projTargets.filter(p => tbTargets.some(t => baseEq(p, t)))
      targets = overlap.length ? overlap : [tbTargets[0] ?? '']
    }
    const newRows = [...rows, ...targets.map((tgt, i) => ({
      id: 0,
      resourceId: id,
      resourceName: resource?.name ?? '',
      sourceLang: src,
      targetLang: tgt.toLowerCase(),
      mode: hasMode ? 'read_write' : undefined,
      priority: hasPriority ? rows.length + i : undefined,
    }))]
    setRows(newRows)
    autoSave(newRows)
  }

  // Sync items when they change externally (e.g. template applied, data refetched)
  useEffect(() => {
    if (items !== prevItemsRef.current) {
      prevItemsRef.current = items
      setRows(items)
    }
  }, [items])

  // Persist changes to the backend. In edit mode we debounce the actual PUT so
  // rapid successive changes (add row, toggle mode, set priority) collapse into
  // a single request with the final state — this prevents the full-replace race
  // where overlapping PUTs interleave DELETE + INSERT and leave the server with
  // the wrong set.
  const [savedFlash, flashSaved] = useSavedFlash()

  const persistToBackend = useDebouncedCallback(async (newRows: AssignmentItem[]) => {
    if (!onSave) return
    try {
      await onSave(newRows)
      flashSaved()
    } catch (err) {
      toast.error(errorText(err, 'Failed to save the assignments.'))
    } finally {
      setSaving(false)
    }
  }, 400)

  // Flush any pending debounced save before the component unmounts so we don't
  // lose the last change when the user navigates away mid-debounce.
  useEffect(() => {
    return () => persistToBackend.flush()
  }, [persistToBackend])

  const autoSave = (newRows: AssignmentItem[]) => {
    if (initialEditing && onDraftChange) {
      // Create mode: parent tracks draft state synchronously via onChange — never debounced.
      onDraftChange(newRows)
    } else if (onSave) {
      // Edit mode: show the "Saving..." indicator immediately for honest UX,
      // then let the debounced write actually hit the backend.
      setSaving(true)
      persistToBackend(newRows)
    }
  }

  const updateRow = <K extends keyof AssignmentItem>(idx: number, field: K, value: AssignmentItem[K]) => {
    const newRows = rows.map((r, i) => i === idx ? { ...r, [field]: value } : r)
    setRows(newRows)
    autoSave(newRows)
  }

  const removeRow = (idx: number) => {
    const filtered = rows.filter((_, i) => i !== idx)
    // When priority is positional (drag-to-sort), keep it 0..n-1 = row order.
    const newRows = hasPriority ? filtered.map((r, i) => ({ ...r, priority: i })) : filtered
    setRows(newRows)
    autoSave(newRows)
  }

  // Drag-to-SORT — only when priority is positional (Terminology). Mirrors
  // TMAssignmentSection: reflow live on drag-over, reindex priority + save on
  // drop. Native HTML5 DnD, handle-armed. MT has no priority so it never drags.
  const [dragIdx, setDragIdx] = useState<number | null>(null)
  const [dragArmed, setDragArmed] = useState(false)

  const reorder = (from: number, to: number) => {
    setRows(prev => {
      if (from === to || from < 0 || to < 0 || from >= prev.length || to >= prev.length) return prev
      const next = [...prev]
      const [moved] = next.splice(from, 1)
      next.splice(to, 0, moved)
      return next
    })
  }

  const commitOrder = () => {
    setRows(prev => {
      const reindexed = prev.map((r, i) => ({ ...r, priority: i }))
      autoSave(reindexed)
      return reindexed
    })
  }

  const addRow = () => {
    // MT auto-fills source from the project; other sections take it from the form.
    const src = hideSource ? (autoSourceLanguage ?? '') : newSrc.trim()
    if (!newResourceId || !src || !newTgt.trim()) return
    const { kind, id } = parseResKey(newResourceId)
    const resource = resources.find(r => r.id === id && (r.kind ?? undefined) === (kind ?? undefined))
    const newRows = [...rows, {
      id: 0,
      resourceId: id,
      engineKind: kind,
      resourceName: resource?.name ?? '',
      sourceLang: src.toLowerCase(),
      targetLang: newTgt.trim().toLowerCase(),
      mode: hasMode ? newMode : undefined,
      // Positional priority: a new row lands at the bottom (checked last).
      priority: hasPriority ? rows.length : undefined,
    }]
    setRows(newRows)
    autoSave(newRows)
    setNewResourceId('')
    setNewSrc('')
    setNewTgt('')
    setNewMode('read_write')
  }

  // Tab flow: when source lang is selected, focus target lang combobox
  const handleSrcSelect = (code: string) => {
    setNewSrc(code)
    // Focus the target combobox after a tick (let React re-render)
    setTimeout(() => {
      const btn = tgtComboRef.current?.querySelector('button')
      btn?.click() // open the combobox
    }, 50)
  }

  // Grid columns based on which optional fields are present. When priority is
  // present it's positional (drag handle up front); no numeric priority column.
  // Mode = two independent Read/Write checkbox columns.
  // Source/Target columns are wide enough for "en — English" style labels.
  const gridCols = resourceMode
    // resource | languages (read-only) | [read] [write] | remove
    ? `${hasPriority ? '44px ' : ''}minmax(200px, 1fr) 1fr${hasMode ? ' 70px 70px' : ''} 52px`
    : `${hasPriority ? '44px ' : ''}1fr ${hideSource ? '' : '160px '}160px${hasMode ? ' 70px 70px' : ''} 52px`

  // Toggle a Read/Write checkbox for row `idx`, keeping at least one checked.
  const toggleModeRow = (idx: number, which: 'read' | 'write') => {
    const next = toggleMode(rows[idx]?.mode, which)
    if (next === null) return
    updateRow(idx, 'mode', next)
  }

  // Toggle a Read/Write checkbox on the add-row form (default read_write).
  const toggleNewMode = (which: 'read' | 'write') => {
    const next = toggleMode(newMode, which)
    if (next !== null) setNewMode(next)
  }

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4">
      <div className="flex max-md:flex-wrap items-center justify-between gap-3 mb-3">
        <div className="flex items-center gap-1">
          <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">{title}</h3>
          {helpText && <HelpTip text={helpText} />}
        </div>
        <div className="flex items-center gap-3">
          <SaveStatus saving={saving} saved={savedFlash} />
          {/* Resource mode (Terminology): add/create control lives here, like TM. */}
          {resourceMode && (
            <Combobox
              options={pickerResources.map(r => ({ value: String(r.id), label: r.name }))}
              value=""
              onChange={(id) => addResource(Number(id))}
              actions={onCreateNewResource ? [createAction(createNewLabel ?? 'Create new...', async () => {
                const result = await onCreateNewResource({ sourceLang: autoSourceLanguage ?? '', targetLang: '' })
                if (!result) return null
                // Stash the created resource's langs so the ensuing add shows the right pair.
                if (result.sourceLang && result.targetLang) {
                  createdLangsRef.current[result.id] = { source_language: result.sourceLang ?? '', target_languages: result.targetLang ? [result.targetLang] : [] }
                }
                return result.id
              }, addResource)] : undefined}
              placeholder={`Add ${resourceLabel}`}
              className="w-56"
              data-testid="resource-select"
            />
          )}
        </div>
      </div>

      {headerContent && <div className="mb-3">{headerContent}</div>}

      {/* Table: header, existing rows, and the add-row (a darker footer line). */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 mb-3 max-md:overflow-x-auto max-md:overflow-y-hidden">
          {/* Header */}
          <div
            className="gap-0 bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider rounded-t-lg"
            style={{ display: 'grid', gridTemplateColumns: gridCols }}
          >
            {hasPriority && <div className="px-1 py-2"></div>}
            <div className="px-3 py-2">{resourceLabel}</div>
            {resourceMode ? (
              <div className="px-3 py-2">Languages</div>
            ) : (
              <>
                {!hideSource && <div className="px-3 py-2">Source</div>}
                <div className="px-3 py-2">Target</div>
              </>
            )}
            {hasMode && (
              <>
                <div className="px-2 py-2 flex items-center justify-center">Read</div>
                <div className="px-2 py-2 flex items-center justify-center">Write</div>
              </>
            )}
            <div className="px-3 py-2"></div>
          </div>

          {rows.length === 0 && (
            <div className="px-3 py-3 text-sm text-gray-400 dark:text-gray-500 border-b border-gray-100 dark:border-gray-800">
              {emptyStateText ?? `No ${title.toLowerCase()} assigned yet.`}
            </div>
          )}

          {/* Data rows */}
          {rows.map((item, idx) => (
            <div
              // Stable key by id (saved rows) so a live drag reflow MOVES the DOM
              // node instead of remounting it; unsaved rows fall back to index.
              key={item.id > 0 ? `a${item.id}` : `n${idx}`}
              className={`gap-0 border-b border-gray-100 dark:border-gray-800 items-center ${
                hasPriority && dragIdx === idx ? 'opacity-50' : ''
              }`}
              style={{ display: 'grid', gridTemplateColumns: gridCols }}
              draggable={hasPriority && dragArmed}
              onDragStart={hasPriority ? (e) => { e.stopPropagation(); setDragIdx(idx); e.dataTransfer.effectAllowed = 'move' } : undefined}
              onDragEnter={hasPriority ? (e) => { e.preventDefault(); e.stopPropagation(); if (dragIdx === null || dragIdx === idx) return; reorder(dragIdx, idx); setDragIdx(idx) } : undefined}
              onDragOver={hasPriority ? (e) => { e.preventDefault(); e.stopPropagation() } : undefined}
              onDrop={hasPriority ? (e) => { e.preventDefault(); e.stopPropagation() } : undefined}
              onDragEnd={hasPriority ? (e) => { e.stopPropagation(); commitOrder(); setDragIdx(null); setDragArmed(false) } : undefined}
            >
              {hasPriority && (
                <div className="flex items-center justify-center py-2 pl-1">
                  <DragHandle
                    title="Drag to reorder (top = checked first)"
                    onMouseDown={() => setDragArmed(true)}
                    onMouseUp={() => setDragArmed(false)}
                    data-testid={`assignment-drag-${item.resourceId}-${item.sourceLang}-${item.targetLang}`}
                  />
                </div>
              )}
              <div className="px-3 py-2 min-w-0">
                {resourceMode ? (
                  <span className="text-sm text-gray-900 dark:text-gray-100 truncate block">
                    {resources.find(r => r.id === item.resourceId)?.name || item.resourceName || `#${item.resourceId}`}
                  </span>
                ) : (
                  <Combobox
                    value={resKey(item.engineKind, item.resourceId)}
                    onChange={v => {
                      const { kind, id } = parseResKey(v)
                      const res = resources.find(r => r.id === id && (r.kind ?? undefined) === (kind ?? undefined))
                      const newRows = rows.map((r, i) => i === idx ? { ...r, resourceId: id, engineKind: kind, resourceName: res?.name ?? r.resourceName } : r)
                      setRows(newRows)
                      autoSave(newRows)
                    }}
                    options={[
                      ...resources.map(r => ({ value: resKey(r.kind, r.id), label: r.name, chip: r.meta })),
                      ...(!resources.find(r => r.id === item.resourceId && (r.kind ?? undefined) === (item.engineKind ?? undefined))
                        ? [{ value: resKey(item.engineKind, item.resourceId), label: item.resourceName || `#${item.resourceId}` }]
                        : []),
                    ]}
                    className="w-full px-1 py-0.5 text-xs"
                    contentClassName="w-[26rem]"
                  />
                )}
              </div>
              {resourceMode ? (
                <div className="px-3 py-2">
                  <LanguagePair source={item.sourceLang} targets={item.targetLang} className="text-xs text-gray-500 dark:text-gray-400" />
                </div>
              ) : (
                <>
                  {!hideSource && (
                    <div className="px-3 py-2">
                      {languages ? (
                        <Combobox
                          options={toLangOptions(languages)}
                          value={item.sourceLang}
                          onChange={code => updateRow(idx, 'sourceLang', code)}
                          placeholder="Source"
                          className="w-full"
                          {...langPickerProps}
                        />
                      ) : (
                        <Input
                          value={item.sourceLang}
                          onChange={e => updateRow(idx, 'sourceLang', e.target.value)}
                          placeholder="src"
                          // A clear X on a 2–5 character language code costs
                          // more width than it saves keystrokes.
                          clearable={false}
                          className="w-full h-7 px-1.5 uppercase"
                          inputClassName="text-xs"
                        />
                      )}
                    </div>
                  )}
                  <div className="px-3 py-2">
                    {languages ? (
                      <Combobox
                        options={toLangOptions(languages)}
                        value={item.targetLang}
                        onChange={code => updateRow(idx, 'targetLang', code)}
                        placeholder="Target"
                        className="w-full"
                        {...langPickerProps}
                      />
                    ) : (
                      <Input
                        value={item.targetLang}
                        onChange={e => updateRow(idx, 'targetLang', e.target.value)}
                        placeholder="tgt"
                        clearable={false}
                        className="w-full h-7 px-1.5 uppercase"
                        inputClassName="text-xs"
                      />
                    )}
                  </div>
                </>
              )}
              {hasMode && (
                <>
                  <div className="flex items-center justify-center py-2">
                    <input
                      type="checkbox"
                      checked={modeHasRead(item.mode ?? 'read_write')}
                      onChange={() => toggleModeRow(idx, 'read')}
                      aria-label="Read"
                      data-testid={`assignment-read-${item.resourceId}-${item.sourceLang}-${item.targetLang}`}
                    />
                  </div>
                  <div className="flex items-center justify-center py-2">
                    <input
                      type="checkbox"
                      checked={modeHasWrite(item.mode ?? 'read_write')}
                      onChange={() => toggleModeRow(idx, 'write')}
                      aria-label="Write"
                      data-testid={`assignment-write-${item.resourceId}-${item.sourceLang}-${item.targetLang}`}
                    />
                  </div>
                </>
              )}
              <div className="flex items-center justify-center py-2">
                <DeleteButton size="icon-xs" onClick={() => removeRow(idx)} aria-label="Remove" />
              </div>
            </div>
          ))}

          {/* Add-row: a darker footer line aligned to the columns. Hidden once
              every allowed target has an engine (MT), and entirely in resource
              mode (Terminology adds via the title control). No reorder handle
              here, never a drag target — a row can't be dropped below it. */}
          {!resourceMode && canAddRow && (
          <div
            data-testid={`assignment-add-row-${resourceLabel.toLowerCase().replace(/\s/g, '-')}`}
            className="gap-0 items-center bg-muted border-t border-gray-200 dark:border-gray-800 rounded-b-lg"
            style={{ display: 'grid', gridTemplateColumns: gridCols }}
          >
            {hasPriority && <div className="px-1 py-2"></div>}
            <div className="px-3 py-2">
              {hasKinds ? (
                // MT with classic + AI engines: options carry a composite
                // kind:id key instead of a bare numeric id.
                <Combobox
                  value={newResourceId}
                  onChange={(v) => {
                    setNewResourceId(v)
                    if (fetchLanguagePairs) { setNewSrc(''); setNewTgt('') }
                  }}
                  options={[
                    { value: '', label: `Select ${resourceLabel}` },
                    ...resources.map(r => ({ value: resKey(r.kind, r.id), label: r.name, chip: r.meta })),
                  ]}
                  actions={resourceActions}
                  className="w-full"
                  contentClassName="w-[26rem]"
                  data-testid={`assignment-add-resource-${resourceLabel.toLowerCase().replace(/\s/g, '-')}`}
                />
              ) : (
              <Combobox
                options={resources.map(r => ({ value: String(r.id), label: r.name }))}
                value={newResourceId}
                onChange={selectAddResource}
                actions={onCreateNewResource ? [createAction(createNewLabel ?? 'Create new...', async () => {
                  const result = await onCreateNewResource({ sourceLang: newSrc, targetLang: newTgt })
                  if (!result) return null
                  // If the created resource came with langs (e.g. a Term base has
                  // them baked in), pre-fill the Add-row so the user doesn't retype.
                  if (result.sourceLang) setNewSrc(result.sourceLang)
                  if (result.targetLang) setNewTgt(result.targetLang)
                  return result.id
                }, (id) => selectAddResource(String(id)))] : undefined}
                placeholder={`Select ${resourceLabel}`}
                className="w-full"
                data-testid="resource-select"
              />
              )}
            </div>
            {!hideSource && (
              <div className="px-3 py-2">
                {languages ? (
                  <Combobox options={toLangOptions(filteredSourceLangs ?? languages)} value={newSrc} onChange={handleSrcSelect} placeholder="Source" className="w-full" {...langPickerProps} />
                ) : (
                  <Input value={newSrc} onChange={e => setNewSrc(e.target.value)} placeholder="src" clearable={false} className="w-full h-7 px-1.5 uppercase" inputClassName="text-xs" />
                )}
              </div>
            )}
            <div className="px-3 py-2" ref={tgtComboRef}>
              {languages ? (
                <Combobox options={toLangOptions(addTargetLangs)} value={newTgt} onChange={setNewTgt} placeholder="Target" className="w-full" {...langPickerProps} />
              ) : (
                <Input value={newTgt} onChange={e => setNewTgt(e.target.value)} placeholder="tgt" clearable={false} className="w-full h-7 px-1.5 uppercase" inputClassName="text-xs" />
              )}
            </div>
            {hasMode && (
              <>
                <div className="flex items-center justify-center py-2">
                  <input type="checkbox" checked={modeHasRead(newMode)} onChange={() => toggleNewMode('read')} aria-label="Read" />
                </div>
                <div className="flex items-center justify-center py-2">
                  <input type="checkbox" checked={modeHasWrite(newMode)} onChange={() => toggleNewMode('write')} aria-label="Write" />
                </div>
              </>
            )}
            <div className="flex items-center justify-center py-2">
              <Button variant="ghost" size="icon-xs"
                onClick={addRow}
                disabled={!newResourceId || (!hideSource && !newSrc.trim()) || !newTgt.trim() || saving}
                aria-label="Add"
                title={`Add ${resourceLabel}`}
                className="text-primary hover:text-primary disabled:text-gray-300 dark:disabled:text-gray-600"
              >
                <Plus className="size-4" />
              </Button>
            </div>
          </div>
          )}
        </div>
    </div>
  )
}

