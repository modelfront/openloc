import { Bug, Lightbulb, Sparkles, type LucideIcon } from 'lucide-react'
import type { ChipColor } from '@/components/ui/chip'
import type {
  Issue, IssueArea, IssueCheck, IssueKind, IssuePriority, IssueScope, IssueSeverity, IssueStatus, IssueVerification,
} from '@/api'

/** Status names users see, and the colour of each status chip. */
export const ISSUE_STATUS: Record<IssueStatus, { label: string; color: ChipColor }> = {
  open: { label: 'Open', color: 'grey' },
  in_progress: { label: 'In progress', color: 'blue' },
  needs_info: { label: 'Needs info', color: 'yellow' },
  resolved: { label: 'Resolved', color: 'green' },
  wont_fix: { label: "Won't fix", color: 'grey' },
  duplicate: { label: 'Duplicate', color: 'grey' },
}

/** The order operators pick a status in. */
export const ISSUE_STATUS_ORDER: IssueStatus[] = ['open', 'in_progress', 'needs_info', 'resolved', 'wont_fix', 'duplicate']

/** The statuses that still need work, the operators' default filter. */
export const ACTIVE_ISSUE_STATUSES: IssueStatus[] = ['open', 'in_progress', 'needs_info']

export const ISSUE_KIND_LABELS: Record<IssueKind, string> = {
  bug: 'Bug',
  enhancement: 'Enhancement',
  feature_request: 'Feature request',
}

/** The icon of each kind, in the report form and before a title in the lists. */
export const ISSUE_KIND_ICONS: Record<IssueKind, LucideIcon> = {
  bug: Bug,
  enhancement: Sparkles,
  feature_request: Lightbulb,
}

/** Bug severities, mildest first, and the colour of each chip. */
export const ISSUE_SEVERITY: Record<IssueSeverity, { label: string; color: ChipColor }> = {
  minor: { label: 'Minor', color: 'grey' },
  major: { label: 'Major', color: 'yellow' },
  critical: { label: 'Critical', color: 'red' },
  blocker: { label: 'Blocker', color: 'red' },
}
export const ISSUE_SEVERITY_ORDER: IssueSeverity[] = ['minor', 'major', 'critical', 'blocker']

/** The support team's priorities, lowest first, and the colour of each chip. */
export const ISSUE_PRIORITY: Record<IssuePriority, { label: string; color: ChipColor }> = {
  low: { label: 'Low', color: 'grey' },
  medium: { label: 'Medium', color: 'yellow' },
  high: { label: 'High', color: 'red' },
}
export const ISSUE_PRIORITY_ORDER: IssuePriority[] = ['low', 'medium', 'high']

/** The assistant's check of a report it wrote, and the colour of each chip. The support team's alone. */
export const ISSUE_VERIFICATION: Record<IssueVerification, { label: string; color: ChipColor }> = {
  verified: { label: 'Verified', color: 'green' },
  not_reproduced: { label: 'Not reproduced', color: 'yellow' },
  unverified: { label: 'Unverified', color: 'grey' },
}
export const ISSUE_VERIFICATION_ORDER: IssueVerification[] = ['verified', 'not_reproduced', 'unverified']

/** Who has to act next on an issue that still needs work. */
export type IssueWaitingOn = 'support' | 'reporter'

/**
 * Who has to act next: the reporter's org on a question (needs info), the
 * support team on an issue in progress, and on an open issue the side that
 * did not act last; nobody once it is closed.
 */
export function issueWaitingOn(issue: Pick<Issue, 'status' | 'last_activity_by'>): IssueWaitingOn | null {
  switch (issue.status) {
    case 'needs_info': return 'reporter'
    case 'in_progress': return 'support'
    case 'open': return issue.last_activity_by === 'operator' ? 'reporter' : 'support'
    default: return null
  }
}

/**
 * The waiting-on label for a viewer: members read their own side as "Your
 * organization", the support team reads it as "Reporter's org".
 */
