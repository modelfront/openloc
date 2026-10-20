import { useCallback, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Plus, Search } from 'lucide-react'
import {
  listIssueAssignees, listIssueOrgs, listIssueTags,
  type IssueArea, type IssueFilter, type IssueKind, type IssuePriority, type IssueScope, type IssueSeverity, type IssueStatus,
  type IssueVerification,
} from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { isManagerRole } from '@/lib/roles'
import {
  ACTIVE_ISSUE_STATUSES, ISSUE_AREA_OPTIONS, ISSUE_KIND_OPTIONS, ISSUE_PRIORITY_OPTIONS, ISSUE_SEVERITY_OPTIONS,
  ISSUE_STATUS_OPTIONS, ISSUE_VERIFICATION_OPTIONS,
} from '@/lib/issue-labels'
import { useDebouncedUrlParam } from '@/hooks/useDebouncedUrlParam'
import { useUrlParamSetters } from '@/hooks/useUrlParamSetters'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { MultiCombobox } from '@/components/ui/combobox'
import { ClearFiltersButton } from '@/components/list/ClearFiltersButton'
import { IssueList } from '@/components/issues/IssueList'
import { ReportIssueDialog } from '@/components/issues/ReportIssueDialog'

/** The URL keys of the filters, as the API names them. */
type FilterKey = 'status' | 'kind' | 'severity' | 'area' | 'org' | 'priority' | 'tag' | 'assignee' | 'verification'

/** The URL key of the search text. */
const SEARCH_KEY = 'q'

/**
 * Reported issues. `mine`: the org's issues the member may see — a manager's
 * every member's, anyone else's their own. `platform`: every org's, for
 * platform operators, with their own filters (organization, priority, tag,
 * assignee, the assistant's check). Both search the title and the
 * description. Shows the issues that still need work, most recently updated
 * first, by default; the search, the filters and the order live in the URL.
 */
export default function IssuesPage({ scope, role }: { scope: IssueScope; role: string }) {
  const platform = scope === 'platform'
  const [reporting, setReporting] = useState(false)
  const [params] = useSearchParams()
  const [search, setSearch] = useDebouncedUrlParam(SEARCH_KEY)
  const { setParam, clearParams } = useUrlParamSetters()
  const { data: orgs = [] } = useQuery({ queryKey: queryKeys.platform.issueOrgs, queryFn: listIssueOrgs, enabled: platform })
  const { data: team = [] } = useQuery({ queryKey: queryKeys.platform.issueAssignees, queryFn: listIssueAssignees, enabled: platform })
  const { data: tags = [] } = useQuery({ queryKey: queryKeys.platform.issueTags, queryFn: listIssueTags, enabled: platform })

  const filter: IssueFilter = useMemo(() => {
    const read = (key: FilterKey) => params.get(key)?.split(',').filter(Boolean) ?? []
    return {
      statuses: (params.get('status') === null ? ACTIVE_ISSUE_STATUSES : read('status')) as IssueStatus[],
      kinds: read('kind') as IssueKind[],
      severities: read('severity') as IssueSeverity[],
      areas: read('area') as IssueArea[],
      q: params.get(SEARCH_KEY) ?? '',
      orgs: platform ? read('org').map(Number) : [],
      priorities: platform ? read('priority') as IssuePriority[] : [],
      tags: platform ? read('tag') : [],
      assignees: platform ? read('assignee') : [],
      verifications: platform ? read('verification') as IssueVerification[] : [],
      oldestFirst: params.get('sort') === 'updated',
    }
  }, [params, platform])
  // An empty status list stays in the URL as "any status": an absent status
  // is the default (still needing work).
  const set = (key: FilterKey) => (next: string[]) =>
    setParam(key, next.join(','), { keepEmpty: key === 'status' })
  const filters: { key: FilterKey; name: string; value: string[]; options: { value: string; label: string }[] }[] = [
    ...(platform ? [{
      key: 'org' as const, name: 'Organization', value: (filter.orgs ?? []).map(String),
      options: orgs.map(o => ({ value: String(o.id), label: o.name })),
    }] : []),
    { key: 'status', name: 'Status', value: filter.statuses ?? [], options: ISSUE_STATUS_OPTIONS },
    { key: 'kind', name: 'Kind', value: filter.kinds ?? [], options: ISSUE_KIND_OPTIONS },
    { key: 'severity', name: 'Severity', value: filter.severities ?? [], options: ISSUE_SEVERITY_OPTIONS },
    { key: 'area', name: 'Area', value: filter.areas ?? [], options: ISSUE_AREA_OPTIONS },
    ...(platform ? [
      { key: 'priority' as const, name: 'Priority', value: filter.priorities ?? [], options: ISSUE_PRIORITY_OPTIONS },
      { key: 'tag' as const, name: 'Tag', value: filter.tags ?? [], options: tags.map(t => ({ value: t, label: t })) },
      {
        key: 'assignee' as const, name: 'Assignee', value: filter.assignees ?? [],
        options: [{ value: 'none', label: 'Unassigned' }, ...team.map(p => ({ value: String(p.id), label: p.name || p.email }))],
      },
      {
        key: 'verification' as const, name: 'Assistant check', value: filter.verifications ?? [],
        options: ISSUE_VERIFICATION_OPTIONS,
      },
    ] : []),
  ]
  // A filter is set when its key is in the URL: an absent status means the
  // default (still needing work). Clearing keeps the sort order.
  const activeFilters = filters.filter(f => params.has(f.key)).length + (params.has(SEARCH_KEY) ? 1 : 0)
  const clearFilters = () => clearParams([...filters.map(f => f.key), SEARCH_KEY])
  const managers = isManagerRole(role)
  const { oldestFirst } = filter
  const toggleSort = useCallback(() => setParam('sort', oldestFirst ? null : 'updated'), [setParam, oldestFirst])

  return (
    // The support team's queue carries more columns, so it takes the full width.
    <PageContainer width={platform ? 'wide' : 'default'}>
      <PageHeader
        title="Reported issues"
        subtitle={platform ? 'Bugs, enhancements and feature requests from users of every organization.'
          : managers ? 'Bugs, enhancements and feature requests your organization sent to the support team.'
            : 'Bugs, enhancements and feature requests you sent to the support team.'}
        // Operators report too: into their own org, like from the account menu.
        actions={<Button onClick={() => setReporting(true)} data-testid="issues-report"><Plus />Report an issue</Button>}
      />
      <div className="mb-4 flex items-start gap-2">
        <div className="grid flex-1 grid-cols-2 gap-2 lg:grid-cols-4">
          <Input value={search} onChange={e => setSearch(e.target.value)} icon={<Search />}
            placeholder="Search titles and descriptions" aria-label="Search issues" maxLength={200}
            className="col-span-2 lg:col-span-4" data-testid="issues-search" />
          {filters.map(f => (
            <MultiCombobox key={f.key} value={f.value} onChange={set(f.key)} options={f.options}
              searchable={['area', 'org', 'tag', 'assignee'].includes(f.key)} placeholder={`Any ${f.name.toLowerCase()}`}
              aria-label={f.name} data-testid={`issues-filter-${f.key}`} />
          ))}
        </div>
        <ClearFiltersButton count={activeFilters} onClear={clearFilters} testId="issues-clear-filters" />
      </div>
      <IssueList scope={scope} filter={filter} showReporter={platform || managers} onToggleSort={toggleSort}
        emptyMessage={filter.q ? 'No issues match this search.' : 'No issues match these filters.'} />
      <ReportIssueDialog open={reporting} onClose={() => setReporting(false)} />
    </PageContainer>
  )
}
