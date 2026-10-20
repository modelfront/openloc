import type { ReactNode } from 'react'
import type { FieldChange, ProjectEvent } from '@/api/project-events'
import { fileRef, num, str, strList, type Renderer } from './project-event-render-shared'
import { count } from '../../lib/plural'
import { JOB_WORKFLOW_RENDERERS } from './project-event-render-workflow'

// Human rendering for project history events. One entry
// per event_type; unknown types fall back to the raw slug + pretty details so
// new backend event types render usefully before the FE catches up. The job
// and workflow sentences live in project-event-render-workflow.tsx.

/** Actor display precedence: joined user → emit-time snapshot → System. */
export function eventActorName(ev: ProjectEvent): string {
  if (ev.actor?.name) return ev.actor.name
  if (ev.actor?.email) return ev.actor.email
  const snap = ev.details['actor_name']
  if (typeof snap === 'string' && snap !== '') return snap
  return 'System'
}

// Full ISO-8601 timestamp — the shape event payloads emit for deadlines
// (project deadline in metadata_updated, step deadlines in step_deadline_set
// and workflow_steps_set). Anchored so ordinary strings never match.
const ISO_TIMESTAMP_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})$/

/** ISO timestamp → "Aug 30, 2026, 05:00 PM". Fixed locale AND UTC (same
 *  rationale as formatDeadline). Unlike the sentence renderings, the changes
 *  cell keeps the time-of-day — date-only would render a time-only deadline
 *  edit as "Aug 30, 2026 → Aug 30, 2026". */
function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('en-US', {
    month: 'short', day: 'numeric', year: 'numeric',
    hour: '2-digit', minute: '2-digit', timeZone: 'UTC',
  })
}

/** Renders one details.changes value for the diff rows. */
export function formatChangeValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'boolean') return v ? 'on' : 'off'
  if (typeof v === 'string') return ISO_TIMESTAMP_RE.test(v) ? formatTimestamp(v) : v
  if (typeof v === 'number') return String(v)
  return JSON.stringify(v)
}

export function changesOf(ev: ProjectEvent): Record<string, FieldChange> | null {
  const raw = ev.details['changes']
  if (!raw || typeof raw !== 'object') return null
  return raw as Record<string, FieldChange>
}

/** before/after arrays of {name?, ...} → comma-joined names for a sentence. */
function namesOf(v: unknown): string {
  if (!Array.isArray(v) || v.length === 0) return 'none'
  const names = v.map((item) => {
    if (item && typeof item === 'object') {
      const o = item as Record<string, unknown>
      return str(o['name']) || str(o['tm_id']) || str(o['term_base_id']) || '?'
    }
    return '?'
  })
  return names.join(', ')
}

// --- assignment-set diffing -------------------------------------------------
// tm/terminology_assignments_set events carry structured before/after rows,
// diffed into added / removed / reordered / mode changes. Dumping both name
// lists renders a mode toggle as "A, B → A, B".

const MODE_LABELS: Record<string, string> = {
  read: 'read-only',
  write: 'write-only',
  read_write: 'read+write',
}

interface AssignmentRowView {
  key: string    // identity across before/after (tm_id, or term_base_id+pair)
  label: string  // quoted human name
  suffix: string // rendered outside the quotes, e.g. " (en→fr)"
  mode: string
  penalty: number | null // per-TM penalty_pct; null when the payload predates it / N/A
}

/** Parses one details.before/after array into keyed row views; null when the
 *  payload lacks the structured shape (defensive fallback to the list dump). */
function assignmentRows(
  v: unknown,
  parse: (o: Record<string, unknown>) => AssignmentRowView | null,
): AssignmentRowView[] | null {
  if (!Array.isArray(v)) return null
  const out: AssignmentRowView[] = []
  for (const item of v) {
    if (!item || typeof item !== 'object') return null
    const row = parse(item as Record<string, unknown>)
    if (row === null) return null
    out.push(row)
  }
  return out
}

