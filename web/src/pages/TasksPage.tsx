// TasksPage — jobs assigned on each job's current active workflow step, one
// card per task. This is the vendor's home page and a "my work" view for
// everyone else. Owner/admin see every assignee's tasks (each card names the
// assignee); project managers and linguists see only their own. Scoped
// server-side, tenant-scoped too.
//
// Filters live in the URL (TaskFilterBar); the list is cursor-paged with a
// "Load more" button. Clicking a card opens that job in the CAT editor, in a
// new tab.

import { useMemo } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { getMe, listTasksPage, type Task } from '../api'
import { queryKeys } from '../lib/query-keys'
import { parseTaskFilters } from '../lib/task-filters'
import { isAdminRole } from '../lib/roles'
import { deadlineChipProps, type DeadlineBand } from '../lib/deadline'
import { isAIStepRunning } from '../lib/project-logic'
import { editorPath, EDITOR_LINK_TARGET, openEditorTab } from '../lib/editor-link'
import { useRefreshOnReturn } from '../hooks/useRefreshOnReturn'
import { cn } from '@/lib/utils'
import { Chip, chipTextClass } from '@/components/ui/chip'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LanguagePair } from '@/components/ui/language-code'
import { CountMeta } from '@/components/ui/count-meta'
import { DotSeparator } from '@/components/ui/dot-separator'
import { listCardClass } from '@/components/list/list-row'
import { LoadMoreButton } from '@/components/list/LoadMoreButton'
import { EditableTitle } from '@/components/list/EditableTitle'
import { TaskFilterBar } from '../components/task/TaskFilterBar'

const PAGE_SIZE = 50

const isRunning = (t: Task) => isAIStepRunning({ pipeline_status: t.status, current_step: t.current_step })

// Translated segments out of the total once the job is segmented, the word
// count before that.
function TaskProgress({ task: t }: { task: Task }) {
  if (t.total_count != null && t.total_count > 0) {
    return (
      <span className="tabular-nums">
        {(t.translated_count ?? 0).toLocaleString()} / <CountMeta items={[{ count: t.total_count, noun: 'segment' }]} />
      </span>
    )
  }
  return <CountMeta items={[{ count: t.word_count, noun: 'word' }]} />
}

