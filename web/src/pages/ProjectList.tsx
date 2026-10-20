// ProjectList — paginated, filtered project list.
//
//   - Filter state lives in URL via useSearchParams (ProjectFilterBar).
//   - useInfiniteQuery drives cursor pagination using listProjectsFilteredPage.
//     The query key includes the filters so changing filters auto-refetches
//     from page 1.
//   - An explicit "Load more" button when has_more (no auto-infinite-scroll).
//   - Status renders as a Chip off STATUS_COLORS / STATUS_LABELS.
//   - Creator column uses CreatorLabel, fed by the list query's created_by_name
//     JOIN; the matching filter chip is CreatedByFilterChip.
//   - The delete button is shown to owner/admin only.

import { useMemo, useState } from 'react'
import { useInfiniteQuery, useQuery, keepPreviousData } from '@tanstack/react-query'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  listProjectsFilteredPage,
  deleteProject,
  getMe,
} from '../api'
import { queryKeys } from '../lib/query-keys'
import { parseProjectFilters } from '../lib/project-filters'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { formatDate } from '../utils'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { hoverActionClass } from '@/components/list/hover-action'
import { listCardClass, listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'
import { LoadMoreButton } from '@/components/list/LoadMoreButton'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { ProjectFilterBar } from '../components/project/ProjectFilterBar'
import { Chip } from '@/components/ui/chip'
import { STATUS_LABELS, STATUS_COLORS } from '../components/project/ProjectStatusSelect'
import { CreatorLabel } from '@/components/user/CreatorLabel'
import { deadlineChipProps } from '../lib/deadline'
import { LanguagePair } from '@/components/ui/language-code'
import { Copy } from 'lucide-react'
import { TakeTourButton } from '@/components/guide/TakeTourButton'

const PAGE_SIZE = 50

export default function ProjectList() {
  const [params] = useSearchParams()
  const filters = useMemo(() => parseProjectFilters(params), [params])
  // Stringify filters for the query key — useInfiniteQuery uses referential
  // equality on the key array, so we serialize to a primitive that React Query
  // can compare cheaply. JSON.stringify is stable here because
  // parseProjectFilters builds keys in a fixed order.
  const filtersKey = useMemo(() => JSON.stringify(filters), [filters])

  // Delete is owner/admin in the list.
  // Duplicate = project creation, which is manager-level.
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isAdmin = isAdminRole(me?.role)
  const isManager = isManagerRole(me?.role)
  const navigate = useNavigate()

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
    // Key built from the factory prefix so invalidations from any mutation
    // (rename, delete, status/customer PATCH from ProjectDetail) prefix-match
    // and refresh ALL filter variants of this query at once.
    queryKey: [...queryKeys.projects.paginated, filtersKey] as const,
    queryFn: ({ pageParam = 0 }) => listProjectsFilteredPage(filters, pageParam, PAGE_SIZE),
    initialPageParam: 0,
    getNextPageParam: (lastPage) => (lastPage.has_more ? lastPage.cursor : undefined),
    // Keep the previous results on screen while a new filter/search query
    // loads. Without this, changing `q` produces a brand-new query key with no
    // cached data, so `isLoading` flips true and the whole page unmounts into
    // <LoadingPlaceholder/> and back — which re-renders everything and steals
    // focus from the search input on every keystroke.
    placeholderData: keepPreviousData,
  })

  const projects = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])

  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)

  // Rename lives on the project detail page (pencil next to the title), not
  // in this list.
  const deleteMut = useMutationWithToast({
    mutationFn: deleteProject,
    successMessage: 'Project deleted',
    errorMessage: 'Delete failed',
    // projects.creators: deleting someone's last project drops them from the
    // "Created by" options.
    invalidateKeys: [queryKeys.projects.all, queryKeys.projects.paginated, queryKeys.projects.creators],
  })

  const handleDelete = (e: React.MouseEvent, id: number, name: string) => {
    e.preventDefault()
    e.stopPropagation()
    setDeleteTarget({ id, name })
  }

  if (isLoading) return <LoadingPlaceholder />

  const hasActiveFilters = Object.keys(filters).length > 0
  // No data at all means the first page failed.
  const loadFailed = isError && !data

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="Projects"
          actions={
            <Link to="/projects/new">
              <Button data-guide="projects.new">New project</Button>
            </Link>
          }
        />

        <ProjectFilterBar myUserId={me?.id ?? null} />

        {loadFailed && <LoadFailed what="projects" error={error} onRetry={() => refetch()} />}

        <div className="space-y-2">
          {projects.map((p, i) => (
            <Link
              key={p.id}
              // Tours point at the first row: the whole list fills the screen
              // and would leave the tour card no room beside it.
              data-guide={i === 0 ? 'projects.list' : undefined}
              // A draft reopens in the wizard, where it was left.
              to={p.status === 'draft' ? `/projects/new?draft=${p.id}` : `/projects/${p.id}`}
              data-testid={`project-row-${p.id}`}
              className={listCardClass}
            >
              <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                  <div className={listCardHeaderClass}>
                    <div className={`${listCardTitleClass} gap-3`}>
                      <div className="font-medium text-gray-900 dark:text-gray-100 truncate">{p.name || <span className="text-gray-400 dark:text-gray-500">Untitled draft</span>}</div>
                      {p.status && (
                        <Chip color={STATUS_COLORS[p.status]} data-testid={`project-status-badge-${p.status}`}>
                          {STATUS_LABELS[p.status]}
                        </Chip>
                      )}
                    </div>
                    <span className="ml-3 shrink-0 text-xs text-gray-400 dark:text-gray-500">
                      Created {formatDate(p.created_at)}
                      {p.updated_at !== p.created_at && <> · Updated {formatDate(p.updated_at)}</>}
                    </span>
                  </div>
                  <div className="flex max-md:flex-wrap items-center justify-between gap-y-1 text-sm text-gray-500 dark:text-gray-400 mt-1">
                    <div><LanguagePair source={p.source_language} targets={p.target_languages} /></div>
                    <div className="flex max-md:flex-wrap items-center gap-3 gap-y-1 text-xs">
                      {/* Deadline pill — band + tooltip carry the full
                          datetime; the row stays scannable (red overdue /
                          amber ≤24h). */}
                      <Chip {...deadlineChipProps(p.deadline, { done: p.status === 'completed' })} />
                      <div className="flex items-center gap-1.5">
                        <span className="text-gray-400 dark:text-gray-500">Customer:</span>
                        {/* Customer is a built-in label, resolved server-side so
                            the row needs no second fetch. */}
                        <span className={p.customer_name ? '' : 'text-gray-400 dark:text-gray-500'}>
                          {p.customer_name || 'None'}
                        </span>
                      </div>
                      <div className="flex items-center gap-1.5">
                        <span className="text-gray-400 dark:text-gray-500">Created by:</span>
                        <CreatorLabel name={p.created_by_name} />
                      </div>
                    </div>
                  </div>
                </div>
                {/* Row actions get their own column, centred on both lines. A
                    draft can't be duplicated; its empty slot keeps Delete in
                    line with the rows above and below. */}
                {isManager && (
                  <div className="flex items-center gap-1 shrink-0" data-testid={`project-actions-${p.id}`}>
                    {p.status === 'draft' ? <span aria-hidden className="size-8" /> : (
                      <Tooltip>
                        <TooltipTrigger
                          render={
                            <Button variant="ghost" size="icon"
                              aria-label="Duplicate"
                              onClick={e => {
                                e.preventDefault()
                                e.stopPropagation()
                                navigate(`/projects/new?fromProject=${p.id}`)
                              }}
                              className={hoverActionClass('muted')}
                              data-testid={`duplicate-project-${p.id}`}
                            />
                          }
                        >
                          <Copy />
                        </TooltipTrigger>
                        <TooltipContent>New project with the same details and settings (no files)</TooltipContent>
                      </Tooltip>
                    )}
                    {isAdmin && (
                      <Tooltip>
                        <TooltipTrigger
                          render={
                            <DeleteButton
                              aria-label="Delete"
                              onClick={e => handleDelete(e, p.id, p.name)}
                              disabled={deleteMut.isPending}
                            />
                          }
                        />
                        <TooltipContent>Delete project</TooltipContent>
                      </Tooltip>
                    )}
                  </div>
                )}
              </div>
            </Link>
          ))}

          {projects.length === 0 && !loadFailed && (
            <EmptyState
              message={hasActiveFilters
                ? 'No projects match the current filters. Clear filters to see all projects.'
                : 'No projects yet. Create one to get started.'}
            />
          )}
          {projects.length === 0 && !hasActiveFilters && !loadFailed && (
            <div className="flex justify-center">
              <TakeTourButton playbook="demo-overview" label="New here? Take the tour" />
            </div>
          )}

          <LoadMoreButton
            hasNextPage={hasNextPage}
            isFetchingNextPage={isFetchingNextPage}
            onLoadMore={() => fetchNextPage()}
            testId="load-more-projects"
          />
        </div>

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title={`Delete project "${deleteTarget?.name}"?`}
          description="This will also delete all its files and translations."
          onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
        />
      </PageContainer>
    </TooltipProvider>
  )
}
