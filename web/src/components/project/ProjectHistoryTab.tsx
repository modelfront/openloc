import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { ChevronsLeftRight, ChevronsRightLeft } from 'lucide-react'
import { listProjectEvents, type ProjectEvent } from '@/api/project-events'
import { listMembers } from '@/api/auth'
import { queryKeys } from '@/lib/query-keys'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/ui/data-table'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { ClearFiltersButton, FilterChip, FilterOption, LoadMoreButton } from '@/components/list'
import { useUrlParamSetters } from '@/hooks/useUrlParamSetters'
import {
  changesOf,
  eventActorName,
  flatChangeGroups,
  formatChangeValue,
  renderEventSentence,
  workflowStepsChangesOf,
  type ChangeGroup,
} from './project-event-render'

/**
 * Project history / audit log tab: who did what and when,
 * newest first — creation, metadata/settings changes, assignment swaps, file
 * lifecycle, job/workflow actions. Segment edits live in the CAT editor's
 * translation_history, not here.
 *
 * Rendered as a table (Type | Action | Changes | User | Date) with filter
 * chips for type and user. Filter state lives in URL params (evtype/evactor)
 * so filtered views survive reload and are shareable — same convention as
 * ProjectFilterBar and the Tasks / Files tab filters.
 *
 * Freshness: staleTime 0 + refetchOnMount 'always' instead of invalidation
 * wiring — the tabs primitive unmounts inactive panels, so every mutation happens
 * while this tab is unmounted and the refetch on open always sees fresh data.
 */
interface Props {
  projectId: number
}

// Category filters map to event_type prefixes ("file." matches every file
// event). The API accepts any prefix, so exact types can be added here later
// without backend changes.
const TYPE_OPTIONS: { value: string; label: string }[] = [
  // "Settings", not "Project": the project.* events are the project's
  // configuration log (settings, metadata, assignments, workflow blueprint).
  { value: 'project.', label: 'Settings' },
  { value: 'file.', label: 'Files' },
  { value: 'job.', label: 'Jobs' },
  { value: 'workflow.', label: 'Workflow' },
  { value: 'analysis.', label: 'Analyses' },
]

/** Category label for an event, matching the Type filter's grouping.
 *  Unknown prefixes fall back to the raw prefix so future backend categories
 *  render something sensible before the FE catches up. */
function categoryOf(eventType: string): string {
  const dot = eventType.indexOf('.')
  if (dot <= 0) return eventType
  const prefix = eventType.slice(0, dot + 1)
  return TYPE_OPTIONS.find((o) => o.value === prefix)?.label ?? eventType.slice(0, dot)
}

/** Plain-text form of a group, for the collapsed row's hover title. */
function changeGroupText(g: ChangeGroup): string {
  const entries = g.entries
    .map((e) => `${e.field ? `${e.field} ` : ''}${formatChangeValue(e.from)} → ${formatChangeValue(e.to)}`)
    .join(', ')
  return `${g.label}: ${entries}`
}

function ChangesCell({
  event,
  expanded,
  onToggleExpanded,
}: {
  event: ProjectEvent
  expanded: boolean
  onToggleExpanded: () => void
}) {
  // Assignee ids in workflow-step payloads resolve against the members list.
  // Cached by react-query (the tab's user filter already fetches it), so this
  // per-row hook costs a cache subscription, not a request.
  const { data: members } = useQuery({ queryKey: queryKeys.members.all, queryFn: listMembers })
  const flat = changesOf(event)
  const groups = flat
    ? flatChangeGroups(flat)
    : event.event_type === 'project.workflow_steps_set'
      ? workflowStepsChangesOf(event, (id) => {
          const m = members?.find((m) => m.id === id)
          return m ? m.name || m.email : null
        })
      : null
  if (!groups || groups.length === 0) return <span className="text-gray-300 dark:text-gray-600">—</span>
  return (
    <div className="relative">
      <dl className="space-y-0.5" data-testid="event-changes">
        {groups.map((g, gi) => (
          <div
            key={gi}
            // Collapsed: one line per change, ellipsized (hover title shows the
            // full text). Expanded: the wide column, wrap freely.
            className={expanded ? 'text-xs' : 'overflow-hidden text-ellipsis whitespace-nowrap text-xs'}
            title={expanded ? undefined : changeGroupText(g)}
          >
            <dt className="inline font-medium text-gray-500 dark:text-gray-400">{g.label}: </dt>
            <dd className="inline text-gray-700 dark:text-gray-300">
              {g.entries.map((e, ei) => (
                <span key={ei}>
                  {ei > 0 && <span className="text-gray-400">, </span>}
                  {e.field && <span className="text-gray-500 dark:text-gray-400">{e.field} </span>}
                  <span className="line-through opacity-60">{formatChangeValue(e.from)}</span>
                  <span className="mx-1 text-gray-400">→</span>
                  <span>{formatChangeValue(e.to)}</span>
                </span>
              ))}
            </dd>
          </div>
        ))}
      </dl>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={(e) => {
          e.stopPropagation()
          onToggleExpanded()
        }}
        aria-pressed={expanded}
        aria-label={expanded ? 'Collapse changes column' : 'Expand changes column'}
        title={expanded ? 'Collapse changes column' : 'Expand changes column'}
        data-testid="history-changes-toggle"
        // Revealed on row hover (the row is a `group` via rowClassName) or on
        // keyboard focus — no reserved space, it floats over the cell corner.
        // Solid bg (matching the row's hover bg) keeps it legible where it
        // overlaps the ellipsized first line.
        className="absolute -right-1 -top-1 opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 bg-gray-50 dark:bg-gray-800 text-gray-400 hover:text-gray-700 dark:text-gray-500 dark:hover:text-gray-200"
      >
        {expanded ? <ChevronsRightLeft className="h-3 w-3" /> : <ChevronsLeftRight className="h-3 w-3" />}
      </Button>
    </div>
  )
}

