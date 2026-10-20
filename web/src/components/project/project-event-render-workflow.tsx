import type { ProjectEvent } from '@/api/project-events'
import { fileRef, joinAnd, num, personName, str, strArray, type Renderer } from './project-event-render-shared'
import { count } from '../../lib/plural'

// History sentences for job and workflow events: pipeline runs, job
// completion and export, and step hand-offs. renderEventSentence
// (project-event-render.tsx) is the entry point.

function skipReasonText(phase: string, reason: string): string {
  switch (reason) {
    case 'no_assignment': return phase === 'TM' ? 'no TM for this language' : 'no MT router set up'
    case 'no_route': return 'the MT router has no engine for this language'
    case 'no_provider': return 'the AI engine cannot translate this language'
    case 'no_segments': return 'nothing left to translate'
    default: return reason.replaceAll('_', ' ')
  }
}

/** " with Default TM and DeepL Pro": the memories that filled segments and
 *  the MT engine a run used, by name. */
function usedNote(ev: ProjectEvent): string {
  const used = [...strArray(ev.details['tm_names']), str(ev.details['mt_engine'])].filter(Boolean)
  return used.length > 0 ? ` with ${joinAnd(used)}` : ''
}

/** " (12 auto-approved, 3 need review)" / " (TM skipped: no TM for this language)" /
 *  " (no TM matches)" — what the automation decided or why a phase did nothing. */
function runOutcomeNote(ev: ProjectEvent): string {
  const parts: string[] = []
  const approved = num(ev.details['auto_approved'])
  const review = num(ev.details['needs_review'])
  if (approved !== null) {
    parts.push(`${approved} auto-approved${review !== null ? `, ${review} need review` : ''}`)
  }
  const tmSkipped = str(ev.details['tm_skipped'])
  const tmNames = ev.details['tm_names']
  if (tmSkipped) {
    parts.push(`TM skipped: ${skipReasonText('TM', tmSkipped)}`)
  } else if (Array.isArray(tmNames) && tmNames.length === 0) {
    parts.push('no TM matches')
  }
  const mtSkipped = str(ev.details['mt_skipped'])
  if (mtSkipped) {
    parts.push(`MT skipped: ${skipReasonText('MT', mtSkipped)}`)
  }
  return parts.length > 0 ? ` (${parts.join('; ')})` : ''
}

/** "Jane Linguist" for an assignee, or "user #7" when the event names nobody. */
function assigneeName(ev: ProjectEvent): string {
  return personName(ev, 'assignee') || `user #${str(ev.details['assignee_id'], '?')}`
}

/** ISO timestamp → "Aug 30, 2026". Fixed locale AND UTC so the rendered date
 *  (and the tests) don't shift with the viewer's timezone. */
function formatDeadline(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
}

/** " — “…”" quoting the text a person wrote with the action: a sent-back reason or a hand-off note. */
function quotedNote(ev: ProjectEvent, key: string): string {
  const text = str(ev.details[key])
  return text ? ` — “${text}”` : ''
}

/** " — dismissed the error “…”" when completing, sending back or skipping the AI step of a task accepted its error. */
function dismissedErrorNote(ev: ProjectEvent): string {
  const error = str(ev.details['error_dismissed'])
  return error ? ` — dismissed the error “${error}”` : ''
}

const RERUN_PHASE_LABELS: Record<string, string> = {
  ai_step: 'the AI step',
  pretranslation: 'pre-translation',
}