function renderAssignmentsSet(
  ev: ProjectEvent,
  noun: string,        // "TM" / "term base" — pluralized with a plain "s"
  legacyNoun: string,  // the old sentence's noun, kept for the fallback
  parse: (o: Record<string, unknown>) => AssignmentRowView | null,
): ReactNode {
  const before = assignmentRows(ev.details['before'], parse)
  const after = assignmentRows(ev.details['after'], parse)
  if (before === null || after === null) {
    // Legacy/degenerate payload — the original list-dump rendering.
    return <>changed {legacyNoun}: {namesOf(ev.details['before'])} → {namesOf(ev.details['after'])}</>
  }

  const beforeByKey = new Map(before.map(r => [r.key, r]))
  const afterKeys = new Set(after.map(r => r.key))
  const quoted = (r: AssignmentRowView) => `“${r.label}”${r.suffix}`

  const clauses: string[] = []
  const added = after.filter(r => !beforeByKey.has(r.key))
  const removed = before.filter(r => !afterKeys.has(r.key))
  if (added.length > 0) clauses.push(`added ${noun}${added.length === 1 ? '' : 's'} ${added.map(quoted).join(', ')}`)
  if (removed.length > 0) clauses.push(`removed ${noun}${removed.length === 1 ? '' : 's'} ${removed.map(quoted).join(', ')}`)
  for (const r of after) {
    const prev = beforeByKey.get(r.key)
    if (!prev) continue
    if (prev.mode !== r.mode) clauses.push(`set ${noun} ${quoted(r)} to ${MODE_LABELS[r.mode] ?? r.mode}`)
    if (prev.penalty !== null && r.penalty !== null && prev.penalty !== r.penalty) {
      clauses.push(`set ${noun} ${quoted(r)} penalty to ${r.penalty}%`)
    }
  }
  // Pure reorder (priority = list order). Only meaningful when membership is
  // unchanged — with adds/removes the sequence shift is implied noise.
  if (added.length === 0 && removed.length === 0 &&
    before.map(r => r.key).join('|') !== after.map(r => r.key).join('|')) {
    clauses.push(`reordered ${noun}s: ${before.map(r => r.label).join(', ')} → ${after.map(r => r.label).join(', ')}`)
  }

  if (clauses.length === 0) {
    // Nothing user-visible changed (e.g. a priority renumbering that kept the
    // order) — still an honest save, just don't pretend it was a list swap.
    return <>updated {legacyNoun} ({after.map(r => r.label).join(', ') || 'none'})</>
  }
  return <>{clauses.join('; ')}</>
}

const parseTMAssignmentRow = (o: Record<string, unknown>): AssignmentRowView | null => {
  if (typeof o['tm_id'] !== 'number') return null
  return {
    key: String(o['tm_id']),
    label: str(o['name']) || `TM #${o['tm_id']}`,
    suffix: '',
    mode: str(o['mode']),
    penalty: typeof o['penalty_pct'] === 'number' ? o['penalty_pct'] : null,
  }
}

const parseTermAssignmentRow = (o: Record<string, unknown>): AssignmentRowView | null => {
  if (typeof o['term_base_id'] !== 'number') return null
  const pair = `${str(o['source_language'], '?')}→${str(o['target_language'], '?')}`
  return {
    key: `${o['term_base_id']}|${pair}`,
    label: str(o['name']) || `term base #${o['term_base_id']}`,
    suffix: ` (${pair})`,
    mode: str(o['mode']),
    penalty: null, // term base assignments carry no penalty
  }
}

// --- workflow-steps diffing ---------------------------------------------------
// workflow_steps_set carries full before/after step snapshots ({name,
// role_required, default_assignee, deadline, ai_router_id, ai_router_name}).
// The Changes cell renders per-step field diffs, built here in ChangesCell's
// {label: {from, to}} shape; a name-list sentence ("A, B → A, B") says nothing
// about a deadline/assignee/role edit.

interface WorkflowStepView {
  key: string // name + occurrence index — steps have no id in the payload
  name: string
  role: string
  assignee: number | null
  deadline: string | null // ISO
  aiRouter: string | null // "name" or "#id" for pre-enrichment payloads
}

/** Parses one details.before/after array; null when the payload lacks the
 *  structured shape (legacy/degenerate events fall back to no Changes cell). */
function workflowStepRows(v: unknown): WorkflowStepView[] | null {
  if (!Array.isArray(v)) return null
  const seen = new Map<string, number>()
  const out: WorkflowStepView[] = []
  for (const item of v) {
    if (!item || typeof item !== 'object') return null
    const o = item as Record<string, unknown>
    const name = str(o['name'])
    if (name === '') return null
    // Duplicate step names are legal — disambiguate by occurrence so a diff
    // of ("review", "review") pairs first-with-first, not first-with-both.
    const n = (seen.get(name) ?? 0) + 1
    seen.set(name, n)
    // Stored payloads may carry the older per-step engine keys (ai_engine_* or
    // ai_system_*); they render under the same column.
    const aiId = num(o['ai_router_id'] ?? o['ai_engine_id'] ?? o['ai_system_id'])
    out.push({
      key: n === 1 ? name : `${name} #${n}`,
      name,
      role: str(o['role_required']),
      assignee: num(o['default_assignee']),
      deadline: str(o['deadline']) || null,
      aiRouter: str(o['ai_router_name'] ?? o['ai_engine_name'] ?? o['ai_system_name']) || (aiId !== null ? `#${aiId}` : null),
    })
  }
  return out
}

