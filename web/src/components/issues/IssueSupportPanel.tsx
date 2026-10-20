import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import {
  ApiError, getIssue, listIssueAssignees, listIssues, listIssueTags, setIssueStatus, updateIssue,
  type Issue, type IssueArea, type IssueDetail, type IssuePriority, type IssueSeverity, type IssueStatus, type IssueUpdate,
} from '@/api'
import { queryKeys } from '@/lib/query-keys'
import {
  ISSUE_AREA_OPTIONS, ISSUE_PRIORITY_OPTIONS, ISSUE_SEVERITY_OPTIONS, ISSUE_STATUS_OPTIONS,
} from '@/lib/issue-labels'
import { Button } from '@/components/ui/button'
import { Combobox, MultiCombobox, type ComboboxOption } from '@/components/ui/combobox'
import { Textarea } from '@/components/ui/textarea'
import { IssueKindSelect } from './IssueKindForm'
import { FormField } from '@/components/ui/form-field'
import { SaveStatus } from '@/components/ui/save-status'
import { useSavedFlash } from '@/hooks/useSavedFlash'
import { useDebounce } from '@/hooks/useDebounce'
import { IssueStatusChip } from '@/components/issues/IssueParts'

/**
 * The support team's fields: assignee, priority, kind, severity (bugs only),
 * area and tags. Each change saves at once, confirms with "Saved ✓" and shows
 * up in the history; the reporter is not notified, and only the assignee
 * reaches the reporter's org. A new kind gets its severity from the server.
 */
export function IssueFieldsForm({ issue, onUpdated }: { issue: Issue; onUpdated: (d: IssueDetail) => void }) {
  const { data: team = [] } = useQuery({ queryKey: queryKeys.platform.issueAssignees, queryFn: listIssueAssignees })
  const { data: tagsInUse = [] } = useQuery({ queryKey: queryKeys.platform.issueTags, queryFn: listIssueTags })
  const [saved, flashSaved] = useSavedFlash()
  const mut = useMutationWithToast({
    mutationFn: (u: IssueUpdate) => updateIssue(issue.id, u),
    successMessage: () => null,
    invalidateKeys: [queryKeys.platform.issueTags],
    onSuccess: d => { onUpdated(d); flashSaved() },
  })
  const tags = useMemo(() => issue.tags ?? [], [issue.tags])
  const assigneeOptions = useMemo(
    () => team.map(p => ({ value: String(p.id), label: p.name || p.email, keywords: p.email })), [team])
  const tagOptions = useMemo(
    () => [...new Set([...tagsInUse, ...tags])].sort().map(t => ({ value: t, label: t })), [tagsInUse, tags])
  return (
    <section className="space-y-3" data-testid="issue-fields-form">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-medium">Triage</h2>
        <SaveStatus saving={mut.isPending} saved={saved} testId="issue-fields-save-status" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <FormField label="Assignee">
          <Combobox value={issue.assignee_id ? String(issue.assignee_id) : ''} options={assigneeOptions} clearable
            placeholder="Unassigned" disabled={mut.isPending} className="w-full" aria-label="Assignee" data-testid="issue-assignee-select"
            onChange={v => mut.mutate({ assignee_id: v ? Number(v) : null })} />
        </FormField>
        <FormField label="Priority">
          <Combobox value={issue.priority ?? ''} options={ISSUE_PRIORITY_OPTIONS} searchable={false} clearable
            placeholder="No priority" disabled={mut.isPending} className="w-full" aria-label="Priority" data-testid="issue-priority-select"
            onChange={v => mut.mutate({ priority: (v || null) as IssuePriority | null })} />
        </FormField>
        <FormField label="Kind">
          <IssueKindSelect value={issue.kind} disabled={mut.isPending} onChange={kind => mut.mutate({ kind })} />
        </FormField>
        {issue.severity && (
          <FormField label="Severity">
            <Combobox value={issue.severity} options={ISSUE_SEVERITY_OPTIONS} searchable={false}
              disabled={mut.isPending} className="w-full" aria-label="Severity" data-testid="issue-severity-select"
              onChange={v => mut.mutate({ severity: v as IssueSeverity })} />
          </FormField>
        )}
        <FormField label="Area">
          <Combobox value={issue.area} options={ISSUE_AREA_OPTIONS}
            disabled={mut.isPending} className="w-full" aria-label="Area" data-testid="issue-area-select"
            onChange={v => mut.mutate({ area: v as IssueArea })} />
        </FormField>
        <FormField label="Tags" className="sm:col-span-2">
          <MultiCombobox value={tags} options={tagOptions} wrap placeholder="No tags" searchPlaceholder="Search or add a tag"
            onCreateOption={t => mut.mutate({ tags: [...tags, t] })} createOptionLabel={'Add tag "{}"'}
            disabled={mut.isPending} aria-label="Tags" data-testid="issue-tags-select"
            onChange={v => mut.mutate({ tags: v })} />
        </FormField>
      </div>
    </section>
  )
}