export const JOB_WORKFLOW_RENDERERS: Record<string, Renderer> = {
  'job.tm_apply_started': (ev) => <>ran Apply TM on {fileRef(ev)}{usedNote(ev)}{runOutcomeNote(ev)}</>,
  'job.mt_apply_started': (ev) => <>ran Apply MT on {fileRef(ev)}{usedNote(ev)}{runOutcomeNote(ev)}</>,
  'job.ai_apply_started': (ev) => <>ran the AI step on {fileRef(ev)}</>,
  'job.translations_cleared': (ev) => {
    const cleared = num(ev.details['cleared'])
    return (
      <>
        cleared translations for {fileRef(ev)}
        {cleared !== null ? ` (${count(cleared, 'segment')})` : ''}
      </>
    )
  },
  'job.deleted': (ev) => <>deleted the job for {fileRef(ev)}</>,
  'job.pipeline_completed': (ev) => (
    <>
      finished {str(ev.details['phase'], 'pre-translation')} for {fileRef(ev)}
      {usedNote(ev)}
      {runOutcomeNote(ev)}
      {personName(ev, 'triggered_by') ? ` (triggered by ${personName(ev, 'triggered_by')})` : ''}
    </>
  ),
  'job.pipeline_failed': (ev) => (
    <>
      {str(ev.details['phase'], 'pre-translation')} failed for {fileRef(ev)}
      {str(ev.details['error']) ? ` — ${str(ev.details['error'])}` : ''}
    </>
  ),
  'job.rerun_started': (ev) => (
    <>re-ran {RERUN_PHASE_LABELS[str(ev.details['phase'])] ?? 'the automation'} on {fileRef(ev)}</>
  ),
  'job.ai_step_skipped': (ev) => <>skipped the AI step on {fileRef(ev)}{dismissedErrorNote(ev)}</>,
  'job.stop_requested': (ev) => <>requested a stop of the run on {fileRef(ev)}</>,
  'job.pipeline_stopped': (ev) => {
    const translated = num(ev.details['translated_count'])
    const total = num(ev.details['total_count'])
    return (
      <>
        stopped {str(ev.details['phase'], 'pre-translation')} for {fileRef(ev)}
        {translated !== null && total !== null
          ? ` after ${translated.toLocaleString()} / ${total.toLocaleString()} segments`
          : ''}
      </>
    )
  },
  'job.completed': (ev) => {
    const approved = num(ev.details['approved_count'])
    const tmSaved = num(ev.details['tm_saved_count'])
    return (
      <>
        completed the job for {fileRef(ev)}
        {approved !== null ? ` (${approved} approved` : ''}
        {approved !== null ? `${tmSaved !== null ? `, ${tmSaved} saved to TM` : ''})` : ''}
      </>
    )
  },
  'job.exported': (ev) =>
    str(ev.details['format']) === 'xliff' ? (
      <>exported a bilingual XLIFF for {fileRef(ev)}</>
    ) : (
      <>exported the final file for {fileRef(ev)}</>
    ),
  'job.assigned': (ev) => (
    <>assigned {fileRef(ev)} to {assigneeName(ev)}</>
  ),
  'workflow.step_advanced': (ev) =>
    ev.details['workflow_completed'] === true ? (
      <>completed the final step “{str(ev.details['from_step'], '?')}” on {fileRef(ev)}{quotedNote(ev, 'handoff_note')}{dismissedErrorNote(ev)}</>
    ) : (
      <>advanced {fileRef(ev)} from “{str(ev.details['from_step'], '?')}” to “{str(ev.details['to_step'], '?')}”{quotedNote(ev, 'handoff_note')}{dismissedErrorNote(ev)}</>
    ),
  'workflow.step_rejected': (ev) => (
    <>
      rejected step “{str(ev.details['from_step'], '?')}” on {fileRef(ev)}
      {str(ev.details['to_step']) ? ` back to “${str(ev.details['to_step'])}”` : ''}
      {quotedNote(ev, 'reason')}
      {dismissedErrorNote(ev)}
    </>
  ),
  // An automated step completed itself once its run finished.
  'workflow.step_auto_completed': (ev) => {
    const from = str(ev.details['from_step'], '?')
    const lead = ev.details['step_type'] === 'pretranslate'
      ? <>pre-translated {fileRef(ev)} and completed “{from}”</>
      : <>completed the AI step “{from}” on {fileRef(ev)}</>
    return ev.details['workflow_completed'] === true
      ? <>{lead} — the job is complete</>
      : <>{lead}, then moved it to “{str(ev.details['to_step'], '?')}”</>
  },
  // A manager's edit reopened a completed task.
  'workflow.step_reopened': (ev) => (
    <>
      reopened “{str(ev.details['step'], '?')}” on {fileRef(ev)} by editing it
      {ev.details['job_reopened'] === true ? ' — the job is open again' : ''}
    </>
  ),
  'workflow.step_assigned': (ev) => (
    <>
      assigned step “{str(ev.details['step'], '?')}” on {fileRef(ev)} to {assigneeName(ev)}
      {str(ev.details['deadline']) ? ` — deadline ${formatDeadline(str(ev.details['deadline']))}` : ''}
    </>
  ),
  'workflow.step_deadline_set': (ev) => (
    <>changed the deadline of step “{str(ev.details['step'], '?')}” on {fileRef(ev)}</>
  ),
}