export default function TasksPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [params] = useSearchParams()
  const filters = useMemo(() => parseTaskFilters(params), [params])
  // parseTaskFilters adds keys in a fixed order, so this string is stable.
  const filtersKey = useMemo(() => JSON.stringify(filters), [filters])

  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })

  // The editor tab moves a task on (approved segments, a completed step).
  const refreshOnReturn = useRefreshOnReturn(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
  })
  const openTask = (path: string) => {
    refreshOnReturn()
    openEditorTab(path, navigate)
  }

  const {
    data, error, isError, isFetchNextPageError, refetch,
    fetchNextPage, hasNextPage, isFetchingNextPage, isLoading,
  } = useInfiniteQuery({
    queryKey: [...queryKeys.tasks.paginated, filtersKey] as const,
    queryFn: ({ pageParam }) => listTasksPage(filters, pageParam, PAGE_SIZE),
    initialPageParam: '',
    getNextPageParam: lastPage => (lastPage.has_more ? lastPage.cursor : undefined),
    // Keep the current cards on screen while a new filter loads, so typing in
    // the search box doesn't unmount the page and drop focus.
    placeholderData: keepPreviousData,
    // Poll while any task is mid-QE+APE so the card unlocks automatically when
    // the step completes (the editor is locked while it runs).
    refetchInterval: q =>
      (q.state.data?.pages ?? []).some(p => p.data.some(isRunning)) ? 2000 : false,
  })
  const tasks = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])

  if (isLoading) return <LoadingPlaceholder />

  const hasActiveFilters = Object.keys(filters).length > 0
  const emptyMessage = hasActiveFilters
    ? 'No tasks match these filters.'
    : isAdminRole(me?.role)
      ? 'No open tasks in your organization yet.'
      : "No tasks assigned to you yet. When a project manager assigns you a job, it'll show up here."

  // No data at all means the first page failed; a failed next page keeps the
  // cards already loaded.
  const loadFailed = isError && !data

  return (
    <PageContainer>
      <PageHeader title="Tasks" />
      <TaskFilterBar />

      {loadFailed && <LoadFailed what="tasks" error={error} onRetry={() => refetch()} />}

      <div data-guide="tasks.list" className="space-y-2">
        {tasks.map(t => {
          // Hard-block opening the task while its QE+APE step runs.
          const aiRunning = isRunning(t)
          const path = editorPath(t.project_id, t.job_id)
          return (
            <div
              key={t.job_id}
              data-testid={`task-row-${t.job_id}`}
              onClick={aiRunning ? undefined : () => openTask(path)}
              title={aiRunning ? 'QE+APE running — this task opens when it completes' : undefined}
              className={cn(
                listCardClass,
                aiRunning
                  ? 'cursor-not-allowed opacity-90 hover:border-gray-200 dark:hover:border-gray-800'
                  : 'cursor-pointer',
              )}
            >
              <div className="flex max-md:flex-wrap items-center justify-between gap-4 gap-y-1">
                <div className="flex items-center gap-2 min-w-0">
                  {aiRunning ? (
                    <span className="flex min-w-0" data-testid={`task-open-${t.job_id}`}>
                      <EditableTitle name={t.file_name} canEdit={false} className="font-medium" />
                    </span>
                  ) : (
                    // A real link, so middle-click and Cmd+click work; the card
                    // handler must not open a second tab on top of it.
                    <Link
                      to={path}
                      {...EDITOR_LINK_TARGET}
                      onClick={e => { e.stopPropagation(); refreshOnReturn() }}
                      className="flex min-w-0 group-hover:underline"
                      data-testid={`task-open-${t.job_id}`}
                    >
                      <EditableTitle name={t.file_name} canEdit={false} focusable={false} className="font-medium" />
                    </Link>
                  )}
                  {aiRunning && (
                    <Chip color="blue" className="shrink-0 animate-pulse" data-testid={`task-qeape-${t.job_id}`}>
                      QE+APE running
                    </Chip>
                  )}
                </div>
                <DeadlineText deadline={t.deadline} />
              </div>

              <div className="flex max-md:flex-wrap items-center justify-between gap-4 gap-y-1 mt-1 text-sm text-gray-500">
                <div className="flex max-md:flex-wrap items-center gap-3 gap-y-1 min-w-0">
                  <span className="flex min-w-0 items-center gap-1" data-testid="task-project">
                    <EditableTitle name={t.project_name} canEdit={false} />
                    {t.customer_name ? (
                      <>
                        <DotSeparator className="mx-0 shrink-0 text-gray-400" />
                        <EditableTitle name={t.customer_name} canEdit={false} className="text-gray-400" />
                      </>
                    ) : null}
                  </span>
                  <LanguagePair source={t.source_language} targets={t.target_language} className="text-xs shrink-0" />
                </div>
                <div className="flex max-md:flex-wrap min-w-0 items-center gap-3 gap-y-1 text-xs">
                  <span className="flex min-w-0 items-center gap-1" data-testid="task-step">
                    <span className="shrink-0">Step:</span>
                    <EditableTitle name={t.step_name} canEdit={false} />
                  </span>
                  <span className="shrink-0" data-testid="task-progress"><TaskProgress task={t} /></span>
                  {t.assignee_name != null && (
                    <span className="shrink-0" data-testid="task-assignee">Assignee: {t.assignee_name || '—'}</span>
                  )}
                </div>
              </div>
            </div>
          )
        })}

        {tasks.length === 0 && !loadFailed && <EmptyState message={emptyMessage} />}

        {isFetchNextPageError ? (
          <div className="pt-4">
            <LoadFailed what="next tasks" error={error} onRetry={() => fetchNextPage()} />
          </div>
        ) : (
          <LoadMoreButton
            hasNextPage={hasNextPage}
            isFetchingNextPage={isFetchingNextPage}
            onLoadMore={() => fetchNextPage()}
            testId="load-more-tasks"
          />
        )}
      </div>
    </PageContainer>
  )
}

// Overdue and due within 24 hours take their chip colour; every other
// deadline is grey, like the rest of the row.
const COLOURED_BANDS: ReadonlySet<DeadlineBand> = new Set(['overdue', 'approaching'])

/** The deadline as plain text, in the same style as the row's Step and Assignee. */
function DeadlineText({ deadline }: { deadline: string | null }) {
  const d = deadlineChipProps(deadline, { exact: true })
  return (
    <span
      className={cn('shrink-0 text-xs', COLOURED_BANDS.has(d['data-band']) ? chipTextClass(d.color) : 'text-gray-500')}
      title={d.title}
      data-band={d['data-band']}
      data-testid={d['data-testid']}
    >
      {d.children}
    </span>
  )
}