/** One from→to diff. `field` labels it when a group holds several ("assignee",
 *  "deadline"); a group's sole unnamed entry renders as a bare from→to. */
export interface ChangeEntry {
  field?: string
  from: unknown
  to: unknown
}

/** One Changes-cell row: a label and the entries rendered together after it. */
export interface ChangeGroup {
  label: string
  entries: ChangeEntry[]
}

/** Change groups for a workflow_steps_set event: a Steps membership row first,
 *  then ONE row per changed step with all its field diffs together (assignee +
 *  deadline edited in one save → one “Translation” row, not two). null =
 *  unparseable payload; empty = a save with no user-visible change.
 *  `userLabel` resolves assignee ids against the members list (payload stores
 *  ids only); unresolved ids render as "user #id". */
const FIELD_LABELS: Record<string, string> = {
  name: 'Name',
  description: 'Description',
  status: 'Status',
  customer_id: 'Customer ID',
  customer_name: 'Customer',
  deadline: 'Deadline',
  client: 'Client',
  domain: 'Domain',
  fuzzy_threshold: 'Fuzzy threshold',
  sublanguage_penalty_pct: 'Language-variant mismatch penalty',
  near_exact_penalty_pct: 'Near-exact penalty',
  tm_lookup_order: 'TM lookup order',
  mt_on_upload: 'MT on upload',
  tm_on_upload: 'TM on upload',
  inline_html: 'Strings contain HTML',
  qa_profile_id: 'QA profile ID',
  qa_profile_name: 'QA profile',
  mt_router_id: 'MT router ID',
  mt_router_name: 'MT router',
}

/** details.changes ({field: {from,to}}) → one single-entry group per field,
 *  so flat diffs and the grouped workflow-step diffs share one render path. */
export function flatChangeGroups(changes: Record<string, FieldChange>): ChangeGroup[] {
  return Object.entries(changes)
    // details is free-form jsonb — never trust an entry to be a {from,to}
    // object (a malformed one must not crash the whole tab).
    .filter(([, ch]) => ch !== null && typeof ch === 'object')
    // When an id field has a name twin (customer_id/customer_name), show only
    // the name row — the id adds noise for the same fact.
    .filter(([field]) => {
      if (field === 'customer_id' && changes['customer_name']) return false
      if (field === 'qa_profile_id' && changes['qa_profile_name']) return false
      if (field === 'mt_router_id' && changes['mt_router_name']) return false
      return true
    })
    .map(([field, ch]) => ({
      label: FIELD_LABELS[field] ?? field,
      entries: [{ from: ch.from, to: ch.to }],
    }))
}

export function workflowStepsChangesOf(
  ev: ProjectEvent,
  userLabel: (id: number) => string | null,
): ChangeGroup[] | null {
  const before = workflowStepRows(ev.details['before'])
  const after = workflowStepRows(ev.details['after'])
  if (before === null || after === null) return null

  const groups: ChangeGroup[] = []
  const names = (rows: WorkflowStepView[]) => rows.map((r) => r.name).join(', ') || 'none'
  if (before.map((r) => r.key).join('|') !== after.map((r) => r.key).join('|')) {
    groups.push({ label: 'Steps', entries: [{ from: names(before), to: names(after) }] })
  }

  const assigneeLabel = (id: number | null) =>
    id === null ? null : userLabel(id) ?? `user #${id}`
  const beforeByKey = new Map(before.map((r) => [r.key, r]))
  for (const r of after) {
    const prev = beforeByKey.get(r.key)
    if (!prev) continue
    const entries: ChangeEntry[] = []
    if (prev.role !== r.role) entries.push({ field: 'role', from: prev.role, to: r.role })
    if (prev.assignee !== r.assignee) {
      entries.push({ field: 'assignee', from: assigneeLabel(prev.assignee), to: assigneeLabel(r.assignee) })
    }
    if (prev.deadline !== r.deadline) {
      // Raw ISO — formatChangeValue formats timestamps for the Changes cell.
      entries.push({ field: 'deadline', from: prev.deadline, to: r.deadline })
    }
    if (prev.aiRouter !== r.aiRouter) {
      entries.push({ field: 'AI router', from: prev.aiRouter, to: r.aiRouter })
    }
    if (entries.length > 0) groups.push({ label: `“${r.name}”`, entries })
  }
  return groups
}

