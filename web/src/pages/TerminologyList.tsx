import { useMemo } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTermBases, updateTermBase, deleteTermBase, getMe } from '../api'
import { queryKeys } from '../lib/query-keys'
import { formatDate } from '../utils'
import { useCRUDList } from '../hooks/use-crud-list'
import { useCreatedByParam } from '../hooks/use-created-by-param'
import { distinctCreators } from '../lib/creators'
import { CreateTermBaseDialog } from '../components/CreateTermBaseDialog'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { CreatedByFilterChip } from '@/components/list'
import { CreatorLabel } from '@/components/user/CreatorLabel'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { listCardClass, listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'
import { LanguagePair } from '@/components/ui/language-code'
import { TM_LANGS_SHOWN } from '@/lib/tm-display'

export default function TerminologyList() {
  const navigate = useNavigate()
  const [createdBy, setCreatedBy] = useCreatedByParam()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })

  const {
    items: allBases, isLoading, loadFailed, error, refetch,
    showForm, setShowForm,
    // Rename now lives on the term-base detail page (pencil next to the title).
    deleteTarget, setDeleteTarget, handleDelete, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.terminology.all,
    queryFn: listTermBases,
    // Create is owned by CreateTermBaseDialog (it captures source/target and does
    // its own cache update + toast), so this createFn is never invoked.
    createFn: () => Promise.resolve(),
    renameFn: ({ id, name }) => updateTermBase(id, name, ''),
    deleteFn: deleteTermBase,
    entityName: 'Term base',
  })

  // Client-side creator filter — the list is fetched whole (a handful of rows
  // per tenant), so there's no server-side filter to add.
  const bases = useMemo(
    () => createdBy == null ? allBases : allBases?.filter(b => b.created_by === createdBy),
    [allBases, createdBy],
  )

  // Creator options come from the rows themselves, not the org member list —
  // see lib/creators.ts for why.
  const creators = useMemo(() => distinctCreators(allBases), [allBases])

  if (isLoading) return <LoadingPlaceholder />

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="Terminology"
          actions={
            <Button onClick={() => setShowForm(true)} data-guide="terminology.new">
              New term base
            </Button>
          }
        />

        <CreateTermBaseDialog
          open={showForm}
          onClose={() => setShowForm(false)}
          // Open the new base straight away so the user can start adding concepts.
          onCreated={(id) => { setShowForm(false); navigate(`/terminology/${id}`) }}
        />

        {/* Creator filter. Shown when there's something to actually
            distinguish (2+ known creators), or when a filter is already active
            — otherwise a URL-set filter would have no way to be cleared from
            the UI. */}
        {(creators.length > 1 || createdBy != null) && (
          <div className="flex items-center gap-2 mb-4" data-testid="term-base-filter-bar">
            <CreatedByFilterChip
              value={createdBy}
              myUserId={me?.id ?? null}
              options={creators}
              onChange={setCreatedBy}
              testId="term-base-filter-created-by"
            />
          </div>
        )}

        {loadFailed && <LoadFailed what="term bases" error={error} onRetry={refetch} />}

        <div className="space-y-2">
          {bases?.map(base => {
            // `languages` is optional-chained on purpose: it's derived by the
            // list query, so any row that entered the cache from a create
            // response lacks it. One missing field must not
            // take the whole page down.
            const present = (base.languages?.length ?? 0) > 0
              ? base.languages
              : [base.source_language, ...(base.target_languages ?? [])]
            const targets = present.filter(l => l && l !== base.source_language)
            return (
            <Link
              key={base.id}
              to={`/terminology/${base.id}`}
              data-testid={`term-base-row-${base.id}`}
              className={listCardClass}
            >
              <div className={listCardHeaderClass}>
                <div className={`${listCardTitleClass} gap-3`}>
                  {/* Name shown in full; a long language list collapses into "+N". */}
                  <div className="font-medium text-gray-900 dark:text-gray-100 shrink-0">{base.name}</div>
                  <LanguagePair source={base.source_language} targets={targets} separator="|" max={TM_LANGS_SHOWN} className="text-gray-500 dark:text-gray-400" />
                  <span className="text-xs text-gray-400 dark:text-gray-500 shrink-0">
                    {base.entry_count} {base.entry_count === 1 ? 'concept' : 'concepts'}
                  </span>
                </div>
                <div className="flex items-center gap-2 ml-3 shrink-0">
                  <span className="text-xs text-gray-400 dark:text-gray-500 inline-flex items-center gap-1">
                    Created {formatDate(base.created_at)} by
                    <CreatorLabel name={base.created_by_name} className="text-xs" />
                  </span>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <DeleteButton
                          aria-label="Delete"
                          onClick={e => handleDelete(e, base.id, base.name)}
                        />
                      }
                    />
                    <TooltipContent>Delete term base</TooltipContent>
                  </Tooltip>
                </div>
              </div>
            </Link>
            )
          })}
          {bases?.length === 0 && (
            <EmptyState
              message={createdBy != null
                ? 'No term bases match the current filter.'
                : 'No term bases yet. Create one to get started.'}
            />
          )}
        </div>

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title={`Delete term base "${deleteTarget?.name}"?`}
          description="This will also delete all its terminology entries."
          onConfirm={confirmDelete}
        />
      </PageContainer>
    </TooltipProvider>
  )
}
