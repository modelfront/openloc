import { useMemo } from 'react'
import { keepPreviousData, useInfiniteQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef } from '@tanstack/react-table'
import { listIssues, type Issue, type IssueFilter, type IssueScope } from '@/api'
import { issuesKey } from '@/lib/query-keys'
import { openRowLink } from '@/lib/row-link'
import { ISSUE_AREA_LABELS, ISSUE_KIND_ICONS, ISSUE_KIND_LABELS, issueListPath } from '@/lib/issue-labels'
import { DEFAULT_SORT, MAX_SORT_LEVELS, naturalDesc, nextSort, type IssueSortKey, type IssueSortLevel } from '@/lib/issue-sort'
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

/** What the Issue column reads from the table: the digits of the widest number shown. */
interface IssueTableMeta {
  numberDigits: number
}

/**
 * The kind as an icon with a tooltip and the number, both in the icon's muted
 * color, then the title (in full on hover when cut off), then, for the support
 * team, the assistant's check, and a bug's severity at the right end of the
 * cell however long the title is.
 */
const titleColumn: ColumnDef<Issue> = {
  id: 'title',
  header: 'Issue',
  cell: ({ row, table }) => {
    const issue = row.original
    const { numberDigits } = table.options.meta as unknown as IssueTableMeta
    const kind = ISSUE_KIND_LABELS[issue.kind]
    return (
      <span className="flex min-w-0 items-center gap-2">
        <span className="flex min-w-0 flex-1 items-center gap-1.5">
          <span className="flex shrink-0 items-center gap-1">
            <SectionInfo label={kind} icon={ISSUE_KIND_ICONS[issue.kind]} data-testid="issue-kind-icon">{kind}</SectionInfo>
            {/* Every number is as wide as the widest, so the titles line up. */}
            <span className="tabular-nums text-muted-foreground/70" style={{ minWidth: `${numberDigits}ch` }} data-testid="issue-number">{issue.id}</span>
          </span>
          <span className="flex min-w-0 flex-1">
            <EditableTitle name={issue.title} canEdit={false} className="font-medium" />
          </span>
        </span>
        {issue.chat_verification && <IssueVerificationChip verification={issue.chat_verification} size="small" />}
        {issue.severity && <IssueSeverityChip severity={issue.severity} size="small" />}
      </span>
    )
  },
}

const areaColumn: ColumnDef<Issue> = {
  id: 'area', header: 'Area', size: 130,
  cell: ({ row }) => <span className="truncate block">{ISSUE_AREA_LABELS[row.original.area]}</span>,
}
/** The status, and below it who has to act next while the issue needs work. */
const statusColumn = (scope: IssueScope): ColumnDef<Issue> => ({
  id: 'status', header: 'Status', size: 130,
  cell: ({ row }) => (
    <span className="flex flex-col items-start gap-1">
      <IssueStatusChip status={row.original.status} />
      <IssueWaitingOnChip issue={row.original} scope={scope} size="small" />
    </span>
  ),
})
/** Operators only. */
const priorityColumn: ColumnDef<Issue> = {
  id: 'priority', header: 'Priority', size: 115,
  cell: ({ row }) => row.original.priority ? <IssuePriorityChip priority={row.original.priority} /> : null,
}
/** Operators only. */
const assigneeColumn: ColumnDef<Issue> = {
  id: 'assignee', header: 'Assignee', size: 130,
  cell: ({ row }) => <span className="truncate block" data-testid="issue-row-assignee">{row.original.assignee?.name ?? ''}</span>,
}
const updatedColumn: ColumnDef<Issue> = {
  id: 'updated', header: 'Updated', size: 110,
  cell: ({ row }) => <When at={row.original.updated_at} />,
}

