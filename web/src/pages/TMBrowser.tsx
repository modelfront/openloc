import { useState, useRef, useCallback, useMemo, useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useInfiniteQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import { Extension } from '@tiptap/core'
import { listTMEntries, listTMCollections, listTMLanguagePairs, deleteTMEntry, importTMX, listTMImports, exportTMX, updateTMEntry, renameTMCollection, type TMEntry, type TMImport } from '../api'
import { EditableTitle } from '@/components/list/EditableTitle'
import { decodeLangPair } from '../lib/lang-pair'
import { useTMEntrySearch } from '../hooks/useTMEntrySearch'
import { tmEntryCountLabel } from '../lib/tm-entry-count'
import { TMEntryFilters } from '@/components/tm/TMEntryFilters'
import { ApiError } from '../api/request'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { invalidateTMContext, advanceTMEntryInCache, removeTMEntryFromCache } from '../lib/query-invalidation'
import { isSaveConflictError, getTMConflictPayload, type TMConflictPayload } from '../lib/save-conflict'
import { getMe } from '../api/auth'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { isDemoReadOnly } from '../lib/demo'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { DeleteButton } from '@/components/ui/delete-button'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { hoverActionClass } from '@/components/list/hover-action'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { VirtualDataTable } from '@/components/ui/virtual-data-table'
import { Upload, Download, Pencil, History, Replace } from 'lucide-react'
import { SegmentText, hasXliffTags, SEGMENT_PREVIEW_CLAMP } from '@/components/SegmentRow.shared'
import RichTextCellEditor, { type RichTextCellEditorHandle } from '@/components/RichTextCellEditor'
import ConflictBanner from '@/components/ConflictBanner'
import ConfirmDialog from '@/components/ConfirmDialog'
import TMEntryHistoryDialog from '@/components/TMEntryHistoryDialog'
import TMBulkFindReplaceModal from '@/components/TMBulkFindReplaceModal'
import MissingTagsBar from '@/components/MissingTagsBar'
import { getMissingTags, type MissingTag } from '@/lib/missing-tags'
import { LanguageCode } from '@/components/ui/language-code'
import { errorText } from '@/lib/error-text'
import { usePageTitle } from '../hooks/usePageTitle'

type EditingCell = { entryId: number; field: 'source' | 'target' }

const PAGE_SIZE = 200

export default function TMBrowser() {
  const { tmId } = useParams<{ tmId: string }>()
  const tid = Number(tmId)
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [selectedPair, setSelectedPair] = useState<string>('all')
  const search = useTMEntrySearch()
  const debouncedSearch = search.debouncedText
  // Options only matter while there is text, so they stay out of the key otherwise.
  const searchOptions = debouncedSearch ? search.options : undefined
  const [activeImport, setActiveImport] = useState<TMImport | null>(null)
  const [uploadProgress, setUploadProgress] = useState<number | null>(null) // 0-100 during upload, null when done
  const uploadAbortRef = useRef<(() => void) | null>(null)

  const { data: tms } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  const tm = tms?.find(t => t.id === tid)
  usePageTitle(tm?.name)

  const { data: langPairs } = useQuery({
    queryKey: queryKeys.tm.langPairs(tid),
    queryFn: () => listTMLanguagePairs(tid),
  })

  const decoded = decodeLangPair(selectedPair)
  const sourceLang = decoded?.source
  const targetLang = decoded?.target

  // TM-wide source language label shown once above the list. Derived from the
  // entries' distinct source languages; falls back to the
  // TM's declared source_language when the TM is still empty.
  const sourceLabel = useMemo(() => {
    const sources = Array.from(new Set((langPairs ?? []).map(p => p.source_language)))
    if (sources.length > 0) return sources.map(s => s.toUpperCase()).join(', ')
    return tm?.source_language ? tm.source_language.toUpperCase() : null
  }, [langPairs, tm?.source_language])
  const sourceCount = new Set((langPairs ?? []).map(p => p.source_language)).size

  const {
    data,
    error,
    isError,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
  } = useInfiniteQuery({
    queryKey: queryKeys.tm.entries(tid, sourceLang, targetLang, debouncedSearch, searchOptions),
    queryFn: ({ pageParam = 0 }) => listTMEntries(tid, {
      limit: PAGE_SIZE,
      cursor: pageParam,
      sourceLang,
      targetLang,
      search: debouncedSearch || undefined,
      searchOptions,
    }),
    enabled: !search.invalid,
    // A 400 is a pattern the server rejected; asking again won't change that.
    retry: (count, err) => !(err instanceof ApiError && err.status === 400) && count < 1,
    getNextPageParam: (lastPage) => lastPage.has_more ? lastPage.cursor : undefined,
    initialPageParam: 0,
    // Same key-swap flash as TerminologyBrowser: search is
    // in the query key, so without this every debounced keystroke blanks the
    // table until the refetch lands.
    placeholderData: keepPreviousData,
  })

  // A 400 is a search the server refused: a pattern Postgres rejects, a search
  // past the time limit, or an over-long text. Its message is a full sentence.
  const serverSearchError = error instanceof ApiError && error.status === 400 ? error.message : null
  const searchBroken = search.invalid || serverSearchError !== null
  // Any other failure with no data at all means the first page failed.
  const loadFailed = isError && !data && serverSearchError === null
  const { sourceHighlight, targetHighlight } = search

  // Flatten all pages into a single entries array. A broken pattern shows
  // nothing, not the previous search's rows.
  const allEntries = searchBroken ? [] : data?.pages.flatMap(p => p.data) ?? []
  // Undefined when a search did not count its matches (it ran too long).
  const totalCount = searchBroken ? 0 : data?.pages[0]?.total

  const deleteMut = useMutationWithToast({
    mutationFn: (entryId: number) => deleteTMEntry(tid, entryId),
    successMessage: 'Entry deleted',
    errorMessage: 'Delete failed',
    onSuccess: (_, entryId) => removeTMEntryFromCache(queryClient, tid, entryId),
  })

  const renameMut = useMutationWithToast({
    mutationFn: (name: string) => renameTMCollection(tid, name),
    successMessage: 'Translation memory renamed',
    errorMessage: 'Rename failed',
    invalidateKeys: [queryKeys.tms.all],
  })
  // React Query's useMutation returns a NEW mutation object identity on most
  // renders. Putting `deleteMut` itself into a useMemo's deps (e.g.
  // `columns`) cascades into column rebuilds → TanStack Table cell remount
  // → editor re-mount → user typing wiped. The `.mutate` method, by
  // contrast, is a stable reference, so the cell render's onClick handler
  // depends on it rather than on the unstable object.
  const deleteMutate = deleteMut.mutate

  // ─── Inline cell editing ──────────────────────────────────────────────
  // One cell editable at a time, mirroring the CAT editor's pattern:
  // capture `updated_at` baseline on edit start → send with PATCH → 409 →
  // side-by-side banner "Use theirs / Use mine" → resolve. The
  // banner shows WHAT the other user wrote before either version is kept.
  const [editingCell, setEditingCell] = useState<EditingCell | null>(null)
  const [savingEntryId, setSavingEntryId] = useState<number | null>(null)
  const [editedEntryIds, setEditedEntryIds] = useState<Set<number>>(new Set())
  // Conflict state is a self-contained snapshot of "what was being edited
  // at the moment the 409 fired" + the server's current view of the row.
  // The banner needs this even after `editingCell` is cleared (commitEdit
  // exits edit mode synchronously, before the mutation resolves) so it
  // can show "my version" (frozen at submit time) vs "their version".
  type TMConflictState = {
    payload: TMConflictPayload
    entryId: number
    field: 'source' | 'target'
    myText: string
    myRichText: string | null
  }
  const [conflictState, setConflictState] = useState<TMConflictState | null>(null)
  // Current-user identity for the banner heading copy. Same shape as the CAT
  // editor — drives "Saved from another tab/session" vs "{Name} saved a
  // different version". Cached for the session.
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe, staleTime: Infinity })
  const currentUserId = me?.id ?? null
  // History viewer — null = closed, set to an entry to open the dialog.
  const [historyEntry, setHistoryEntry] = useState<TMEntry | null>(null)
  // Entry pending delete confirmation. Edits autosave with no undo, so
  // deletion goes through a ConfirmDialog. Only the stable setState function
  // is captured in the columns cell — never this value (columns-deps churn,
  // see the note on the columns useMemo).
  const [deleteTarget, setDeleteTarget] = useState<TMEntry | null>(null)
  // Bulk find & replace modal.
  const [findReplaceOpen, setFindReplaceOpen] = useState(false)
  const editorRef = useRef<RichTextCellEditorHandle | null>(null)
  const baselineUpdatedAtRef = useRef<string | null>(null)
  // Set on Esc so the unmount-induced blur doesn't accidentally save the cancelled edit.
  const cancellingRef = useRef(false)
  // Mirror of editingCell readable inside TipTap keymap callbacks (which run
  // outside React render context and would otherwise see a stale closure).
  const editingCellRef = useRef<EditingCell | null>(null)
  useEffect(() => { editingCellRef.current = editingCell }, [editingCell])
  // Mirror of `data` (the React Query infinite-query result) for use inside
  // `commitEdit`. Without this, putting `data` directly in commitEdit's
  // useCallback deps would change commitEdit's identity on every refetch →
  // ripple through `columns` useMemo → TanStack Table sees new column refs
  // → cells remount → the inline `RichTextCellEditor` re-mounts with
  // `initialText` from the entry (the original chip-bearing string),
  // wiping the user's in-flight typing. Keeping commitEdit identity stable
  // requires reading `data` via a ref instead of via a closure dep.
  const dataRef = useRef<typeof data>(undefined)
  useEffect(() => { dataRef.current = data }, [data])

  // Missing-tags state. `docTick` bumps on every editor
  // transaction (via onTextChange in the cell render below) so atom-only
  // edits (chip insert/delete) trigger missingTags recomputation — atoms
  // don't change `getText()` but DO fire `onUpdate`.
  const [docTick, setDocTick] = useState(0)

  // Plain useMutation: the conflict banner needs the submitted snapshot from
  // the mutation variables, which useMutationWithToast's onError does not get.
  const updateMut = useMutation({
    mutationFn: async (params: {
      entryId: number
      payload: Parameters<typeof updateTMEntry>[2]
      // Snapshot of the cell being edited at submit time, threaded through
      // so onError can rebuild the banner state even after commitEdit
      // synchronously cleared `editingCell`.
      snapshot?: { field: 'source' | 'target'; myText: string; myRichText: string | null }
    }) => {
      setSavingEntryId(params.entryId)
      return updateTMEntry(tid, params.entryId, params.payload)
    },
    onSuccess: (saved) => {
      setSavingEntryId(null)
      // Advance baseline so back-to-back edits by the same user don't 409 against our own write.
      if (saved.updated_at) baselineUpdatedAtRef.current = saved.updated_at
      // ALSO patch the cached TM entries list synchronously so the
      // next `startEdit` re-read of `entry.updated_at` reflects the new value
      // — preventing baseline regression while invalidation refetches. Same
      // pattern as `advanceTranslationInCache` in SegmentRow.
      advanceTMEntryInCache(queryClient, tid, saved)
      setEditedEntryIds(prev => {
        const next = new Set(prev)
        next.add(saved.id)
        return next
      })
      invalidateTMContext(queryClient, tid)
      toast.success('TM entry updated')
    },
    onError: (err: Error, variables) => {
      setSavingEntryId(null)
      // The query client shows the demo organization's one notice.
      if (isDemoReadOnly(err)) return
      if (isSaveConflictError(err)) {
        const payload = getTMConflictPayload(err)
        if (payload && variables.snapshot) {
          // Store the conflict + snapshot together — banner reads from this
          // single source of truth (editingCell is already null at this point).
          setConflictState({
            payload,
            entryId: variables.entryId,
            field: variables.snapshot.field,
            myText: variables.snapshot.myText,
            myRichText: variables.snapshot.myRichText,
          })
          return
        }
        // Legacy backend without conflict body OR no snapshot — fall back
        // to a toast so the user at least knows something happened.
        toast.error(err.message)
        return
      }
      toast.error(`Save failed: ${err.message}`)
    },
  })

  const startEdit = useCallback((entry: TMEntry, field: 'source' | 'target') => {
    cancellingRef.current = false
    baselineUpdatedAtRef.current = entry.updated_at ?? null
    setEditingCell({ entryId: entry.id, field })
  }, [])

  const cancelEdit = useCallback(() => {
    cancellingRef.current = true
    setEditingCell(null)
    baselineUpdatedAtRef.current = null
  }, [])

  // Commit the in-flight edit. Called from onBlur (click outside, Enter→blur).
  // Reads the current entry from the latest cache (via `dataRef.current`, NOT
  // a closure dep on `data`). The dataRef indirection keeps commitEdit's
  // identity stable across refetches — putting `data` directly in deps would
  // ripple through `columns` useMemo → TanStack Table sees new column
  // refs → editor cell remounts mid-typing → user's content gets wiped.
  const commitEdit = useCallback(() => {
    // Esc set the cancel flag; reset and bail.
    if (cancellingRef.current) {
      cancellingRef.current = false
      return
    }
    const cell = editingCellRef.current
    if (!cell) return
    const handle = editorRef.current
    if (!handle) return

    // Read latest data via ref to avoid making this callback depend on `data`.
    const liveData = dataRef.current
    const entry = liveData?.pages.flatMap(p => p.data).find(e => e.id === cell.entryId)
    if (!entry) {
      setEditingCell(null)
      return
    }
    const newText = handle.getText()
    const newRichText = handle.getRichText()

    const original = cell.field === 'source' ? entry.source_text : entry.target_text
    if (newText === original) {
      // No-op edit (user clicked away without changing anything). Just exit edit mode.
      setEditingCell(null)
      return
    }
    if (newText.trim() === '') {
      toast.error(`${cell.field === 'source' ? 'Source' : 'Target'} text cannot be empty`)
      setEditingCell(null)
      return
    }

    const payload = cell.field === 'source'
      ? {
          sourceText: newText,
          sourceRichText: newRichText,
          targetText: entry.target_text,
          targetRichText: entry.target_rich_text ?? null,
          expectedUpdatedAt: baselineUpdatedAtRef.current,
        }
      : {
          sourceText: entry.source_text,
          sourceRichText: entry.source_rich_text ?? null,
          targetText: newText,
          targetRichText: newRichText,
          expectedUpdatedAt: baselineUpdatedAtRef.current,
        }
    updateMut.mutate({
      entryId: cell.entryId,
      payload,
      snapshot: { field: cell.field, myText: newText, myRichText: newRichText },
    })
    setEditingCell(null)
  }, [updateMut])

  // Keep refs to the latest callbacks so the (stable) TipTap keymap extension
  // never holds a stale closure. Same pattern as SegmentEditor's keymap.
  const cancelEditRef = useRef(cancelEdit)
  const commitEditRef = useRef(commitEdit)
  useEffect(() => { cancelEditRef.current = cancelEdit }, [cancelEdit])
  useEffect(() => { commitEditRef.current = commitEdit }, [commitEdit])
  // Forward-declared refs for F8 / Alt-F8 — populated by useEffect below
  // (after `insertNextMissingTag`/`insertAllMissingTags` are defined). Stable
  // object identity means the keymap closure always reads the latest impl.
  const insertNextRef = useRef<() => void>(() => {})
  const insertAllRef = useRef<() => void>(() => {})

  // Stable keymap extension: Esc cancels, Enter commits via blur (which fires
  // onBlur → commitEdit). F8 / Alt-F8 insert the next / all missing source
  // tags at cursor (only meaningful in target cells; no-op when no missing
  // tags exist).
  const tmCellKeymap = useRef(
    Extension.create({
      name: 'tmCellKeymap',
      addKeyboardShortcuts() {
        return {
          'Escape': () => { cancelEditRef.current(); return true },
          'Enter': () => { commitEditRef.current(); return true },
          'F8': () => { insertNextRef.current(); return true },
          'Alt-F8': () => { insertAllRef.current(); return true },
        }
      },
    }),
  ).current

  // Banner action: discard local edits and pull the server's version.
  // Mirrors the CAT editor's "Use theirs" path.
  const handleUseTheirs = useCallback(() => {
    setConflictState(null)
    setEditingCell(null)
    baselineUpdatedAtRef.current = null
    invalidateTMContext(queryClient, tid)
  }, [queryClient, tid])

  // Banner action: re-issue the save with `expected_updated_at = null` so
  // the optimistic-lock check is skipped. The OTHER field stays at its
  // server value (per the conflict payload) — user only intended to
  // overwrite the field they were editing. The other user's version is
  // already preserved in `tm_entry_history`.
  const handleUseMine = useCallback(() => {
    if (!conflictState) return
    const { entryId, field, myText, myRichText, payload } = conflictState
    const sourceText = field === 'source' ? myText : payload.source_text
    const targetText = field === 'target' ? myText : payload.target_text
    const sourceRichText = field === 'source' ? myRichText : payload.source_rich_text
    const targetRichText = field === 'target' ? myRichText : payload.target_rich_text

    setSavingEntryId(entryId)
    updateTMEntry(tid, entryId, {
      sourceText,
      targetText,
      sourceRichText,
      targetRichText,
      // Bypass version check — last-writer-wins on this save.
      expectedUpdatedAt: null,
      changeReason: 'edit',
    })
      .then(saved => {
        setSavingEntryId(null)
        if (saved.updated_at) baselineUpdatedAtRef.current = saved.updated_at
        advanceTMEntryInCache(queryClient, tid, saved)
        setEditedEntryIds(prev => {
          const next = new Set(prev)
          next.add(saved.id)
          return next
        })
        invalidateTMContext(queryClient, tid)
        setConflictState(null)
        setEditingCell(null)
        toast.success('TM entry updated')
      })
      .catch((err: Error) => {
        setSavingEntryId(null)
        toast.error(`Save failed: ${err.message}`)
      })
  }, [conflictState, queryClient, tid])

  // Compute the missing source tags for the *currently
  // edited target cell*. Only meaningful when editing TARGET of a row whose
  // source has XLIFF chips. Reads live editor state via editorRef so the
  // bar tracks user typing in real time (subject to docTick — see above).
  const missingTags = useMemo<MissingTag[]>(() => {
    if (editingCell?.field !== 'target') return []
    const entry = data?.pages.flatMap(p => p.data).find(e => e.id === editingCell.entryId)
    if (!entry?.source_rich_text || !hasXliffTags(entry.source_rich_text)) return []
    const handle = editorRef.current
    const liveRich = handle?.getRichText() ?? null
    const liveText = handle?.getText() ?? null
    const targetRich = liveRich !== null
      ? liveRich
      : liveText !== null
        ? liveText
        : (entry.target_rich_text || entry.target_text || '')
    return getMissingTags(entry.source_rich_text, targetRich)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editingCell, data, docTick])

  // Latest missingTags readable from the stable TipTap keymap closure.
  const missingTagsRef = useRef<MissingTag[]>([])
  useEffect(() => { missingTagsRef.current = missingTags }, [missingTags])

  // Insert a single chip atom at cursor. Used by both bar-chip clicks and F8.
  const insertTagAtCursor = useCallback((tag: MissingTag) => {
    const tipTap = editorRef.current?.getEditor()
    if (!tipTap) return
    tipTap.chain().focus().insertContent({
      type: 'xliffTag',
      attrs: {
        tagType: tag.tagType,
        tagId: tag.tagId,
        ctype: tag.ctype,
        tagNum: tag.tagNum,
        closing: tag.closing,
        rawXml: tag.rawXml,
      },
    }).run()
  }, [])

  const insertNextMissingTag = useCallback(() => {
    const list = missingTagsRef.current
    if (list.length > 0) insertTagAtCursor(list[0])
  }, [insertTagAtCursor])

  const insertAllMissingTags = useCallback(() => {
    // Snapshot the list — each insert mutates the doc → memo recomputes,
    // but we want to insert exactly what was missing when invoked.
    const snapshot = missingTagsRef.current.slice()
    for (const t of snapshot) insertTagAtCursor(t)
  }, [insertTagAtCursor])

  // Sync forward-declared refs (declared above tmCellKeymap so the stable
  // keymap closure can read from them without re-creating the extension).
  useEffect(() => { insertNextRef.current = insertNextMissingTag }, [insertNextMissingTag])
  useEffect(() => { insertAllRef.current = insertAllMissingTags }, [insertAllMissingTags])

  // Column definitions for TM entries table.
  // Editable cells (Source/Target) swap a read-only <SegmentText> for a
  // <RichTextCellEditor> when the user clicks. Stateful deps are intentional:
  // the cell render function captures the current editing state via closure,
  // so the columns array must rebuild when state changes. Cost is bounded by
  // virtualization (~30 visible rows).
  const columns = useMemo<ColumnDef<TMEntry>[]>(() => {
    const renderEditableCell = (entry: TMEntry, field: 'source' | 'target') => {
      const isEditing = editingCell?.entryId === entry.id && editingCell.field === field
      // Cells wrap up to the CAT preview cap; the row being edited is uncapped.
      const isRowActive = editingCell?.entryId === entry.id
      const isSaving = savingEntryId === entry.id
      const isEdited = editedEntryIds.has(entry.id)
      const text = field === 'source' ? entry.source_text : entry.target_text
      const richText = field === 'source' ? entry.source_rich_text : entry.target_rich_text
      const lang = field === 'source' ? entry.source_language : entry.target_language
      const initialText = (richText && richText !== '') ? richText : text
      const baseClass = field === 'source'
        ? 'text-sm text-gray-900 dark:text-gray-100'
        : 'text-sm text-gray-700 dark:text-gray-300'

      if (isEditing) {
        return (
          // Stable React `key` keeps the editor mounted across re-renders of
          // this cell. Without a key, when the columns array's identity
          // changes (e.g. some other state nearby triggered a re-render),
          // React's reconciliation may elect to swap the editor — which
          // re-runs `useEditor` with `content: textToContent(initialText)`,
          // recreating the entry's ORIGINAL chip-bearing content and wiping
          // the user's in-flight typing. Same `id-field` pair → same key →
          // same component instance.
          <RichTextCellEditor
            key={`${entry.id}-${field}`}
            ref={editorRef}
            initialText={initialText}
            readOnly={false}
            language={lang}
            dataTestId={`tm-cell-edit-${entry.id}-${field}`}
            className={`${baseClass} px-1 -mx-1 bg-primary/10 rounded`}
            extraExtensions={[tmCellKeymap]}
            onTextChange={() => {
              // Bump on every editor transaction so missingTags recomputes
              // on atom-only changes (chip insert via F8 / chip-click,
              // chip delete via Backspace) — atoms don't change `getText()`
              // but they DO fire onUpdate.
              setDocTick(t => t + 1)
            }}
            // Read commitEdit through a ref so this cell function (captured
            // in `columns` useMemo) doesn't capture commitEdit's identity,
            // so a change to commitEdit's deps can't remount the cells.
            onBlur={() => commitEditRef.current()}
          />
        )
      }
      return (
        <span
          className={`${baseClass} break-words ${isRowActive ? 'block' : SEGMENT_PREVIEW_CLAMP} cursor-text hover:bg-gray-50 dark:hover:bg-gray-800 px-1 -mx-1 rounded`}
          onClick={() => startEdit(entry, field)}
          data-testid={`tm-cell-${entry.id}-${field}`}
        >
          <SegmentText text={text} richText={richText} search={field === 'source' ? sourceHighlight : targetHighlight} />
          {isSaving && <span className="ml-2 text-xs text-gray-400 italic">Saving…</span>}
          {!isSaving && isEdited && (
            <Pencil className="inline w-3 h-3 ml-1 text-emerald-500 align-baseline" aria-label="Edited this session" />
          )}
        </span>
      )
    }

    return [
      // Entries can carry a different source locale than the TM's declared one
      // (imports keep the tuv's locale, write-back keeps the
      // project's), so SL is shown per row alongside the TM-wide label above.
      { accessorKey: 'source_language', header: 'SL', size: 70, cell: ({ getValue }) => <LanguageCode code={getValue() as string} className="text-xs text-gray-500" /> },
      { accessorKey: 'target_language', header: 'TL', size: 70, cell: ({ getValue }) => <LanguageCode code={getValue() as string} className="text-xs text-gray-500" /> },
      { accessorKey: 'source_text', header: 'Source', cell: ({ row }) => renderEditableCell(row.original, 'source') },
      { accessorKey: 'target_text', header: 'Target', cell: ({ row }) => renderEditableCell(row.original, 'target') },
      {
        accessorKey: 'updated_by', header: 'Last edited by', size: 160,
        cell: ({ getValue }) => {
          const v = getValue() as string | undefined
          if (!v) return <span className="text-xs text-gray-500">—</span>
          // Truncate to the fixed column width; full "Name <email>" on hover.
          return (
            <Tooltip>
              <TooltipTrigger render={<span className="text-xs text-gray-500 truncate block">{v}</span>} />
              <TooltipContent>{v}</TooltipContent>
            </Tooltip>
          )
        },
      },
      { accessorKey: 'created_at', header: 'Added', size: 110, cell: ({ row }) => <span className="text-xs text-gray-400">{new Date(row.original.created_at).toLocaleDateString()}</span> },
      {
        id: 'actions', header: '', size: 70,
        cell: ({ row }) => (
          <div className="flex items-center gap-0.5">
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button variant="ghost" size="icon-xs"
                    onClick={() => setHistoryEntry(row.original)}
                    className={hoverActionClass()}
                    data-testid={`tm-history-btn-${row.original.id}`}
                  />
                }
              ><History className="w-3 h-3" /></TooltipTrigger>
              <TooltipContent>View edit history</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                render={
                  <DeleteButton size="icon-xs"
                    onClick={() => setDeleteTarget(row.original)}
                    data-testid={`tm-delete-btn-${row.original.id}`}
                  />
                }
              />
              <TooltipContent>Delete entry</TooltipContent>
            </Tooltip>
          </div>
        ),
      },
    ]
    // Deps must be referentially stable, or the cells remount
    // mid-typing. Use `deleteMutate` (the stable `.mutate`), never the
    // mutation object; `commitEdit` is absent because the cell reads it via
    // `commitEditRef.current()`. Any dep that itself depends on
    // `data`/refetch values must follow the same ref pattern.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deleteMutate, editingCell, savingEntryId, editedEntryIds, startEdit, tmCellKeymap, sourceHighlight, targetHighlight])

  // Load more callback for VirtualDataTable infinite scroll
  const handleLoadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) fetchNextPage()
  }, [hasNextPage, isFetchingNextPage, fetchNextPage])

  // Poll for import progress
  const pollImport = useCallback(async (importId: number) => {
    const poll = async () => {
      const imports = await listTMImports(tid)
      const imp = imports.find((i: TMImport) => i.id === importId)
      if (!imp) return

      setActiveImport(imp)

      if (imp.status === 'completed') {
        toast.success(`Imported ${imp.imported.toLocaleString()} entries${imp.skipped > 0 ? ` (${imp.skipped} skipped)` : ''}`)
        setActiveImport(null)
        queryClient.invalidateQueries({ queryKey: queryKeys.tm.allEntries(tid) })
        queryClient.invalidateQueries({ queryKey: queryKeys.tm.langPairs(tid) })
        return
      }

      if (imp.status === 'failed') {
        toast.error(`Import failed: ${imp.error_message || 'Unknown error'}`)
        setActiveImport(null)
        return
      }

      // Still processing — poll again
      setTimeout(poll, 1000)
    }
    poll()
  }, [tid, queryClient])

  const handleImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    // Reset input so same file can be re-selected
    e.target.value = ''

    try {
      setUploadProgress(0)
      const { promise, abort } = importTMX(tid, file, (pct) => setUploadProgress(pct))
      uploadAbortRef.current = abort
      const imp = await promise
      uploadAbortRef.current = null
      setUploadProgress(null)
      setActiveImport(imp)
      toast.info('Import started...')
      pollImport(imp.id)
    } catch (err) {
      uploadAbortRef.current = null
      setUploadProgress(null)
      const msg = errorText(err, 'Unknown error')
      if (msg !== 'Upload cancelled') toast.error(`Import failed: ${msg}`)
    }
  }

  const handleExport = async () => {
    try {
      await exportTMX(tid, tm?.name ?? `tm-${tid}`)
      toast.success('TMX file downloaded')
    } catch (err) {
      toast.error(`Export failed: ${errorText(err, 'Unknown error')}`)
    }
  }

  const entryCountLabel = tmEntryCountLabel(allEntries.length, totalCount, !!debouncedSearch, !!hasNextPage)

  return (
    <TooltipProvider delay={200}>
      <PageContainer width="full">
        <Breadcrumbs items={[
          { label: 'Translation memories', to: '/memories' },
          {
            label: (
              <EditableTitle
                name={tm?.name ?? `TM ${tid}`}
                onRename={(name) => renameMut.mutate(name)}
                data-testid="rename-tm"
              />
            ),
          },
        ]} />

        <div className="flex max-md:flex-wrap items-center justify-between gap-2 mb-4">
          {sourceLabel ? (
            <div className="text-xs text-gray-500 dark:text-gray-400" data-testid="tm-source-lang">
              {sourceCount > 1 ? 'Sources' : 'Source'}: <LanguageCode code={sourceLabel} className="text-gray-600 dark:text-gray-300" />
            </div>
          ) : <span />}
          <div className="flex max-md:flex-wrap items-center gap-2">
            {!loadFailed && <span className="text-sm text-gray-500 dark:text-gray-400">{entryCountLabel}</span>}
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setFindReplaceOpen(true)}
                    disabled={totalCount === 0}
                    data-testid="tm-find-replace-btn"
                  />
                }
              >
                <Replace className="w-4 h-4 mr-1" />
                Find &amp; replace
              </TooltipTrigger>
              <TooltipContent>Bulk find &amp; replace across this TM</TooltipContent>
            </Tooltip>
            <input
              ref={fileInputRef}
              type="file"
              accept=".tmx"
              onChange={handleImport}
              className="hidden"
            />
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => fileInputRef.current?.click()}
                    disabled={!!activeImport}
                    data-guide="tm.import"
                  />
                }
              >
                <Upload className="w-4 h-4 mr-1" />
                Import TMX
              </TooltipTrigger>
              <TooltipContent>Import entries from a TMX file</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleExport}
                    disabled={totalCount === 0}
                  />
                }
              >
                <Download className="w-4 h-4 mr-1" />
                Export TMX
              </TooltipTrigger>
              <TooltipContent>Export all entries as TMX file</TooltipContent>
            </Tooltip>
          </div>
        </div>

        {/* Upload progress banner */}
        {uploadProgress !== null && (
          <div className="mb-4 px-4 py-2 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg text-sm text-blue-700 dark:text-blue-300">
            <div className="flex items-center justify-between">
              <span>Uploading file... {uploadProgress}%</span>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => { uploadAbortRef.current?.(); toast.info('Upload cancelled') }}
                className="text-blue-600 hover:text-red-600 dark:text-blue-400 dark:hover:text-red-400"
              >
                Cancel
              </Button>
            </div>
            <Progress value={uploadProgress} size="sm" tone="blue" aria-label="Upload progress" className="mt-1" />
          </div>
        )}

        {/* Import progress banner */}
        {activeImport && activeImport.status === 'processing' && (
          <div className="mb-4 px-4 py-2 bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800 rounded-lg text-sm text-emerald-700 dark:text-emerald-300">
            Importing... {activeImport.imported.toLocaleString()} / {activeImport.total_units.toLocaleString()} entries
            {activeImport.total_units > 0 && (
              <Progress
                value={(activeImport.imported / activeImport.total_units) * 100}
                size="sm"
                tone="green"
                aria-label="Import progress"
                className="mt-1"
              />
            )}
          </div>
        )}

        <TMEntryFilters
          langPairs={langPairs}
          selectedPair={selectedPair}
          onSelectPair={setSelectedPair}
          search={search}
          serverSearchError={serverSearchError}
        />

        {/* Missing-tags bar for inline target editing. Rendered
            at page level, not inside the cell: inside, `missingTags` and the
            insert handlers would join the `columns` deps and remount the
            editor mid-typing. F8/Alt-F8 on the editor work regardless. */}
        {editingCell?.field === 'target' && missingTags.length > 0 && (
          <div className="mb-2">
            <MissingTagsBar
              missing={missingTags}
              onInsert={insertTagAtCursor}
              onInsertAll={insertAllMissingTags}
            />
          </div>
        )}

        {/* Side-by-side conflict banner. Sits above the data
            table so the row being edited
            stays visible directly below the banner. The cell editor exits
            edit mode synchronously when the user blurs out, but the banner
            shows their FROZEN edit (captured at submit time in
            `conflictState`) — the user has already moved focus elsewhere
            so there's no live editing to preserve, unlike the CAT case. */}
        {conflictState && (() => {
          const { payload, field, myText, myRichText } = conflictState
          const isSource = field === 'source'
          return (
            <div className="mb-2">
              <ConflictBanner
                fieldLabel={isSource ? 'SOURCE' : 'TARGET'}
                theirText={isSource ? payload.source_text : payload.target_text}
                theirRichText={isSource ? payload.source_rich_text : payload.target_rich_text}
                myEditText={myText}
                myEditRichText={myRichText}
                editedByUserId={payload.updated_by_user_id}
                editedByName={payload.updated_by_name}
                currentUserId={currentUserId}
                onUseTheirs={handleUseTheirs}
                onUseMine={handleUseMine}
              />
            </div>
          )
        })()}

        {isLoading ? (
          <LoadingPlaceholder />
        ) : loadFailed ? (
          <LoadFailed what="entries" error={error} onRetry={() => refetch()} />
        ) : (
          <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 overflow-hidden flex-1 min-h-0" data-guide="tm.entries">
            <VirtualDataTable
              columns={columns}
              data={allEntries}
              estimateSize={40}
              // Measured (variable-height) rows: the inline editor wraps
              // multi-line entries past the 40px estimate; without
              // measurement the virtualizer kept laying out subsequent rows
              // at 40px offsets and the editing row painted under them.
              measureRows
              onLoadMore={handleLoadMore}
              isLoadingMore={isFetchingNextPage}
              emptyMessage={search.invalid
                ? 'Invalid regular expression.'
                : serverSearchError
                ? serverSearchError
                : debouncedSearch
                ? 'No entries match your search.'
                : 'No TM entries yet. Import a TMX file or confirm translations in the editor to populate this TM.'}
              rowClassName={(row) => {
                const isEditing = editingCell?.entryId === row.id
                return `group ${isEditing ? 'bg-primary/10' : ''}`
              }}
            />
          </div>
        )}

        <TMEntryHistoryDialog
          tmId={tid}
          entry={historyEntry}
          onClose={() => setHistoryEntry(null)}
        />

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title="Delete TM entry?"
          description={deleteTarget
            ? `"${deleteTarget.source_text.length > 80 ? `${deleteTarget.source_text.slice(0, 80)}…` : deleteTarget.source_text}" and its translation will be permanently removed.`
            : ''}
          onConfirm={() => deleteTarget && deleteMutate(deleteTarget.id)}
        />

        <TMBulkFindReplaceModal
          open={findReplaceOpen}
          onClose={() => setFindReplaceOpen(false)}
          tmId={tid}
          sourceLanguage={sourceLang}
          targetLanguage={targetLang}
        />
      </PageContainer>
    </TooltipProvider>
  )
}
