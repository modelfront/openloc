import { useMemo, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTMCollections, renameTMCollection, deleteTMCollection, getMe } from '../api'
import { queryKeys } from '../lib/query-keys'
import { formatDate } from '../utils'
import { useCRUDList } from '../hooks/use-crud-list'
import { useCreatedByParam } from '../hooks/use-created-by-param'
import { distinctCreators } from '../lib/creators'
import { CreateTMDialog } from '../components/CreateTMDialog'
import { TMProfilesList } from '../components/tm/TMProfilesList'
import { isAdminRole } from '../lib/roles'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { SubNav } from '@/components/layout/SubNav'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { CreatedByFilterChip } from '@/components/list'
import { CreatorLabel } from '@/components/user/CreatorLabel'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { DotSeparator } from '@/components/ui/dot-separator'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { LanguagePair } from '@/components/ui/language-code'
import { TM_LANGS_SHOWN } from '@/lib/tm-display'
import { listCardClass, listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'

// Two tabs, each with its own URL so a profile page can link back to the right
// one: /memories (the TMs) and /memories/profiles (the reusable bundles).
const PROFILES_PATH = '/memories/profiles'
const TM_TABS = [
  { to: '/memories', label: 'Memories', end: true },
  { to: PROFILES_PATH, label: 'TM profiles' },
]

export default function TMList() {
  const [createdBy, setCreatedBy] = useCreatedByParam()
  const { pathname } = useLocation()
  const tab: 'memories' | 'profiles' = pathname.startsWith(PROFILES_PATH) ? 'profiles' : 'memories'
  const [newProfileOpen, setNewProfileOpen] = useState(false)
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })

  const {
    items: allTms, isLoading, loadFailed, error, refetch,
    showForm, setShowForm,
    // Rename now lives on the TM detail page (pencil next to the title).
    deleteTarget, setDeleteTarget, handleDelete, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.tms.all,
    queryFn: listTMCollections,
    // Create is owned by CreateTMDialog (it captures source/target/customer and
    // does its own cache update + toast), so this createFn is never invoked.
    createFn: () => Promise.resolve(),
    renameFn: ({ id, name }) => renameTMCollection(id, name),
    deleteFn: deleteTMCollection,
    entityName: 'TM',
  })

  // Client-side creator filter — the list is fetched whole (a handful of rows
  // per tenant), so there's no server-side filter to add.
  const tms = useMemo(
    () => createdBy == null ? allTms : allTms?.filter(tm => tm.created_by === createdBy),
    [allTms, createdBy],
  )

  // Creator options come from the rows themselves, not the org member list —
  // see lib/creators.ts for why.
  const creators = useMemo(() => distinctCreators(allTms), [allTms])

  if (isLoading) return <LoadingPlaceholder />

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="Translation memories"
          actions={tab === 'memories' ? (
            <Button onClick={() => setShowForm(true)} data-guide="tms.new">
              New TM
            </Button>
          ) : (
            <Button onClick={() => setNewProfileOpen(true)} data-testid="new-tm-profile" data-guide="tm_profiles.new">
              New TM profile
            </Button>
          )}
        />

        <SubNav className="mb-4" items={TM_TABS} />

        {tab === 'profiles' ? (
          <TMProfilesList
            canDelete={isAdminRole(me?.role)}
            createOpen={newProfileOpen}
            onCreateClose={() => setNewProfileOpen(false)}
          />
        ) : (
        <>

        <CreateTMDialog
          open={showForm}
          onClose={() => setShowForm(false)}
          onCreated={() => setShowForm(false)}
        />

        {/* Creator filter. Shown when there's something to actually
            distinguish (2+ known creators), or when a filter is already active
            — otherwise a URL-set filter would have no way to be cleared from
            the UI. */}
        {(creators.length > 1 || createdBy != null) && (
          <div className="flex items-center gap-2 mb-4" data-testid="tm-filter-bar">
            <CreatedByFilterChip
              value={createdBy}
              myUserId={me?.id ?? null}
              options={creators}
              onChange={setCreatedBy}
              testId="tm-filter-created-by"
            />
          </div>
        )}

        {loadFailed && <LoadFailed what="translation memories" error={error} onRetry={refetch} />}

        <div className="space-y-2" data-guide="tms.list">
          {tms?.map(tm => {
            const hasLangs = !!tm.source_language && (tm.target_languages?.length ?? 0) > 0
            return (
            <Link
              key={tm.id}
              to={`/memories/${tm.id}`}
              data-testid={`tm-row-${tm.id}`}
              className={listCardClass}
            >
              <div className={listCardHeaderClass}>
                <div className={`${listCardTitleClass} gap-2`}>
                  <div className="font-medium text-gray-900 dark:text-gray-100 truncate">{tm.name}</div>
                  {/* Meta bits are separated only by DotSeparators (no flex gap
                      here) so there's no extra space to the right of the
                      languages. */}
                  {(hasLangs || tm.customer_name || tm.entry_count != null) && (
                    <div
                      className="flex items-center text-xs text-gray-400 dark:text-gray-500 shrink-0"
                      title={hasLangs ? `${tm.source_language} → ${tm.target_languages!.join(', ')}` : undefined}
                    >
                      {hasLangs && (
                        <LanguagePair
                          source={tm.source_language!}
                          targets={tm.target_languages!}
                          max={TM_LANGS_SHOWN}
                          className="text-gray-500 dark:text-gray-400"
                        />
                      )}
                      {tm.customer_name && (
                        <>
                          {hasLangs && <DotSeparator />}
                          <span>{tm.customer_name}</span>
                        </>
                      )}
                      {tm.entry_count != null && (
                        <>
                          {(hasLangs || tm.customer_name) && <DotSeparator />}
                          <span>{tm.entry_count.toLocaleString()} {tm.entry_count === 1 ? 'entry' : 'entries'}</span>
                        </>
                      )}
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 ml-3 shrink-0">
                  <span className="text-xs text-gray-400 dark:text-gray-500 inline-flex items-center gap-1">
                    Created {formatDate(tm.created_at)} by
                    <CreatorLabel name={tm.created_by_name} className="text-xs" />
                  </span>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <DeleteButton
                          aria-label="Delete"
                          onClick={e => handleDelete(e, tm.id, tm.name)}
                        />
                      }
                    />
                    <TooltipContent>Delete TM</TooltipContent>
                  </Tooltip>
                </div>
              </div>
            </Link>
            )
          })}
          {tms?.length === 0 && (
            <EmptyState
              message={createdBy != null
                ? 'No translation memories match the current filter.'
                : 'No translation memories yet. Create one to get started.'}
            />
          )}
        </div>

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title={`Delete TM "${deleteTarget?.name}"?`}
          description="This will also delete all its entries."
          onConfirm={confirmDelete}
        />

        </>
        )}
      </PageContainer>
    </TooltipProvider>
  )
}
