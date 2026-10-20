import { useState, type ComponentProps } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Chip, type ChipColor } from '@/components/ui/chip'
import { Button } from '@/components/ui/button'
import { getMe, type Issue, type IssuePriority, type IssueRef, type IssueScope, type IssueSeverity, type IssueStatus, type IssueVerification } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import {
  ISSUE_AREA_LABELS, ISSUE_CONTEXT_LABELS, ISSUE_KIND_LABELS, ISSUE_PRIORITY, ISSUE_SEVERITY, ISSUE_STATUS,
  ISSUE_VERIFICATION, issueListPath, issueWaitingOn, waitingOnLabel,
} from '@/lib/issue-labels'
import { timeAgo } from '@/lib/time-ago'
import { TextLink } from '@/components/ui/text-link'
import { DotSeparator } from '@/components/ui/dot-separator'
import { IssueText } from './IssueText'
import { IssueTextField } from './IssueTextField'

/** A relative time, with the exact one on hover. */
export function When({ at }: { at: string }) {
  return <time dateTime={at} title={new Date(at).toLocaleString()}>{timeAgo(at)}</time>
}

type ChipSize = ComponentProps<typeof Chip>['size']

/** An entry of one of the issue label maps, as a chip. */
function IssueLabelChip({ entry, size, testId }: {
  entry: { label: string; color: ChipColor }
  size?: ChipSize
  testId: string
}) {
  return <Chip color={entry.color} size={size} className="shrink-0" data-testid={testId}>{entry.label}</Chip>
}

export function IssueStatusChip({ status, size, testId = 'issue-status' }: { status: IssueStatus; size?: ChipSize; testId?: string }) {
  return <IssueLabelChip entry={ISSUE_STATUS[status]} size={size} testId={testId} />
}

export function IssueSeverityChip({ severity, size }: { severity: IssueSeverity; size?: ChipSize }) {
  return <IssueLabelChip entry={ISSUE_SEVERITY[severity]} size={size} testId="issue-severity" />
}

export function IssuePriorityChip({ priority, size }: { priority: IssuePriority; size?: ChipSize }) {
  return <IssueLabelChip entry={ISSUE_PRIORITY[priority]} size={size} testId="issue-priority" />
}

/** The assistant's check of a report it wrote. The support team's alone. */
export function IssueVerificationChip({ verification, size }: { verification: IssueVerification; size?: ChipSize }) {
  return <IssueLabelChip entry={ISSUE_VERIFICATION[verification]} size={size} testId="issue-verification" />
}

/**
 * Who has to act next, for an issue that still needs work: yellow when it is
 * the viewer's side, grey when it is the other side.
 */
export function IssueWaitingOnChip({ issue, scope, size }: {
  issue: Pick<Issue, 'status' | 'last_activity_by'>
  scope: IssueScope
  size?: ChipSize
}) {
  const on = issueWaitingOn(issue)
  if (!on) return null
  const viewersTurn = (on === 'support') === (scope === 'platform')
  return (
    <Chip color={viewersTurn ? 'yellow' : 'grey'} size={size} className="shrink-0" data-testid="issue-waiting-on">
      {waitingOnLabel(on, scope)}
    </Chip>
  )
}

/**
 * Another issue: a link with its number and title, or its number alone when
 * the viewer may not open it; then its status. `to` overrides the link's
 * target.
 */
function IssueRefLink({ issue, scope, to, testId }: { issue: IssueRef; scope: IssueScope; to?: string; testId: string }) {
  const href = to ?? (issue.can_open ? `${issueListPath(scope)}/${issue.id}` : undefined)
  const label = issue.title ? `#${issue.id} ${issue.title}` : `#${issue.id}`
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {href ? <TextLink to={href} data-testid={testId}>{label}</TextLink> : <span data-testid={testId}>{label}</span>}
      <IssueStatusChip status={issue.status} size="small" testId={`${testId}-status`} />
    </span>
  )
}

/**
 * Where the support team tracks a duplicate. A member sees the original's
 * title and a link only when they may open it; a support team member reading
 * their own org's issue gets the support team's page of the original.
 */
function DuplicateNotice({ issue, scope }: { issue: Issue; scope: IssueScope }) {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const original = issue.duplicate_of
  if (!original) {
    return <p className="text-sm" data-testid="issue-duplicate-notice">The original issue no longer exists.</p>
  }
  const support = scope === 'mine' && !original.can_open && me?.is_platform_admin
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-sm" data-testid="issue-duplicate-notice">
      The support team tracks this in
      <IssueRefLink issue={original} scope={scope} to={support ? `${issueListPath('platform')}/${original.id}` : undefined} testId="issue-duplicate-link" />
    </p>
  )
}

