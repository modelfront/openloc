import type { ReactNode } from 'react'
import type { IssueArea, IssueChange, IssueComment, IssueKind, IssuePriority, IssueScope, IssueSeverity } from '@/api'
import { Chip } from '@/components/ui/chip'
import { TextLink } from '@/components/ui/text-link'
import { ISSUE_AREA_LABELS, ISSUE_KIND_LABELS, ISSUE_PRIORITY, ISSUE_SEVERITY } from '@/lib/issue-labels'
import { IssueText } from './IssueText'
import { IssuePriorityChip, IssueSeverityChip, IssueStatusChip, When } from './IssueParts'

/**
 * The conversation: comments, status changes and other changes, oldest first.
 * The viewer's own entries read "You". The support team also sees their
 * internal notes and changes, marked Internal.
 */
export function IssueConversation({ comments, scope, issueId, meId }: {
  comments: IssueComment[]
  scope: IssueScope
  issueId: number
  meId: number | undefined
}) {
  if (comments.length === 0) return <p className="text-sm text-muted-foreground">No replies yet.</p>
  return (
    <ol className="space-y-3" data-testid="issue-conversation">
      {comments.map(c => (
        <li key={c.id} className="rounded-md border border-border bg-card px-3 py-2" data-testid="issue-comment"
          data-internal={c.internal ? 'true' : undefined}>
          <div className="mb-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground" data-testid="issue-comment-author">{authorLabel(c, meId)}</span>
            <When at={c.created_at} />
            {c.internal && <Chip size="small" data-testid="issue-comment-internal">Internal</Chip>}
            <EntryAction comment={c} scope={scope} />
          </div>
          <div className="space-y-2">
            <IssueText text={c.body} files={c.attachments} scope={scope} issueId={issueId} testId="issue-comment-body" />
          </div>
        </li>
      ))}
    </ol>
  )
}

function authorLabel(c: IssueComment, meId: number | undefined): string {
  if (meId !== undefined && c.author_id === meId) return 'You'
  if (c.author_side === 'operator') return c.author_name ? `${c.author_name} (support team)` : 'Support team'
  return c.author_name || 'Former member'
}

/** What an entry did besides its comment: the status it set, the field it changed. */
function EntryAction({ comment, scope }: { comment: IssueComment; scope: IssueScope }) {
  const { status, change } = comment
  if (status === 'duplicate' && change?.field === 'duplicate') {
    return <span data-testid="issue-comment-action">marked it a <IssueStatusChip status="duplicate" size="small" />{duplicateOf(change, scope)}</span>
  }
  if (status) {
    return <span className="flex items-center gap-1" data-testid="issue-comment-action">set the status to <IssueStatusChip status={status} size="small" /></span>
  }
  if (!change) return null
  return <span className="flex flex-wrap items-center gap-1" data-testid="issue-comment-action">{changeText(change)}</span>
}

/** " of #12" with a link, for the support team, who see the original's id. */
function duplicateOf(change: IssueChange, scope: IssueScope): ReactNode {
  if (scope !== 'platform' || !change.to) return null
  return <> of <TextLink to={`/platform/issues/${change.to}`}>#{change.to}</TextLink></>
}

const tagList = (v: string | undefined) => (v ? v.split(',') : [])

/** One line saying what a field change did. */
function changeText(change: IssueChange): ReactNode {
  const { field, from, to } = change
  switch (field) {
    case 'kind':
      return <>changed the kind to {ISSUE_KIND_LABELS[to as IssueKind] ?? to}</>
    case 'assignee':
      if (to !== undefined) return <>assigned it to {to}</>
      return <>unassigned {from ?? 'it'}</>
    case 'priority':
      return to && to in ISSUE_PRIORITY
        ? <>set the priority to <IssuePriorityChip priority={to as IssuePriority} size="small" /></>
        : <>cleared the priority</>
    case 'severity':
      return to && to in ISSUE_SEVERITY
        ? <>set the severity to <IssueSeverityChip severity={to as IssueSeverity} size="small" /></>
        : <>cleared the severity</>
    case 'area':
      return <>moved it to {ISSUE_AREA_LABELS[to as IssueArea] ?? to}</>
    case 'tags': {
      const before = tagList(from), after = tagList(to)
      const added = after.filter(t => !before.includes(t)), removed = before.filter(t => !after.includes(t))
      return (
        <>
          {added.length > 0 && <>tagged it {added.map(t => <Chip key={t} size="small">{t}</Chip>)}</>}
          {added.length > 0 && removed.length > 0 && ' and '}
          {removed.length > 0 && <>removed {removed.map(t => <Chip key={t} size="small">{t}</Chip>)}</>}
        </>
      )
    }
    case 'confirmed':
      return <>confirmed the fix</>
    case 'duplicate':
      return <>marked it a duplicate</>
  }
}
