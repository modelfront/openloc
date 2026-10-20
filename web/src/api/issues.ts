import { request } from './request'
import { fetchBlob } from './download'

// Issues a user reports to the support team (the deployment's platform admins),
// and their conversation. Managers see their org's, other members their own;
// operators see every org's.

export type IssueKind = 'bug' | 'enhancement' | 'feature_request'
/** Bugs only. */
export type IssueSeverity = 'minor' | 'major' | 'critical' | 'blocker'
export type IssueArea =
  | 'editor' | 'projects' | 'tasks' | 'tm' | 'terminology' | 'mt' | 'ai_engines' | 'chat' | 'qa' | 'workflow'
  | 'project_settings' | 'general_settings' | 'dashboards' | 'integrations' | 'api' | 'mcp' | 'other'
export type IssueStatus = 'open' | 'in_progress' | 'needs_info' | 'resolved' | 'wont_fix' | 'duplicate'
export type IssueSide = 'reporter' | 'operator'
/** The support team's own urgency; the reporter's org never sees it. */
export type IssuePriority = 'low' | 'medium' | 'high'
/** The assistant's check of a report it wrote. The support team's alone. */
export type IssueVerification = 'verified' | 'not_reproduced' | 'unverified'

/** The assistant's check as the report form sends it, with the token that proves it. */
export interface IssueCheck {
  result: IssueVerification
  note: string
  token: string
}

/** What the app attaches to a report. The role is added by the server. */
export interface IssueContext {
  path?: string
  project_id?: number
  file_id?: number
  job_id?: number
  segment_id?: number
  browser?: string
  os?: string
  viewport?: string
  app_version?: string
  role?: string
  /** Set by the server when the report did not come from the form: "AI assistant". */
  via?: string
}

/**
 * One event of the action trail a report carries: the last pages, clicks,
 * failed API calls, console errors and notices before it. The support team
 * reads it; no model does.
 */
export type TrailEvent =
  | { at: string; kind: 'page'; path: string }
  | { at: string; kind: 'click'; target: string }
  /** body: the start of a failed call's response, in a recording only. */
  | { at: string; kind: 'api'; method: string; path: string; status: number; body?: string }
  | { at: string; kind: 'console'; message: string }
  | { at: string; kind: 'toast'; level: string; message: string }

/** One event of a watch-mode recording: the trail's events, and what the user typed in a field. */
export type RecordingEvent = TrailEvent | { at: string; kind: 'input'; target: string; value: string }

/**
 * The steps the user repeated while the browser recorded them, for the
 * support team. No model reads it.
 */
export interface IssueRecording {
  started_at: string
  ended_at: string
  events: RecordingEvent[]
}

/** A file on an issue or a comment. Images are shown; other files download. */
export interface IssueAttachment {
  id: number
  filename: string
  size_bytes: number
  is_image: boolean
}

export interface Issue {
  id: number
  kind: IssueKind
  severity: IssueSeverity | null
  area: IssueArea
  title: string
  description: string
  status: IssueStatus
  context: IssueContext
  /** The files sent with the report; a comment's files are on the comment. */
  attachments: IssueAttachment[]
  last_activity_by: IssueSide
  created_at: string
  updated_at: string
  resolved_at: string | null
  /** When the reporter's org confirmed the fix of a resolved issue. */
  confirmed_at: string | null
  /** The support team member working on it; absent until one is assigned. */
  assignee?: { name: string }
  /**
   * The original of a duplicate: its number and status, and its title only
   * when the viewer may open it (`can_open`; for a member, not another org's
   * issue or a colleague's they do not see). Absent once it is gone.
   */
  duplicate_of?: IssueRef
  reporter_name: string
  reporter_email: string
  /** Operators only. */
  org_name?: string
  /** The viewer may delete it: their own, one reported by a lower role, any
   *  of the org's for an owner, any at all for an operator. */
  can_delete: boolean
  /** Operators only. */
  assignee_id?: number | null
  /** Operators only. */
  priority?: IssuePriority | null
  /** Operators only. */
  tags?: string[]
  /** Operators only: where an imported issue came from (a sheet and row). */
  import_key?: string | null
  /** Operators only: the assistant's check of a report it wrote; null on any other report. */
  chat_verification?: IssueVerification | null
  /** Operators only: one line of what the assistant saw. */
  chat_verification_note?: string | null
  /** Operators only, on the detail page: the issues marked as duplicates of this one. */
  duplicates?: IssueRef[]
}

