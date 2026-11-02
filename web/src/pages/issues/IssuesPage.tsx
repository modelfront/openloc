import { useCallback, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Search } from 'lucide-react'
import {
  listIssueAssignees, listIssueOrgs, listIssueReporters, listIssueTags,
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
import { readStorage, writeStorage } from '@/lib/safe-storage'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { CreateButton } from '@/components/ui/create-button'
import { Input } from '@/components/ui/input'
import { MultiCombobox, type ComboboxOption } from '@/components/ui/combobox'
import { ClearFiltersButton } from '@/components/list/ClearFiltersButton'
import { IssueCount } from '@/components/issues/IssueCount'
import { IssueList } from '@/components/issues/IssueList'
import { ReportIssueDialog } from '@/components/issues/ReportIssueDialog'
import { formatSort, parseSort, sortKeysFor, type IssueSortLevel } from '@/lib/issue-sort'

/** The URL keys of the filters, as the API names them. */
type FilterKey = 'status' | 'kind' | 'severity' | 'area' | 'org' | 'reporter' | 'priority' | 'tag' | 'assignee' | 'verification'

/**
 * One filter dropdown. Most own the URL key named by key; the reporter
 * dropdown owns params (org and reporter) and splits its picks between them.
 */
interface FilterField {
  key: FilterKey
  name: string
  value: string[]
  options: ComboboxOption[]
  params?: readonly FilterKey[]
  onChange?: (next: string[]) => void
}

/** Where each list remembers the order last chosen on it. */
const sortStorageKey = (scope: IssueScope) => `issues.sort.${scope}`

// The reporter dropdown's values: an org or a person, by id.
const ORG_PICK = 'org:'
const PERSON_PICK = 'user:'
const picked = (values: string[], prefix: string) =>
  values.filter(v => v.startsWith(prefix)).map(v => v.slice(prefix.length)).join(',')

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
  const { setParam, setParamValues, clearParams } = useUrlParamSetters()
  const { data: orgs = [] } = useQuery({ queryKey: queryKeys.platform.issueOrgs, queryFn: listIssueOrgs, enabled: platform })
  const { data: reporters = [] } = useQuery({ queryKey: queryKeys.platform.issueReporters, queryFn: listIssueReporters, enabled: platform })
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
      reporters: platform ? read('reporter').map(Number) : [],
      priorities: platform ? read('priority') as IssuePriority[] : [],
      tags: platform ? read('tag') : [],
      assignees: platform ? read('assignee') : [],
      verifications: platform ? read('verification') as IssueVerification[] : [],
      // A link's order wins; else the order last chosen on this list.
      sort: parseSort(params.get('sort') ?? readStorage(sortStorageKey(scope)), sortKeysFor(scope)),
    }
  }, [params, platform, scope])
  // An empty status list stays in the URL as "any status": an absent status
  // is the default (still needing work).
  const set = (key: FilterKey) => (next: string[]) =>
    setParam(key, next.join(','), { keepEmpty: key === 'status' })
  const filters: FilterField[] = [
    // The support team picks orgs, then people: an issue of a picked org or
    // by a picked person matches.
    ...(platform ? [{
      key: 'reporter' as const, name: 'Reporter', params: ['org', 'reporter'] as const,
      value: [...(filter.orgs ?? []).map(id => ORG_PICK + id), ...(filter.reporters ?? []).map(id => PERSON_PICK + id)],
      options: [
        ...orgs.map(o => ({ value: ORG_PICK + o.id, label: o.name, group: 'orgs' })),
        ...reporters.map(p => ({
          value: PERSON_PICK + p.id, label: p.name || p.email, chip: p.orgs, group: 'people',
          keywords: `${p.email} ${p.orgs}`,
        })),
      ],
      onChange: (next: string[]) => setParamValues({ org: picked(next, ORG_PICK), reporter: picked(next, PERSON_PICK) }),
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
  const keysOf = (f: FilterField) => f.params ?? [f.key]
  const activeFilters = filters.filter(f => keysOf(f).some(k => params.has(k))).length + (params.has(SEARCH_KEY) ? 1 : 0)
  const clearFilters = () => clearParams([...filters.flatMap(keysOf), SEARCH_KEY])
  const managers = isManagerRole(role)
  const onSortChange = useCallback((sort: IssueSortLevel[]) => {
    const value = formatSort(sort)
    // '' remembers the default order, so a click back to it sticks too.
    writeStorage(sortStorageKey(scope), value ?? '')
    setParam('sort', value)
  }, [setParam, scope])

  return (
    // The support team's queue carries more columns, so it takes the full width.
    <PageContainer width={platform ? 'wide' : 'default'}>
      <PageHeader
        title="Reported issues"
        subtitle={platform ? 'Bugs, enhancements and feature requests from users of every organization.'
          : managers ? 'Bugs, enhancements and feature requests your organization sent to the support team.'
            : 'Bugs, enhancements and feature requests you sent to the support team.'}
        // Operators report too: into their own org, like from the account menu.
        actions={<CreateButton onClick={() => setReporting(true)} data-testid="issues-report">Report an issue</CreateButton>}
      />
      <div className="mb-3 grid grid-cols-2 gap-2 lg:grid-cols-4">
        <Input value={search} onChange={e => setSearch(e.target.value)} icon={<Search />}
          placeholder="Search titles and descriptions" aria-label="Search issues" maxLength={200}
          className="col-span-2 lg:col-span-4" data-testid="issues-search" />
        {filters.map(f => (
          <MultiCombobox key={f.key} value={f.value} onChange={f.onChange ?? set(f.key)} options={f.options}
            searchable={['area', 'reporter', 'tag', 'assignee'].includes(f.key)} placeholder={`Any ${f.name.toLowerCase()}`}
            aria-label={f.name} data-testid={`issues-filter-${f.key}`} />
        ))}
      </div>
      <div className="mb-2 flex min-h-6 items-center gap-3">
        <IssueCount scope={scope} filter={filter} />
        <ClearFiltersButton count={activeFilters} onClear={clearFilters} testId="issues-clear-filters" />
      </div>
      <IssueList scope={scope} filter={filter} showReporter={platform || managers} onSortChange={onSortChange}
        emptyMessage={filter.q ? 'No issues match this search.' : 'No issues match these filters.'} />
      <ReportIssueDialog open={reporting} onClose={() => setReporting(false)} />
    </PageContainer>
  )
}