/** The support team's view of an original: the issues marked as its duplicates. */
function DuplicatesList({ duplicates }: { duplicates: IssueRef[] }) {
  return (
    <div className="space-y-1 text-sm" data-testid="issue-duplicates">
      <p className="text-muted-foreground">Duplicates of this issue</p>
      <ul className="space-y-1">
        {duplicates.map(d => <li key={d.id}><IssueRefLink issue={d} scope="platform" testId="issue-duplicates-link" /></li>)}
      </ul>
    </div>
  )
}

/** The report as filed: what happened, where, and its files. */
export function IssueSummary({ issue, scope }: { issue: Issue; scope: IssueScope }) {
  const context = Object.entries(issue.context ?? {}).filter(([, v]) => v !== undefined && v !== '')
  if (issue.import_key) context.push(['imported_from', issue.import_key])
  const waiting = issueWaitingOn(issue)
  return (
    <section className="space-y-4" data-testid="issue-summary">
      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <IssueStatusChip status={issue.status} />
        {issue.confirmed_at && <Chip color="green" data-testid="issue-confirmed">Fix confirmed</Chip>}
        <span data-testid="issue-kind">{ISSUE_KIND_LABELS[issue.kind]}</span>
        {issue.severity && <IssueSeverityChip severity={issue.severity} />}
        <DotSeparator className="mx-0" />
        <span data-testid="issue-area">{ISSUE_AREA_LABELS[issue.area]}</span>
        <DotSeparator className="mx-0" />
        <span>Reported <When at={issue.created_at} /></span>
        <DotSeparator className="mx-0" />
        <span data-testid="issue-reported-by">
          by {issue.org_name && `${issue.org_name} · `}
          {issue.reporter_name ? `${issue.reporter_name} (${issue.reporter_email})` : issue.reporter_email}
        </span>
      </div>
      {(waiting || issue.assignee) && (
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          {waiting && <span className="flex items-center gap-1">Waiting on <IssueWaitingOnChip issue={issue} scope={scope} /></span>}
          {waiting && issue.assignee && <DotSeparator className="mx-0" />}
          {issue.assignee && <span data-testid="issue-assignee">Assigned to {issue.assignee.name || 'the support team'}</span>}
        </div>
      )}
      {issue.chat_verification && (
        <p className="flex flex-wrap items-center gap-2 text-sm" data-testid="issue-check">
          <span className="text-muted-foreground">Assistant check</span>
          <IssueVerificationChip verification={issue.chat_verification} />
          {issue.chat_verification_note && <span data-testid="issue-check-note">{issue.chat_verification_note}</span>}
        </p>
      )}
      {issue.status === 'duplicate' && <DuplicateNotice issue={issue} scope={scope} />}
      {issue.duplicates && issue.duplicates.length > 0 && <DuplicatesList duplicates={issue.duplicates} />}
      <IssueText text={issue.description} files={issue.attachments} scope={scope} issueId={issue.id} testId="issue-description" />
      {context.length > 0 && (
        <dl className="grid max-w-xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-md bg-muted px-3 py-2 text-xs" data-testid="issue-context">
          {context.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{ISSUE_CONTEXT_LABELS[k] ?? k}</dt>
              <dd className="truncate font-mono">{String(v)}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  )
}

/** A comment box with optional files. Clears itself once sent. */
export function IssueCommentBox({ onSend, sending, placeholder = 'Write a reply' }: {
  onSend: (body: string, files: File[]) => Promise<unknown>
  sending: boolean
  placeholder?: string
}) {
  const [body, setBody] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const empty = !body.trim() && files.length === 0
  const send = () => {
    onSend(body, files).then(() => { setBody(''); setFiles([]) }, () => {})
  }
  return (
    <div className="space-y-2" data-testid="issue-comment-box">
      <IssueTextField
        value={body}
        onChange={setBody}
        files={files}
        onFilesChange={setFiles}
        disabled={sending}
        rows={3}
        placeholder={placeholder}
        data-testid="issue-comment-input"
        filesTestId="issue-comment-files"
      />
      <div className="flex justify-end">
        <Button onClick={send} loading={sending} disabledReason={empty ? 'Write a reply or attach a file.' : undefined}
          disabled={empty} data-testid="issue-comment-send">
          Send
        </Button>
      </div>
    </div>
  )
}