// Column order: Type | Action | Changes | User | Date — what happened comes
// first, who and when trail as metadata.
//
// Two width presets: the default layout, and a changes-focused one (toggled
// from any Changes cell) that shrinks everything else so a long diff reads in
// full. table-fixed stretches both proportionally on wider windows.
function buildColumns(changesExpanded: boolean, onToggleChanges: () => void): ColumnDef<ProjectEvent>[] {
  // In the changes-focused layout the de-emphasized text cells clamp to one
  // ellipsized line each (full value on hover via title) instead of wrapping
  // into tall rows at their narrow widths.
  const clamp = changesExpanded ? 'truncate' : ''
  return [
    {
      id: 'type',
      header: 'Type',
      // Default: tightest width that keeps every category on one line — the
      // widest badge ("Workflow") measures 72px + the cell's 2×12px padding.
      // Unknown future categories truncate inside the badge instead of
      // overflowing; in the changes-focused layout every badge truncates.
      size: changesExpanded ? 64 : 98,
      cell: ({ row }) => (
        <Badge
          variant="outline"
          className="max-w-full truncate"
          title={categoryOf(row.original.event_type)}
          data-testid="event-type-badge"
        >
          {categoryOf(row.original.event_type)}
        </Badge>
      ),
    },
    {
      id: 'action',
      header: 'Action',
      size: changesExpanded ? 130 : 216,
      cell: ({ row }) => {
        const ev = row.original
        const sentence = renderEventSentence(ev)
        // Sentences are authored lowercase (they read as "<actor> <sentence>"
        // elsewhere); standing alone in a table cell they get sentence case via
        // ::first-letter — needs a block element to apply.
        return (
          <span className={`block break-words text-sm text-gray-700 dark:text-gray-300 first-letter:uppercase ${clamp}`}>
            {sentence ?? (
              // Forward-compatible fallback for event types this FE build
              // doesn't know yet: raw slug + pretty-printed details.
              <>
                <span className="font-mono text-xs">{ev.event_type}</span>{' '}
                <span className="text-xs text-gray-500">{JSON.stringify(ev.details)}</span>
              </>
            )}
          </span>
        )
      },
    },
    {
      id: 'changes',
      header: 'Changes',
      // Default: Action 216 / Changes 200 keeps the Action-slightly-wider
      // ratio; focused: Changes takes the lion's share.
      size: changesExpanded ? 480 : 200,
      cell: ({ row }) => (
        <ChangesCell event={row.original} expanded={changesExpanded} onToggleExpanded={onToggleChanges} />
      ),
    },
    {
      id: 'user',
      header: 'User',
      size: changesExpanded ? 110 : 170,
      cell: ({ row }) => {
        const actor = row.original.actor
        // Joined user: full name with the email underneath. Otherwise fall back
        // to eventActorName (emit-time snapshot for deleted users, or "System"),
        // which is a single line — usually an email — so no second row.
        if (actor?.name && actor.email) {
          return (
            <div className="min-w-0">
              <span className={`block text-sm font-medium text-gray-900 dark:text-gray-100 ${clamp || 'break-words'}`} title={actor.name}>
                {actor.name}
              </span>
              <span className={`block text-xs text-gray-500 dark:text-gray-400 ${clamp || 'break-all'}`} title={actor.email}>
                {actor.email}
              </span>
            </div>
          )
        }
        return (
          <span className={`block text-sm font-medium text-gray-900 dark:text-gray-100 ${clamp || 'break-all'}`}>
            {eventActorName(row.original)}
          </span>
        )
      },
    },
    {
      id: 'time',
      header: 'Date',
      // Default: wide enough for "Aug 24, 2026, 11:43 AM" on one line at
      // text-xs; focused: truncates (full timestamp stays on the title).
      size: changesExpanded ? 100 : 165,
      cell: ({ row }) => {
        const iso = row.original.created_at
        const when = new Date(iso).toLocaleString(undefined, {
          year: 'numeric',
          month: 'short',
          day: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
        })
        return (
          <time className="block truncate text-xs text-gray-500 dark:text-gray-400" dateTime={iso} title={iso}>
            {when}
          </time>
        )
      },
    },
  ]
}

