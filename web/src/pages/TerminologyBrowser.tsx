import { useState, useCallback, useMemo, useEffect } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { useQuery, useInfiniteQuery, keepPreviousData } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import { listConcepts, listTermBases, listTermBaseLanguages, listLanguages, type Concept } from '../api'
import { queryKeys } from '../lib/query-keys'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { InlineRenameField } from '@/components/list/InlineRenameField'
import { EditableTitle } from '@/components/list/EditableTitle'
import { hoverActionClass } from '@/components/list/hover-action'
import { TooltipProvider, Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { VirtualDataTable } from '@/components/ui/virtual-data-table'
import { Upload, Download, Plus, Pencil, ChevronDown, Loader2 } from 'lucide-react'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { useTerminologyMutations } from './terminology-browser/use-terminology-mutations'
import { useTerminologyImportExport } from './terminology-browser/use-terminology-import-export'
import { ConceptTermsGrid, TERMS_GRID, TermLanguagesContext, focusOnMount } from './terminology-browser/ConceptTermsGrid'
import { useConceptInlineEdit, type EditApi } from './terminology-browser/use-inline-edit'
import { LanguageFilter } from './terminology-browser/LanguageFilter'
import ConceptEditorDialog from './terminology-browser/ConceptEditorDialog'
import { useDebounce } from '../hooks/useDebounce'
import { formatDate } from '../utils'
import { ChipButton } from '@/components/ui/chip'
import { usePageTitle } from '../hooks/usePageTitle'
import { LanguagePair } from '@/components/ui/language-code'
import { TM_LANGS_SHOWN } from '@/lib/tm-display'

const PAGE_SIZE = 100

// UpdatesCell shows a concept's creation and last-update stamps (timestamp + the
// acting user) stacked in one column.
function UpdatesCell({ c }: { c: Concept }) {
  // Timestamp styling mirrors the rest of the repo (list pages): non-italic,
  // "Created {date}" (no colon), text-xs text-gray-400 dark:text-gray-500. The
  // acting user, when known, sits on its own muted line.
  const row = (label: string, at: string, by: string) => (
    <div>
      <div className="text-xs text-gray-400 dark:text-gray-500">{label} {at ? formatDate(at) : '—'}</div>
      {by && <div className="text-xs text-gray-400 dark:text-gray-600 truncate" title={by}>{by}</div>}
    </div>
  )
  return (
    <div className="space-y-1.5">
      {row('Created', c.created_at, c.created_by)}
      {row('Updated', c.updated_at, c.updated_by)}
    </div>
  )
}

// DefinitionCell — clamped, click-to-edit (multi-line). Saves on blur, cancels on
// Escape (Enter inserts a newline, so it's not a commit key here).
function DefinitionCell({ concept, editApi }: { concept: Concept; editApi: EditApi }) {
  const active = editApi.get()
  if (active?.kind === 'definition' && active.conceptId === concept.id) {
    return (
      <Textarea
        ref={focusOnMount}
        aria-label="Edit definition"
        className="text-sm"
        rows={Math.min(6, Math.max(2, concept.terms.length))}
        value={active.draft}
        onChange={e => editApi.setDraft(e.target.value)}
        onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); editApi.cancel() } }}
        onBlur={() => editApi.commit()}
        onClick={e => e.stopPropagation()}
      />
    )
  }
  // Clamp to the terms' height (~36px/term row over 20px text lines).
  const lines = Math.max(1, Math.floor(((concept.terms.length || 1) * 36) / 20))
  return (
    <span
      className="text-sm text-gray-500 dark:text-gray-400 cursor-text rounded hover:bg-gray-100 dark:hover:bg-gray-800"
      style={{ display: '-webkit-box', WebkitBoxOrient: 'vertical', WebkitLineClamp: lines, overflow: 'hidden' }}
      onClick={e => { e.stopPropagation(); editApi.startDefinition(concept.id, concept.definition, concept.domain) }}
    >
      {concept.definition || <span className="italic text-gray-300 dark:text-gray-600">Add definition…</span>}
    </span>
  )
}

