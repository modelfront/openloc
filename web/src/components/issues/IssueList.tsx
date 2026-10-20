import { useMemo } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { listIssues, type Issue, type IssueFilter, type IssueScope } from '@/api'
import { issuesKey } from '@/lib/query-keys'
import { ISSUE_AREA_LABELS, ISSUE_KIND_ICONS, ISSUE_KIND_LABELS, issueListPath } from '@/lib/issue-labels'
import { SectionInfo } from '@/components/ui/section-info'
import { EditableTitle } from '@/components/list/EditableTitle'
import { DataTable } from '@/components/ui/data-table'
import { LoadMoreButton } from '@/components/list/LoadMoreButton'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { DeleteButton } from '@/components/ui/delete-button'
import {
  IssuePriorityChip, IssueSeverityChip, IssueStatusChip, IssueVerificationChip, IssueWaitingOnChip, When,
} from './IssueParts'
import { useDeleteIssue } from './useDeleteIssue'

/**
 * The kind as an icon with a tooltip, the title (in full on hover when cut off),
 * then, for the support team, the assistant's check, and a bug's severity at
 * the right end of the cell however long the title is.
 */
const titleColumn: ColumnDef<Issue> = {
  id: 'title',
  header: 'Title',
  cell: ({ row }) => {
    const issue = row.original
    const kind = ISSUE_KIND_LABELS[issue.kind]
    return (
      <span className="flex min-w-0 items-center gap-2">
        <SectionInfo label={kind} icon={ISSUE_KIND_ICONS[issue.kind]} data-testid="issue-kind-icon">{kind}</SectionInfo>
        <span className="flex min-w-0 flex-1">
          <EditableTitle name={issue.title} canEdit={false} className="font-medium" />
        </span>
        {issue.chat_verification && <IssueVerificationChip verification={issue.chat_verification} size="small" />}
        {issue.severity && <IssueSeverityChip severity={issue.severity} size="small" />}
      </span>
    )
  },
}

const areaColumn: ColumnDef<Issue> = {
  id: 'area', header: 'Area', size: 150,
  cell: ({ row }) => <span className="truncate block">{ISSUE_AREA_LABELS[row.original.area]}</span>,
}
/** The status, and below it who has to act next while the issue needs work. */
const statusColumn = (scope: IssueScope): ColumnDef<Issue> => ({
  id: 'status', header: 'Status', size: 150,
  cell: ({ row }) => (
    <span className="flex flex-col items-start gap-1">
      <IssueStatusChip status={row.original.status} />
      <IssueWaitingOnChip issue={row.original} scope={scope} size="small" />
    </span>
  ),
})
/** Operators only. */
const priorityColumn: ColumnDef<Issue> = {
  id: 'priority', header: 'Priority', size: 90,
  cell: ({ row }) => row.original.priority ? <IssuePriorityChip priority={row.original.priority} /> : null,
}
/** Operators only. */
const assigneeColumn: ColumnDef<Issue> = {
  id: 'assignee', header: 'Assignee', size: 150,
  cell: ({ row }) => <span className="truncate block" data-testid="issue-row-assignee">{row.original.assignee?.name ?? ''}</span>,
}
const updatedColumn = (sort: SortProps): ColumnDef<Issue> => ({
  id: 'updated', header: 'Updated', size: 110,
  cell: ({ row }) => <When at={row.original.updated_at} />,
  meta: { sort: { direction: sort.oldestFirst ? 'asc' : 'desc', onToggle: sort.onToggleSort } },
})

/** Operators see the org over the member; an org's managers see the member. */
const reporterColumn = (scope: IssueScope): ColumnDef<Issue> => ({
  id: 'reporter', header: 'Reported by', size: 190,
  cell: ({ row }) => {
    const { org_name, reporter_name, reporter_email } = row.original
    const [first, second] = scope === 'platform' ? [org_name, reporter_email] : [reporter_name, reporter_email]
    return (
      <span className="flex min-w-0 flex-col" title={`${first} · ${second}`} data-testid="issue-reporter">
        <span className="truncate">{first}</span>
        <span className="truncate text-xs text-muted-foreground">{second}</span>
      </span>
    )
  },
})

/** The trailing hover action: shown only where the server allows the delete. */
const deleteColumn = (onDelete: (issue: Issue) => void): ColumnDef<Issue> => ({
  id: 'actions', size: 44,
  cell: ({ row }) => row.original.can_delete && (
    // Deleting must not also open the issue.
    <div className="flex justify-end" onClick={e => e.stopPropagation()}>
      <DeleteButton
        size="icon-xs"
        className="opacity-0 transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
        title="Delete issue"
        aria-label={`Delete issue ${row.original.title}`}
        data-testid={`issue-delete-${row.original.id}`}
        onClick={() => onDelete(row.original)}
      />
    </div>
  ),
})

interface SortProps {
  /** By last update, newest first unless set. */
  oldestFirst: boolean
  onToggleSort: () => void
}

const columnsFor = (scope: IssueScope, showReporter: boolean, sort: SortProps, onDelete: (issue: Issue) => void): ColumnDef<Issue>[] => [
  titleColumn, ...(showReporter ? [reporterColumn(scope)] : []), areaColumn,
  ...(scope === 'platform' ? [priorityColumn, assigneeColumn] : []),
  statusColumn(scope), updatedColumn(sort), deleteColumn(onDelete),
]

/** A paged table of issues: the ones the member may see in their org, or every org's for operators. */
export function IssueList({ scope, filter = {}, showReporter, onToggleSort, emptyMessage }: {
  scope: IssueScope
  /** Includes the order (oldestFirst), which the Updated header toggles. */
  filter?: IssueFilter
  onToggleSort: () => void
  /** A column with who reported each issue: for operators and an org's managers. */
  showReporter: boolean
  emptyMessage: string
}) {
  const navigate = useNavigate()
  const queryKey = [...issuesKey(scope), 'list', filter] as const
  const { data, error, isError, isLoading, refetch, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => listIssues(scope, pageParam, filter),
    initialPageParam: '',
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  })
  const issues = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])
  const oldestFirst = !!filter.oldestFirst
  const { ask, dialog } = useDeleteIssue(scope)
  const columns = useMemo(
    () => columnsFor(scope, showReporter, { oldestFirst, onToggleSort }, ask),
    [scope, showReporter, oldestFirst, onToggleSort, ask],
  )

  if (isLoading) return <LoadingPlaceholder />
  if (isError && issues.length === 0) return <LoadFailed what="issues" error={error} onRetry={() => void refetch()} />
  const base = issueListPath(scope)
  return (
    <>
      <DataTable
        columns={columns}
        data={issues}
        emptyMessage={emptyMessage}
        onRowClick={issue => navigate(`${base}/${issue.id}`)}
        rowTestId={issue => `issue-row-${issue.id}`}
        rowClassName="group"
      />
      {dialog}
      <LoadMoreButton hasNextPage={!!hasNextPage} isFetchingNextPage={isFetchingNextPage}
        onLoadMore={() => void fetchNextPage()} testId="issues-load-more" />
    </>
  )
}