/** Another issue. `title` is absent when the viewer may not open it. */
export interface IssueRef {
  id: number
  title?: string
  status: IssueStatus
  can_open: boolean
}

/** The field an entry changed besides the status. */
export type IssueChangeField = 'kind' | 'assignee' | 'priority' | 'severity' | 'area' | 'tags' | 'duplicate' | 'confirmed'

/**
 * A field change in the history. For the kind, the kinds; for the assignee,
 * names (absent: nobody);
 * for tags, comma-separated lists; for a duplicate, the original's id
 * (operators only); for confirmed, nothing.
 */
export interface IssueChange {
  field: IssueChangeField
  from?: string
  to?: string
}

export interface IssueComment {
  id: number
  /** Null once the author's account is gone. */
  author_id: number | null
  author_side: IssueSide
  author_name: string
  body: string | null
  /** The status this entry set, when it changed the status. */
  status: IssueStatus | null
  /** Operators only: an internal note or an internal change. */
  internal?: boolean
  change?: IssueChange
  attachments: IssueAttachment[]
  created_at: string
}

export interface IssueDetail {
  issue: Issue
  comments: IssueComment[]
  /** Operators only: the action trail the report carried. */
  trail?: TrailEvent[]
  /** Operators only: the recording of the steps the report carried. */
  recording?: IssueRecording
}

export interface IssuePage {
  data: Issue[]
  cursor: string
  has_more: boolean
}

export interface NewIssue {
  kind: IssueKind
  /** Bugs only. */
  severity?: IssueSeverity
  area: IssueArea
  title: string
  description: string
  context: IssueContext
  files?: File[]
  /** A report the assistant wrote: its check, which only the support team sees. */
  verification?: IssueCheck
  /** The action trail, for the support team. */
  trail?: TrailEvent[]
  /** The recording of the steps, for the support team. */
  recording?: IssueRecording
}

/** Who is looking: the reporter's routes or the operator console's. */
export type IssueScope = 'mine' | 'platform'

const base = (scope: IssueScope) => (scope === 'mine' ? '/issues' : '/platform/issues')

function appendFiles(form: FormData, files: File[] = []) {
  for (const f of files) form.append('files', f)
}

function commentForm(body: string, files?: File[]): FormData {
  const form = new FormData()
  if (body.trim()) form.append('body', body)
  appendFiles(form, files)
  return form
}

export function createIssue(input: NewIssue): Promise<Issue> {
  const form = new FormData()
  form.append('kind', input.kind)
  if (input.kind === 'bug' && input.severity) form.append('severity', input.severity)
  form.append('area', input.area)
  form.append('title', input.title)
  form.append('description', input.description)
  form.append('context', JSON.stringify(input.context))
  if (input.verification) {
    form.append('verification', input.verification.result)
    form.append('verification_note', input.verification.note)
    form.append('verification_token', input.verification.token)
  }
  if (input.trail?.length) form.append('trail', JSON.stringify(input.trail))
  if (input.recording) form.append('recording', JSON.stringify(input.recording))
  appendFiles(form, input.files)
  return request<{ data: Issue }>('/issues', { method: 'POST', body: form }).then(r => r.data)
}

/** List filters. An empty list matches any value; a severity matches bugs only. */
export interface IssueFilter {
  statuses?: IssueStatus[]
  kinds?: IssueKind[]
  areas?: IssueArea[]
  severities?: IssueSeverity[]
  /** A part of the title or description. */
  q?: string
  /** Operators only: org ids. */
  orgs?: number[]
  /** Operators only. */
  priorities?: IssuePriority[]
  /** Operators only: issues with any of these tags. */
  tags?: string[]
  /** Operators only: assignee ids, and "none" for the unassigned issues. */
  assignees?: string[]
  /** Operators only: the assistant's check. */
  verifications?: IssueVerification[]
  /** The list is by last update, newest first unless this is set. */
  oldestFirst?: boolean
}

/** A support team member an issue may be assigned to. */
export interface IssueAssignable {
  id: number
  name: string
  email: string
}

/** An org that has issues, for the operators' organization filter. */
export interface IssueOrg {
  id: number
  name: string
}