// DomainCell — click-to-edit pill; empty shows a hover "+ domain" affordance.
function DomainCell({ concept, editApi }: { concept: Concept; editApi: EditApi }) {
  const active = editApi.get()
  if (active?.kind === 'domain' && active.conceptId === concept.id) {
    return (
      <Input
        ref={focusOnMount}
        aria-label="Edit domain"
        className="h-7 text-sm"
        value={active.draft}
        onChange={e => editApi.setDraft(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); editApi.commit() } else if (e.key === 'Escape') { e.preventDefault(); editApi.cancel() } }}
        onBlur={() => editApi.commit()}
        onClick={e => e.stopPropagation()}
      />
    )
  }
  const start = (e: React.MouseEvent) => { e.stopPropagation(); editApi.startDomain(concept.id, concept.definition, concept.domain) }
  return concept.domain
    ? <ChipButton color="grey" onClick={start}>{concept.domain}</ChipButton>
    : <button type="button" onClick={start} className="opacity-0 group-hover:opacity-100 text-xs italic text-gray-400 dark:text-gray-500">+ domain</button> // eslint-disable-line no-restricted-syntax -- inline click-to-edit trigger (domain), not an action button
}

export default function TerminologyBrowser() {
  const { terminologyId } = useParams<{ terminologyId: string }>()
  const gid = Number(terminologyId)

  // Search + language filter live in the URL so the view is deep-linkable (e.g. a
  // "view in term base" link from the CAT editor) and survives a refresh. `search`
  // is seeded from ?q and mirrored back (debounced); the language filter is derived
  // from ?lang below.
  const [searchParams, setSearchParams] = useSearchParams()
  const [search, setSearch] = useState(() => searchParams.get('q') ?? '')
  const debouncedSearch = useDebounce(search, 300)

  // ?lang contract: absent = default to the base's source language (source-first
  // view); `all` = every language (no filter); `de,fr` = just those. Writing the
  // filter always sets an explicit value ('all' or a code list), so a chosen state
  // — including "All languages" — persists and is linkable.
  const setLangFilter = useCallback((next: string[]) => {
    setSearchParams(prev => {
      const p = new URLSearchParams(prev)
      p.set('lang', next.length ? next.join(',') : 'all')
      return p
    }, { replace: true })
  }, [setSearchParams])
  // conceptId: number = edit, null = new concept, undefined = closed.
  const [editorConceptId, setEditorConceptId] = useState<number | null | undefined>(undefined)
  // When opening the dialog from a specific term row, scroll that term into view.
  const [focusTermId, setFocusTermId] = useState<number | undefined>(undefined)
  // Stable (setters are stable) so the []-style columns memo doesn't churn.
  const openConcept = useCallback((conceptId: number | null, termId?: number) => {
    setFocusTermId(termId)
    setEditorConceptId(conceptId)
  }, [])

  // Editable header state
  const [editingDesc, setEditingDesc] = useState(false)
  const [descValue, setDescValue] = useState('')

  const { data: bases, error: basesError, refetch: refetchBases } = useQuery({ queryKey: queryKeys.terminology.all, queryFn: listTermBases })
  const base = bases?.find(b => b.id === gid)
  usePageTitle(base?.name)

  // Distinct languages present — drives the CSV export picker + the header hint.
  const { data: baseLanguages = [], isSuccess: langsLoaded, error: langsError, refetch: refetchLangs } = useQuery({
    queryKey: queryKeys.termBaseLanguages(gid),
    queryFn: () => listTermBaseLanguages(gid),
  })

  // Derive the active language filter from ?lang. `filterReady` gates the concepts
  // query so the FIRST fetch is already scoped (no wasted all-languages load then
  // refetch). The source-default and explicit-code cases need the base's live
  // languages loaded; `all` does not.
  const langParam = searchParams.get('lang')
  const { langFilter, filterReady } = useMemo(() => {
    if (langParam === 'all') return { langFilter: [] as string[], filterReady: true }
    if (!base || !langsLoaded) return { langFilter: [] as string[], filterReady: false }
    if (langParam) {
      // Match URL codes against the base's actual languages, case-insensitively, so
      // a hand-typed or editor-built link round-trips regardless of casing.
      const wanted = langParam.split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
      return { langFilter: baseLanguages.filter(l => wanted.includes(l.toLowerCase())), filterReady: true }
    }
    // Absent: default to the source language, but only if it actually has terms —
    // otherwise the view would start empty. The user can switch to All from there.
    const src = base.source_language
    return { langFilter: src && baseLanguages.includes(src) ? [src] : [], filterReady: true }
  }, [langParam, base, langsLoaded, baseLanguages])

  // Mirror the debounced search into ?q so it's linkable + survives refresh.
  // replace: true keeps typing from spamming the history stack.
  useEffect(() => {
    setSearchParams(prev => {
      const p = new URLSearchParams(prev)
      if (debouncedSearch) p.set('q', debouncedSearch)
      else p.delete('q')
      return p
    }, { replace: true })
  }, [debouncedSearch, setSearchParams])

  const {
    data,
    error: conceptsError,
    isError: conceptsFailed,
    refetch: refetchConcepts,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isFetching,
    isLoading,
  } = useInfiniteQuery({
    queryKey: queryKeys.concepts.byBase(gid, debouncedSearch, langFilter),
    queryFn: ({ pageParam = 0 }) => listConcepts(gid, {
      limit: PAGE_SIZE,
      cursor: pageParam,
      q: debouncedSearch || undefined,
      lang: langFilter.length ? langFilter : undefined,
    }),
    getNextPageParam: (lastPage) => lastPage.has_more ? lastPage.cursor : undefined,
    initialPageParam: 0,
    placeholderData: keepPreviousData,
    // Wait until the default source-language filter is resolved, so the first
    // fetch is already scoped (no wasted all-languages load + refetch).
    enabled: filterReady,
  })

  // Memoize the flattened list: a fresh array each render would give react-table
  // a new `data` reference on every keystroke of an inline edit, forcing it to
  // rebuild the ENTIRE row model (O(all loaded concepts)) per keypress — which
  // makes typing lag badly once pagination has loaded many concepts. Stable
  // identity keeps the row model cached; typing then costs only the visible rows.
  const allConcepts = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])
  const totalCount = data?.pages[0]?.total ?? 0

  const handleLoadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) fetchNextPage()
  }, [hasNextPage, isFetchingNextPage, fetchNextPage])

  const { renameMut } = useTerminologyMutations(gid)
  const { fileInputRef, uploadProgress, importing, handleImport, handleExport, cancelUpload } = useTerminologyImportExport(gid, base?.name ?? `term-base-${gid}`)

  // Inline editing controller (stable identity); the add-term picker's languages
  // come through TermLanguagesContext. Keeping the columns' inputs stable keeps
  // the columns stable — the requirement that prevents the cell-input
  // remount/focus bug.
  const editApi = useConceptInlineEdit(gid)
  const { data: allLanguages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const langNameOf = useMemo(() => {
    const m = new Map((allLanguages ?? []).map(l => [l.code, l.name]))
    return (code: string) => m.get(code) ?? code
  }, [allLanguages])
  const termLanguages = useMemo(() => allLanguages ?? [], [allLanguages])

  const columns = useMemo<ColumnDef<Concept>[]>(() => [
    {
      // Language + Terms + Status as one grid column (see ConceptTermsGrid); the
      // header mirrors the same grid so the labels sit over their sub-columns.
      // This is the fill column so Terms gets as much width as possible.
      id: 'terms',
      header: () => (
        <div className={TERMS_GRID}>
          <span>Language</span>
          <span>Terms</span>
          <span className="text-center">Status</span>
        </div>
      ),
      cell: ({ row }) => <ConceptTermsGrid concept={row.original} editApi={editApi} onOpenConcept={openConcept} />,
    },
    {
      header: 'Definition', accessorKey: 'definition', size: 280,
      cell: ({ row }) => <DefinitionCell concept={row.original} editApi={editApi} />,
    },
    {
      header: 'Domain', accessorKey: 'domain', size: 200,
      cell: ({ row }) => <DomainCell concept={row.original} editApi={editApi} />,
    },
    {
      header: 'Info', id: 'info', size: 190,
      cell: ({ row }) => (
        <div className="flex items-start justify-between gap-2">
          <UpdatesCell c={row.original} />
          <Button
            variant="ghost"
            size="icon-xs"
            type="button"
            aria-label="Edit concept"
            className={hoverActionClass('accent', 'shrink-0')}
            onClick={e => { e.stopPropagation(); openConcept(row.original.id) }}
          >
            <Pencil className="w-4 h-4" />
          </Button>
        </div>
      ),
    },
    // editApi and openConcept are both stable — so the columns never
    // rebuild (cell-fn identities stay put).
  ], [editApi, openConcept])

  // Header edit helpers
  const startEditDesc = () => { setDescValue(base?.description ?? ''); setEditingDesc(true) }
  const submitDesc = () => {
    renameMut.mutate({ name: base?.name ?? '', description: descValue.trim() })
    setEditingDesc(false)
  }

  const filtered = Boolean(debouncedSearch || langFilter.length)
  // The concepts query waits on the base and its languages, so their failures
  // block the list too. No concepts data at all means the first page failed.
  const loadError = !filterReady
    ? (basesError ?? langsError)
    : conceptsFailed && !data ? conceptsError : null
  const retryLoad = () => {
    if (basesError) refetchBases()
    if (langsError) refetchLangs()
    if (conceptsFailed) refetchConcepts()
  }
  // "Loading" spans both the pre-filter-ready gate AND the first page fetch.
  const loading = !loadError && (!filterReady || isLoading)
  // A search is "in progress" while the debounce is still pending (typed text not
  // yet applied) or the (non-pagination) query is refetching for the new query.
  const searching = !loading && (search !== debouncedSearch || (isFetching && !isFetchingNextPage))
  // Never claim "0 concepts" before the first page has resolved — that reads as an
  // empty term base when it's really still loading.
  const countLabel = loadError
    ? ''
    : loading
    ? 'Loading…'
    : totalCount > 0
      ? `${allConcepts.length.toLocaleString()} of ${totalCount.toLocaleString()} ${filtered ? 'matching concepts' : 'concepts'}`
      : filtered ? 'No matching concepts' : 'No concepts yet'

  const langSource = base?.source_language ?? ''
  const langPresent = baseLanguages.length > 0 ? baseLanguages : [langSource, ...(base?.target_languages ?? [])]
  const langTargets = langPresent.filter(l => l && l !== langSource)

  return (
    <TooltipProvider delay={200}>
      <PageContainer width="full">
        <Breadcrumbs items={[
          { label: 'Terminology', to: '/terminology' },
          {
            label: (
              <EditableTitle
                name={base?.name ?? `Term base ${gid}`}
                onRename={(name) => renameMut.mutate({ name, description: base?.description ?? '' })}
                data-testid="rename-term-base"
              />
            ),
          },
        ]} />

        <div className="flex items-center justify-between mb-1">
          <LanguagePair source={langSource} targets={langTargets} separator="|" max={TM_LANGS_SHOWN} className="text-gray-500 dark:text-gray-400" />
          <div className="flex items-center gap-2">
            <input ref={fileInputRef} type="file" accept=".csv,.tbx,.xml" onChange={handleImport} className="hidden" />
            <Tooltip>
              <TooltipTrigger render={<Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()} data-guide="term_base.import" />}>
                <Upload className="w-4 h-4 mr-1" />
                Import
              </TooltipTrigger>
              <TooltipContent>Import a CSV (bilingual rows) or TBX (multilingual concepts) file</TooltipContent>
            </Tooltip>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={<Button variant="outline" size="sm" disabled={totalCount === 0} />}
              >
                <Download className="w-4 h-4 mr-1" />
                Export
                <ChevronDown className="w-3.5 h-3.5 ml-1 opacity-60" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => handleExport('csv')}>CSV</DropdownMenuItem>
                <DropdownMenuItem onClick={() => handleExport('tbx')}>TBX</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        {/* Upload / import progress banner. Once the bytes are sent (importing),
            the request is still open while the server parses + inserts — which can
            take minutes for a large TBX — so we show an indeterminate "Importing"
            state instead of a stuck "Uploading 100%". */}
        {uploadProgress !== null && (
          <div className="mb-4 px-4 py-2 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg text-sm text-blue-700 dark:text-blue-300">
            <div className="flex items-center justify-between">
              <span>
                {importing
                  ? 'Importing… Large files can take a minute or two'
                  : `Uploading... ${uploadProgress}%`}
              </span>
              {/* No Cancel once importing: the bytes are already on the server and
                  aborting the request won't roll back the in-progress insert. */}
              {!importing && (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={cancelUpload}
                  className="text-blue-600 hover:text-red-600 dark:text-blue-400 dark:hover:text-red-400"
                >
                  Cancel
                </Button>
              )}
            </div>
            <Progress
              value={importing ? null : uploadProgress}
              size="sm"
              tone="blue"
              aria-label={importing ? 'Import progress' : 'Upload progress'}
              className="mt-1"
            />
          </div>
        )}

        {/* Editable description */}
        <div className="mb-4 group/desc">
          {editingDesc ? (
            <InlineRenameField
              value={descValue}
              onChange={setDescValue}
              onSubmit={submitDesc}
              onCancel={() => setEditingDesc(false)}
              className="text-sm text-gray-500 dark:text-gray-400 max-w-md font-normal"
              aria-label="Edit term base description"
              placeholder="Add a description..."
              maxLength={500}
            />
          ) : (
            <p
              className="text-sm text-gray-500 dark:text-gray-400 cursor-pointer hover:text-primary inline-flex items-center gap-1"
              onClick={startEditDesc}
              title="Click to edit description"
            >
              {base?.description || <span className="italic text-gray-300 dark:text-gray-600">Add a description...</span>}
              <Pencil className="w-3 h-3 text-gray-300 dark:text-gray-600 opacity-0 group-hover/desc:opacity-100 transition-opacity" />
            </p>
          )}
        </div>

        {/* Toolbar: search + language filter on the left, concept count + "New
            concept" on the right; wraps on a narrow screen. */}
        <div className="flex max-md:flex-wrap items-center justify-between gap-3 mb-4">
          <div className="flex max-md:flex-wrap items-center gap-3 min-w-0">
            <Input
              placeholder="Search concepts..."
              value={search}
              onChange={e => setSearch(e.target.value)}
              className="w-64 h-8 text-sm"
              action={searching && (
                <Loader2
                  className="w-4 h-4 text-gray-400 animate-spin"
                  aria-label="Searching"
                  data-testid="concept-search-spinner"
                />
              )}
            />
            <LanguageFilter
              languages={baseLanguages}
              selected={langFilter}
              onChange={setLangFilter}
              nameOf={langNameOf}
            />
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <span className="text-sm text-gray-500 dark:text-gray-400">{countLabel}</span>
            <Tooltip>
              <TooltipTrigger render={<Button variant="outline" size="sm" onClick={() => openConcept(null)} data-testid="new-concept" />}>
                <Plus className="w-4 h-4 mr-1" />
                New concept
              </TooltipTrigger>
              <TooltipContent>Create a new multilingual concept</TooltipContent>
            </Tooltip>
          </div>
        </div>

        {loadError ? (
          <LoadFailed what="concepts" error={loadError} onRetry={retryLoad} />
        ) : loading ? (
          <LoadingPlaceholder />
        ) : (
          <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 overflow-hidden flex-1 min-h-0" data-guide="term_base.concepts">
            <TermLanguagesContext.Provider value={termLanguages}>
              <VirtualDataTable
                columns={columns}
                data={allConcepts}
                estimateSize={72}
                measureRows
                // Its width in a 1280px window; on a phone the table scrolls sideways instead.
                minFillColumnWidth={320}
                onLoadMore={handleLoadMore}
                isLoadingMore={isFetchingNextPage}
                emptyMessage={filtered
                  ? 'No concepts match your filters.'
                  : 'No concepts yet. Create one or import a CSV/TBX file.'}
                // Concepts get a thicker (4px) divider than the 1px term dividers
                // inside a concept — same color, so thickness alone signals the
                // concept boundary. Scoped here so other tables keep their default.
                // No onRowClick: fields are edited inline; the Info pencil opens the
                // full concept dialog.
                rowClassName={() => 'group !border-b-4 !border-gray-200 dark:!border-gray-700'}
              />
            </TermLanguagesContext.Provider>
          </div>
        )}

        {editorConceptId !== undefined && (
          <ConceptEditorDialog
            open
            onOpenChange={(o) => { if (!o) setEditorConceptId(undefined) }}
            baseId={gid}
            conceptId={editorConceptId}
            focusTermId={focusTermId}
            defaultLanguage={base?.source_language}
          />
        )}
      </PageContainer>
    </TooltipProvider>
  )
}