export function ProjectHistoryTab({ projectId }: Props) {
  const [params] = useSearchParams()
  const typeFilter = params.get('evtype') ?? ''
  const actorFilter = Number(params.get('evactor')) || 0

  // Changes-focused layout (toggled from any Changes cell): the Changes
  // column widens, everything else clamps to one ellipsized line.
  const [changesExpanded, setChangesExpanded] = useState(false)
  const columns = useMemo(
    () => buildColumns(changesExpanded, () => setChangesExpanded((v) => !v)),
    [changesExpanded],
  )

  const { setParam, clearParams } = useUrlParamSetters()

  // Members feed the user filter. The tab is manager-gated (same as
  // /api/members), so this never 403s for someone who can see it.
  const { data: members } = useQuery({ queryKey: queryKeys.members.all, queryFn: listMembers })

  const {
    data,
    error,
    isLoading,
    refetch,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: queryKeys.projectEvents.byProjectFiltered(projectId, typeFilter, actorFilter),
    queryFn: ({ pageParam = 0 }) =>
      listProjectEvents(projectId, pageParam, { type: typeFilter || undefined, actorId: actorFilter || undefined }),
    initialPageParam: 0,
    getNextPageParam: (lastPage) => (lastPage.has_more ? lastPage.cursor : undefined),
    staleTime: 0,
    refetchOnMount: 'always',
    // Keep the current rows on screen while a filter change refetches —
    // without this the table unmounts into the loading placeholder on every
    // chip click (testing.md filter-blank pattern; same as ProjectList).
    placeholderData: keepPreviousData,
  })

  const events = useMemo(() => data?.pages.flatMap((p) => p.data) ?? [], [data])

  const typeLabel = TYPE_OPTIONS.find((o) => o.value === typeFilter)?.label ?? null
  const actorMember = members?.find((m) => m.id === actorFilter)
  const actorLabel = actorMember ? actorMember.name || actorMember.email : actorFilter ? `#${actorFilter}` : null
  const hasFilters = Boolean(typeFilter || actorFilter)

  return (
    <div data-testid="project-history-tab">
      <div className="mb-3 flex flex-wrap items-center gap-2" data-guide="project_history.filters">
        <FilterChip
          label="Type"
          value={typeLabel}
          testId="history-filter-type"
          render={(close) => (
            <>
              <FilterOption selected={!typeFilter} onClick={() => { setParam('evtype', null); close() }}>
                All types
              </FilterOption>
              {TYPE_OPTIONS.map((o) => (
                <FilterOption
                  key={o.value}
                  selected={typeFilter === o.value}
                  onClick={() => { setParam('evtype', o.value); close() }}
                >
                  {o.label}
                </FilterOption>
              ))}
            </>
          )}
        />
        <FilterChip
          label="User"
          value={actorLabel}
          testId="history-filter-user"
          render={(close) => (
            <>
              <FilterOption selected={!actorFilter} onClick={() => { setParam('evactor', null); close() }}>
                Anyone
              </FilterOption>
              {(members ?? []).map((m) => (
                <FilterOption
                  key={m.id}
                  selected={actorFilter === m.id}
                  onClick={() => { setParam('evactor', String(m.id)); close() }}
                >
                  {m.name || m.email}
                </FilterOption>
              ))}
            </>
          )}
        />
        {/* One update for both keys: setParam twice in a row would start both
            writes from the same URL, and the second would re-add the key the
            first removed. */}
        <ClearFiltersButton
          count={(typeFilter ? 1 : 0) + (actorFilter ? 1 : 0)}
          onClear={() => clearParams(['evtype', 'evactor'])}
          testId="history-filter-clear"
        />
      </div>

      {isLoading ? (
        <LoadingPlaceholder />
      ) : error ? (
        <LoadFailed what="history" error={error} onRetry={() => refetch()} />
      ) : (
        <div data-testid="project-history-list" data-guide="project_history.list">
          <DataTable
            columns={columns}
            data={events}
            emptyMessage={
              hasFilters
                ? 'No history matches these filters.'
                : 'No history yet — project actions will show up here.'
            }
            rowClassName="group align-middle"
          />
          <LoadMoreButton
            size="sm"
            hasNextPage={hasNextPage}
            isFetchingNextPage={isFetchingNextPage}
            onLoadMore={() => fetchNextPage()}
            testId="project-history-load-more"
          />
        </div>
      )}
    </div>
  )
}