export function waitingOnLabel(on: IssueWaitingOn, scope: IssueScope): string {
  if (on === 'support') return 'Support team'
  return scope === 'mine' ? 'Your organization' : "Reporter's org"
}
export const DEFAULT_ISSUE_SEVERITY: IssueSeverity = 'major'

/** The part of OpenLoc an issue is about, in the order the picker lists them. */
export const ISSUE_AREA_LABELS: Record<IssueArea, string> = {
  editor: 'Editor',
  projects: 'Projects',
  tasks: 'Tasks',
  tm: 'Translation memory',
  terminology: 'Terminology',
  mt: 'Machine translation',
  ai_engines: 'AI engines',
  chat: 'AI assistant (chat)',
  qa: 'Quality assurance',
  workflow: 'Workflow',
  project_settings: 'Project settings',
  general_settings: 'General settings',
  dashboards: 'Dashboards & reporting',
  integrations: 'Integrations',
  api: 'API',
  mcp: 'MCP server',
  other: 'Other',
}

/** Context keys as the people reading an issue see them. */
export const ISSUE_CONTEXT_LABELS: Record<string, string> = {
  path: 'Page',
  project_id: 'Project',
  file_id: 'File',
  job_id: 'Job',
  segment_id: 'Segment',
  browser: 'Browser',
  os: 'Operating system',
  viewport: 'Window size',
  app_version: 'App version',
  role: 'Role',
  via: 'Sent through',
  imported_from: 'Imported from',
}

const pick = <V extends string>(labels: Record<V, unknown>, v: unknown): V | undefined =>
  typeof v === 'string' && v in labels ? (v as V) : undefined

/**
 * The report form's defaults from the assistant's report tool arguments,
 * keeping only values the form knows, so a wrong guess falls back to the
 * form's default instead of breaking it. The assistant's check goes along
 * only with the token from its preview, which proves it to the server.
 */
export function issueDefaultsFromToolArgs(args: Record<string, unknown>, verificationToken?: string) {
  const text = (v: unknown) => (typeof v === 'string' ? v : undefined)
  const result = pick(ISSUE_VERIFICATION, args.verification)
  const note = text(args.verification_note)
  const verification: IssueCheck | undefined = result && note && verificationToken
    ? { result, note, token: verificationToken } : undefined
  const screenshotUploadIds = Array.isArray(args.attachment_upload_ids)
    ? args.attachment_upload_ids.filter((id): id is string => typeof id === 'string') : []
  return {
    kind: pick(ISSUE_KIND_LABELS, args.kind),
    severity: pick(ISSUE_SEVERITY, args.severity),
    area: pick(ISSUE_AREA_LABELS, args.area),
    title: text(args.title),
    description: text(args.description),
    verification,
    screenshotUploadIds,
  }
}

/** Picker options for each list above, in their display order. */
export const ISSUE_STATUS_OPTIONS = ISSUE_STATUS_ORDER.map(s => ({ value: s, label: ISSUE_STATUS[s].label }))
export const ISSUE_KIND_OPTIONS = (Object.keys(ISSUE_KIND_LABELS) as IssueKind[]).map(k => ({ value: k, label: ISSUE_KIND_LABELS[k] }))
export const ISSUE_SEVERITY_OPTIONS = ISSUE_SEVERITY_ORDER.map(s => ({ value: s, label: ISSUE_SEVERITY[s].label }))
export const ISSUE_AREA_OPTIONS = (Object.keys(ISSUE_AREA_LABELS) as IssueArea[]).map(a => ({ value: a, label: ISSUE_AREA_LABELS[a] }))
export const ISSUE_PRIORITY_OPTIONS = ISSUE_PRIORITY_ORDER.map(p => ({ value: p, label: ISSUE_PRIORITY[p].label }))
export const ISSUE_VERIFICATION_OPTIONS = ISSUE_VERIFICATION_ORDER.map(v => ({ value: v, label: ISSUE_VERIFICATION[v].label }))

/** Where a scope's issue list lives in the app. */
export const issueListPath = (scope: IssueScope) => (scope === 'mine' ? '/issues' : '/platform/issues')