/** Operators see the org over the member; an org's managers see the member. */
const reporterColumn = (scope: IssueScope): ColumnDef<Issue> => ({
  id: 'reporter', header: 'Reported by', size: 160,
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

const MORE_LEVELS = `Shift+click to sort by more than one column (up to ${MAX_SORT_LEVELS}).`

/**
 * column, sortable by key: its header shows how the list is sorted by it, and
 * a click works out the next order. The Issue column sorts by type, then a
 * bug's severity, not by the title text.
 */
function sortable(column: ColumnDef<Issue>, key: IssueSortKey, sort: IssueSortLevel[], onSortChange: (sort: IssueSortLevel[]) => void): ColumnDef<Issue> {
  const i = sort.findIndex(l => l.key === key)
  return {
    ...column,
    meta: {
      ...column.meta,
      sort: {
        direction: i < 0 ? null : sort[i].desc ? 'desc' : 'asc',
        rank: i >= 0 && sort.length > 1 ? i + 1 : undefined,
        // Down for the column's natural order, whichever way that runs.
        arrow: i >= 0 && sort[i].desc !== naturalDesc(key) ? 'up' : 'down',
        hint: key === 'kind' ? `Sorts by type, then severity. ${MORE_LEVELS}` : MORE_LEVELS,
        onToggle: (shift: boolean) => onSortChange(nextSort(sort, key, shift)),
      },
    },
  }
}

interface SortProps {
  sort: IssueSortLevel[]
  onSortChange: (sort: IssueSortLevel[]) => void
}

const columnsFor = (scope: IssueScope, showReporter: boolean, { sort, onSortChange }: SortProps, onDelete: (issue: Issue) => void): ColumnDef<Issue>[] => {
  const by = (column: ColumnDef<Issue>, key: IssueSortKey) => sortable(column, key, sort, onSortChange)
  return [
    by(titleColumn, 'kind'), ...(showReporter ? [by(reporterColumn(scope), 'reporter')] : []), by(areaColumn, 'area'),
    ...(scope === 'platform' ? [by(priorityColumn, 'priority'), by(assigneeColumn, 'assignee')] : []),
    by(statusColumn(scope), 'status'), by(updatedColumn, 'updated'), deleteColumn(onDelete),
  ]
}

/** A paged table of issues: the ones the member may see in their org, or every org's for operators. */
export function IssueList({ scope, filter = {}, showReporter, onSortChange, emptyMessage }: {
  scope: IssueScope
  /** Includes the order (sort), which the column headers change. */
  filter?: IssueFilter
  onSortChange: (sort: IssueSortLevel[]) => void
  /** A column with who reported each issue: for operators and an org's managers. */
  showReporter: boolean
  emptyMessage: string
}) {
  const navigate = useNavigate()
  const queryKey = [...issuesKey(scope), 'list', filter] as const
  const { data, error, isError, isFetchNextPageError, isLoading, isPlaceholderData, refetch, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => listIssues(scope, pageParam, filter),
    initialPageParam: '',
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
    // A new order or filter keeps the old rows, dimmed, until it loads, so the
    // headers stay put between clicks.
    placeholderData: keepPreviousData,
  })
  const issues = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])
  const meta = useMemo((): IssueTableMeta => ({
    numberDigits: issues.reduce((most, i) => Math.max(most, String(i.id).length), 1),
  }), [issues])
  const sort = filter.sort ?? DEFAULT_SORT
  const { ask, dialog } = useDeleteIssue(scope)
  const columns = useMemo(
    () => columnsFor(scope, showReporter, { sort, onSortChange }, ask),
    [scope, showReporter, sort, onSortChange, ask],
  )

  if (isLoading) return <LoadingPlaceholder />
  if (isError && issues.length === 0) return <LoadFailed what="issues" error={error} onRetry={() => void refetch()} />
  const base = issueListPath(scope)
  return (
    <>
      <DataTable
        columns={columns}
        data={issues}
        meta={meta as unknown as Record<string, unknown>}
        emptyMessage={emptyMessage}
        onRowClick={(issue, e) => openRowLink(e, `${base}/${issue.id}`, navigate)}
        onRowAuxClick={(issue, e) => openRowLink(e, `${base}/${issue.id}`, navigate)}
        rowTestId={issue => `issue-row-${issue.id}`}
        rowClassName="group"
        className={isPlaceholderData ? 'opacity-50 transition-opacity' : 'transition-opacity'}
      />
      {dialog}
      {isFetchNextPageError ? (
        // Retry loads the list again from the first page, so a cursor the
        // server refuses (one from before a deploy) is replaced.
        <div className="pt-4">
          <LoadFailed what="more issues" error={error} onRetry={() => void refetch()} />
        </div>
      ) : (
        <LoadMoreButton hasNextPage={!!hasNextPage} isFetchingNextPage={isFetchingNextPage}
          onLoadMore={() => void fetchNextPage()} testId="issues-load-more" />
      )}
    </>
  )
}