// An original is any other issue that is not a duplicate itself.
const ORIGINAL_STATUSES = ISSUE_STATUS_OPTIONS.map(o => o.value as IssueStatus).filter(s => s !== 'duplicate')

/**
 * The issues a duplicate may point at, as picker options: those whose title
 * or description contains `search`, plus the issue numbered `search` when it
 * is a number. The picked one stays first, whatever the search.
 */
const SEARCH_FAILED = 'The search failed. Type again to retry.'

function useOriginalIssueOptions(issueId: number, search: string, picked: Issue | null, enabled: boolean) {
  const q = useDebounce(search.trim(), 250)
  const number = /^#?\d+$/.test(q) ? Number(q.replace('#', '')) : 0
  const matches = useQuery({
    queryKey: [...queryKeys.platform.issues, 'original-search', q],
    queryFn: () => listIssues('platform', '', { q, statuses: ORIGINAL_STATUSES }),
    enabled,
  })
  const byNumber = useQuery({
    queryKey: queryKeys.platform.issue(number),
    queryFn: () => getIssue('platform', number),
    enabled: enabled && number > 0 && number !== issueId,
    retry: false,
  })
  const issues = useMemo(() => {
    const found = [picked, byNumber.data?.issue, ...(matches.data?.data ?? [])]
    const seen = new Set<number>()
    return found.filter((i): i is Issue => !!i && i.id !== issueId && i.status !== 'duplicate' && !seen.has(i.id) && !!seen.add(i.id))
  }, [picked, byNumber.data, matches.data, issueId])
  const options = useMemo<ComboboxOption[]>(() => issues.map(i => ({
    value: String(i.id), label: `#${i.id} ${i.title}`, chip: <IssueStatusChip status={i.status} size="small" />,
  })), [issues])
  // An unknown number is a 404: nothing matches, nothing failed.
  const numberFailed = byNumber.isError && !(byNumber.error instanceof ApiError && byNumber.error.status === 404)
  return { issues, options, failed: matches.isError || numberFailed, searching: q !== search.trim() || matches.isFetching || byNumber.isFetching }
}

/**
 * The support team changes the status, with an optional comment the reporter
 * sees; a duplicate names its original, found by its title or number. Keyed
 * on the issue's status, so a new status starts the form afresh.
 */
export function IssueStatusForm({ issueId, current, onUpdated }: {
  issueId: number
  current: IssueStatus
  onUpdated: (d: IssueDetail) => void
}) {
  const [status, setStatus] = useState<IssueStatus>(current)
  const [note, setNote] = useState('')
  const [original, setOriginal] = useState<Issue | null>(null)
  const [search, setSearch] = useState('')
  const duplicate = status === 'duplicate'
  const originals = useOriginalIssueOptions(issueId, search, original, duplicate)
  const mut = useMutationWithToast({
    mutationFn: () => setIssueStatus(issueId, status, note, duplicate ? original?.id : undefined),
    successMessage: 'The reporter has been notified.',
    onSuccess: d => { onUpdated(d); setNote('') },
  })
  const reason = duplicate && !original ? 'Pick the original issue.'
    : !duplicate && status === current && !note.trim() ? 'Pick another status or write a comment.'
      : status === 'needs_info' && status !== current && !note.trim() ? 'Write the question the reporter should answer.'
        : undefined
  return (
    <section className="space-y-3 rounded-md border border-border bg-card p-3" data-testid="issue-status-form">
      <div className="flex flex-wrap gap-4">
        <FormField label="Status">
          <Combobox value={status} onChange={v => setStatus(v as IssueStatus)} options={ISSUE_STATUS_OPTIONS}
            searchable={false} className="w-56" aria-label="Status" data-testid="issue-status-select" />
        </FormField>
        {duplicate && (
          <FormField label="Original issue" className="min-w-0 flex-1" error={original && originals.failed ? SEARCH_FAILED : undefined}>
            <Combobox value={original ? String(original.id) : ''} options={originals.options} onSearchChange={setSearch}
              onChange={v => setOriginal(originals.issues.find(i => String(i.id) === v) ?? null)}
              placeholder="Pick the original issue" searchPlaceholder="Search by title or number"
              emptyText={originals.searching ? 'Searching…' : originals.failed ? SEARCH_FAILED : 'No matching issues'}
              className="w-full max-w-xl" aria-label="Original issue" data-testid="issue-duplicate-of" />
            {/* The pick stays in the list, so the list is never empty once one is made:
                the empty and failed states need a line of their own. */}
            {original && !originals.failed && !originals.searching && originals.issues.length === 1 && (
              <p className="text-xs text-muted-foreground">No other matching issues</p>
            )}
          </FormField>
        )}
      </div>
      <FormField label={status === 'needs_info' ? 'Question for the reporter' : 'Comment (optional)'}>
        <Textarea value={note} onChange={e => setNote(e.target.value)} rows={2} data-testid="issue-status-note" />
      </FormField>
      <Button onClick={() => mut.mutate()} loading={mut.isPending} disabled={!!reason} disabledReason={reason}
        data-testid="issue-status-save">
        Update status
      </Button>
    </section>
  )
}