// Each renderer returns the sentence AFTER the actor name ("<actor> <sentence>").
const RENDERERS: Record<string, Renderer> = {
  'project.created': (ev) => {
    const langs = ev.details['target_languages']
    const langsStr = strList(langs)
    const template = str(ev.details['template_name'])
    return (
      <>
        created the project{langsStr ? ` (${str(ev.details['source_language'])} → ${langsStr})` : ''}
        {template ? ` from template “${template}”` : ''}
        {ev.details['via'] === 'mcp' ? ' via MCP' : ''}
      </>
    )
  },
  'project.published': () => <>published the project</>,
  'project.metadata_updated': () => <>updated project details</>,
  // The actor is whoever finished or removed the last outstanding task, so
  // the phrasing credits the trigger rather than a deliberate status pick.
  'project.auto_completed': () => <>completed the last task — project marked completed</>,
  'project.auto_reopened': () => <>project reopened — it has unfinished tasks again</>,
  'project.settings_updated': () => <>updated project settings</>,
  'project.tm_assignments_set': (ev) => renderAssignmentsSet(ev, 'TM', 'TM assignments', parseTMAssignmentRow),
  'project.mt_assignments_set': (ev) => (
    <>changed MT assignments: {namesOf(ev.details['before'])} → {namesOf(ev.details['after'])}</>
  ),
  'project.terminology_assignments_set': (ev) =>
    renderAssignmentsSet(ev, 'term base', 'term base assignments', parseTermAssignmentRow),
  // The what-changed detail lives in the Changes cell (workflowStepsChangesOf).
  'project.workflow_steps_set': () => <>edited workflow steps</>,
  'project.template_applied': (ev) => <>applied template “{str(ev.details['template_name'], '?')}”</>,
  'project.saved_as_template': (ev) => <>saved the project as template “{str(ev.details['template_name'], '?')}”</>,
  'project.translations_cleared': (ev) => {
    const cleared = num(ev.details['cleared'])
    return (
      <>
        cleared translations project-wide
        {cleared !== null ? ` (${count(cleared, 'segment')}` : ''}
        {cleared !== null ? `, scope: ${str(ev.details['scope'], 'all')})` : ''}
      </>
    )
  },
  'project.pretranslate_started': (ev) => (
    <>started pre-translation ({num(ev.details['queued']) ?? '?'} of {num(ev.details['total']) ?? '?'} jobs queued)</>
  ),
  'file.uploaded': (ev) => {
    const segs = num(ev.details['segment_count'])
    const pre = ev.details['pretranslate']
    let preStr = ''
    if (pre && typeof pre === 'object') {
      const p = pre as Record<string, unknown>
      const phases = [p['tm'] === true ? 'TM' : null, p['mt'] === true ? 'MT' : null].filter(Boolean)
      if (phases.length > 0) preStr = ` and queued ${phases.join(' + ')} pre-translation`
    }
    return (
      <>
        uploaded {str(ev.details['file_name'], 'a file')}
        {segs !== null ? ` (${count(segs, 'segment')})` : ''}
        {preStr}
      </>
    )
  },
  'file.renamed': () => <>renamed a file</>,
  'file.deleted': (ev) => <>deleted file {str(ev.details['file_name'], '?')}</>,
  // The untouched upload leaving the system. Distinct from
  // job.exported, which covers deliverables rebuilt from translations.
  'file.downloaded': (ev) => <>downloaded the source file {str(ev.details['file_name'], '?')}</>,
  'file.reimported': (ev) => (
    <>imported an XLIFF into {fileRef(ev)}: {count(num(ev.details['updated']), 'segment') || '? segments'} updated</>
  ),
  ...JOB_WORKFLOW_RENDERERS,
  'analysis.created': (ev) =>
    ev.details['trigger'] === 'auto' ? (
      <>queued auto-analysis “{str(ev.details['name'], '?')}”</>
    ) : (
      <>created analysis “{str(ev.details['name'], '?')}”</>
    ),
  'analysis.deleted': (ev) => <>deleted analysis “{str(ev.details['name'], '?')}”</>,
}

/** Sentence body for the event; null when the type is unknown to the FE. */
export function renderEventSentence(ev: ProjectEvent): ReactNode | null {
  const renderer = RENDERERS[ev.event_type]
  return renderer ? renderer(ev) : null
}