export function listIssues(scope: IssueScope, cursor = '', filter: IssueFilter = {}): Promise<IssuePage> {
  const params = new URLSearchParams()
  if (cursor) params.set('cursor', cursor)
  const platform = scope === 'platform'
  const lists: [string, (string | number)[] | undefined][] = [
    ['status', filter.statuses], ['kind', filter.kinds], ['area', filter.areas], ['severity', filter.severities],
    ['org', platform ? filter.orgs : undefined], ['priority', platform ? filter.priorities : undefined],
    ['tag', platform ? filter.tags : undefined], ['assignee', platform ? filter.assignees : undefined],
    ['verification', platform ? filter.verifications : undefined],
  ]
  for (const [key, values] of lists) if (values?.length) params.set(key, values.join(','))
  if (filter.q?.trim()) params.set('q', filter.q.trim())
  if (filter.oldestFirst) params.set('sort', 'updated')
  const qs = params.toString()
  return request<IssuePage>(`${base(scope)}${qs ? `?${qs}` : ''}`)
}

/** Operators only: the orgs that have issues. */
export const listIssueOrgs = () => request<{ data: IssueOrg[] }>('/platform/issues/orgs').then(r => r.data)

/** Operators only: the support team, who may be assigned. */
export const listIssueAssignees = () =>
  request<{ data: IssueAssignable[] }>('/platform/issues/assignees').then(r => r.data)

/** Operators only: every tag in use. */
export const listIssueTags = () => request<{ data: string[] }>('/platform/issues/tags').then(r => r.data)

export const getIssue = (scope: IssueScope, id: number) =>
  request<{ data: IssueDetail }>(`${base(scope)}/${id}`).then(r => r.data)

export const deleteIssue = (scope: IssueScope, id: number): Promise<void> =>
  request(`${base(scope)}/${id}`, { method: 'DELETE' })

export const commentOnIssue = (scope: IssueScope, id: number, body: string, files?: File[]) =>
  request<{ data: IssueDetail }>(`${base(scope)}/${id}/comments`, {
    method: 'POST', body: commentForm(body, files),
  }).then(r => r.data)

/**
 * Operators only: change the status, with an optional comment. A duplicate
 * names its original.
 */
export const setIssueStatus = (id: number, status: IssueStatus, note: string, duplicateOf?: number) =>
  request<{ data: IssueDetail }>(`/platform/issues/${id}/status`, {
    method: 'POST', body: JSON.stringify({ status, note, ...(status === 'duplicate' ? { duplicate_of: duplicateOf } : {}) }),
  }).then(r => r.data)

/**
 * Operators only: the fields they change without telling the reporter. A
 * field left out stays as it is; a null priority or assignee clears it. A new
 * kind without a severity drops it, or, for a bug, starts it at Major.
 */
export interface IssueUpdate {
  kind?: IssueKind
  severity?: IssueSeverity
  area?: IssueArea
  priority?: IssuePriority | null
  tags?: string[]
  assignee_id?: number | null
}

export const updateIssue = (id: number, update: IssueUpdate) =>
  request<{ data: IssueDetail }>(`/platform/issues/${id}`, {
    method: 'PATCH', body: JSON.stringify(update),
  }).then(r => r.data)

/** Operators only: an internal note, which the reporter's org never sees. */
export const addIssueNote = (id: number, body: string, files?: File[]) =>
  request<{ data: IssueDetail }>(`/platform/issues/${id}/notes`, {
    method: 'POST', body: commentForm(body, files),
  }).then(r => r.data)

/** A new kind for an issue, with the severity a bug needs. */
export interface IssueKindChange {
  kind: IssueKind
  severity?: IssueSeverity
}

/**
 * The reporter's side changes the kind: the reporter, or a manager who sees
 * the issue (the support team uses `updateIssue`). Recorded in the
 * conversation; nobody is notified.
 */
export const changeIssueKind = (id: number, change: IssueKindChange) =>
  request<{ data: IssueDetail }>(`/issues/${id}/kind`, { method: 'PATCH', body: JSON.stringify(change) }).then(r => r.data)

/** The reporter's org confirms the fix of a resolved issue. */
export const confirmIssueFixed = (id: number) =>
  request<{ data: IssueDetail }>(`/issues/${id}/confirm`, { method: 'POST' }).then(r => r.data)

/** One file of the issue or of one of its comments. */
export const fetchIssueAttachment = (scope: IssueScope, id: number, attachmentId: number) =>
  fetchBlob(`${base(scope)}/${id}/attachments/${attachmentId}`)
