import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { formatChangeValue, renderEventSentence, workflowStepsChangesOf } from './project-event-render'
import type { ProjectEvent } from '@/api/project-events'

afterEach(cleanup)

function makeEvent(eventType: string, details: Record<string, unknown>): ProjectEvent {
  return {
    id: 1,
    event_type: eventType,
    actor: null,
    file_id: 10,
    job_id: 20,
    details,
    created_at: '2026-08-26T10:00:00Z',
  }
}

function sentenceText(ev: ProjectEvent): string {
  const node = renderEventSentence(ev)
  expect(node).not.toBeNull()
  return render(<>{node}</>).container.textContent ?? ''
}

// TM/terminology assignment events carry structured before/after rows
// ({tm_id, name, mode, priority}); the renderer must say WHAT changed
// (added / removed / reordered / mode) instead of dumping both name lists.
describe('project.tm_assignments_set rendering', () => {
  const tm = (tmId: number, name: string, mode = 'read_write', priority = 0) =>
    ({ tm_id: tmId, name, mode, priority })

  it('renders an add as "added TM"', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0)],
      after: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 1)],
    }))
    expect(text).toBe('added TM “J31 TM”')
  })

  it('renders a remove as "removed TM"', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 1)],
      after: [tm(1, 'Default TM', 'read_write', 0)],
    }))
    expect(text).toBe('removed TM “J31 TM”')
  })

  it('renders a mode toggle as a mode change, not an identical name dump', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 1)],
      after: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read', 1)],
    }))
    expect(text).toBe('set TM “J31 TM” to read-only')
  })

  it('renders a penalty change (per-TM penalty rides the same PUT)', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [{ ...tm(1, 'Default TM'), penalty_pct: 1 }],
      after: [{ ...tm(1, 'Default TM'), penalty_pct: 5 }],
    }))
    expect(text).toBe('set TM “Default TM” penalty to 5%')
  })

  it('renders a pure reorder as a reorder', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 1)],
      after: [tm(2, 'J31 TM', 'read_write', 0), tm(1, 'Default TM', 'read_write', 1)],
    }))
    expect(text).toBe('reordered TMs: Default TM, J31 TM → J31 TM, Default TM')
  })

  it('joins multiple changes into one sentence', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0), tm(3, 'Old TM', 'read_write', 1)],
      after: [tm(1, 'Default TM', 'read', 0), tm(2, 'J31 TM', 'read_write', 1)],
    }))
    expect(text).toBe('added TM “J31 TM”; removed TM “Old TM”; set TM “Default TM” to read-only')
  })

  it('falls back to a generic sentence when nothing visibly changed (priority-only reindex)', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {
      before: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 3)],
      after: [tm(1, 'Default TM', 'read_write', 0), tm(2, 'J31 TM', 'read_write', 1)],
    }))
    expect(text).toBe('updated TM assignments (Default TM, J31 TM)')
  })

  it('falls back to the legacy list dump when before/after are missing', () => {
    const text = sentenceText(makeEvent('project.tm_assignments_set', {}))
    expect(text).toBe('changed TM assignments: none → none')
  })
})

describe('project.terminology_assignments_set rendering', () => {
  const tb = (id: number, name: string, src: string, tgt: string, mode = 'read', priority = 0) =>
    ({ term_base_id: id, name, source_language: src, target_language: tgt, mode, priority })

  it('renders an add with the language pair', () => {
    const text = sentenceText(makeEvent('project.terminology_assignments_set', {
      before: [tb(1, 'Default Term Base', 'en', 'de')],
      after: [tb(1, 'Default Term Base', 'en', 'de'), tb(2, 'Legal Terms', 'en', 'fr')],
    }))
    expect(text).toBe('added term base “Legal Terms” (en→fr)')
  })

  it('renders a mode change per pair', () => {
    const text = sentenceText(makeEvent('project.terminology_assignments_set', {
      before: [tb(1, 'Default Term Base', 'en', 'de', 'read')],
      after: [tb(1, 'Default Term Base', 'en', 'de', 'read_write')],
    }))
    expect(text).toBe('set term base “Default Term Base” (en→de) to read+write')
  })
})

// Automation outcomes: pipeline_completed events carry what actually happened
// (QE+APE counts, phases skipped) and step assignments carry their deadline —
// the History row should surface all of it.
describe('automation and assignment detail rendering', () => {
  it('QE+APE completion shows the outcome counts', () => {
    const text = sentenceText(makeEvent('job.pipeline_completed', {
      phase: 'QE+APE', file_name: 'manual.docx', target_language: 'de',
      auto_approved: 12, needs_review: 3,
    }))
    expect(text).toBe('finished QE+APE for manual.docx (de) (12 auto-approved, 3 need review)')
  })

  it('pre-translation completion notes skipped phases', () => {
    const text = sentenceText(makeEvent('job.pipeline_completed', {
      phase: 'TM + MT pre-translation', file_name: 'manual.docx', target_language: 'de',
      tm_skipped: 'no_assignment',
    }))
    expect(text).toBe('finished TM + MT pre-translation for manual.docx (de) (TM skipped: no TM for this language)')
  })

  it('says in words why MT was skipped', () => {
    const skipped = (mt_skipped: string) => sentenceText(makeEvent('job.pipeline_completed', {
      phase: 'TM + MT pre-translation', file_name: 'manual.docx', target_language: 'de', mt_skipped,
    }))
    expect(skipped('no_route')).toBe('finished TM + MT pre-translation for manual.docx (de) (MT skipped: the MT router has no engine for this language)')
    expect(skipped('no_assignment')).toBe('finished TM + MT pre-translation for manual.docx (de) (MT skipped: no MT router set up)')
    expect(skipped('no_provider')).toBe('finished TM + MT pre-translation for manual.docx (de) (MT skipped: the AI engine cannot translate this language)')
    expect(skipped('no_segments')).toBe('finished TM + MT pre-translation for manual.docx (de) (MT skipped: nothing left to translate)')
    expect(skipped('some_new_reason')).toBe('finished TM + MT pre-translation for manual.docx (de) (MT skipped: some new reason)')
  })

  it('pipeline failure names the phase and carries the error', () => {
    const text = sentenceText(makeEvent('job.pipeline_failed', {
      phase: 'MT', file_name: 'manual.docx', target_language: 'de', error: 'engine timeout',
    }))
    expect(text).toBe('MT failed for manual.docx (de) — engine timeout')
  })

  it('stop request reads as the actor asking for a stop on the file', () => {
    const text = sentenceText(makeEvent('job.stop_requested', {
      file_name: 'manual.docx', target_language: 'de',
    }))
    expect(text).toBe('requested a stop of the run on manual.docx (de)')
  })

  it('pipeline stop names the phase and the translated / total counts', () => {
    const text = sentenceText(makeEvent('job.pipeline_stopped', {
      phase: 'MT', file_name: 'manual.docx', target_language: 'de',
      translated_count: 1230, total_count: 22857,
    }))
    expect(text).toBe('stopped MT for manual.docx (de) after 1,230 / 22,857 segments')
  })

  it('pipeline stop without counts falls back to the default phase and omits the tail', () => {
    const text = sentenceText(makeEvent('job.pipeline_stopped', {
      file_name: 'manual.docx', target_language: 'de',
    }))
    expect(text).toBe('stopped pre-translation for manual.docx (de)')
  })

  it('step assignment names the assignee and shows the deadline when one was set', () => {
    const text = sentenceText(makeEvent('workflow.step_assigned', {
      step: 'Translation', file_name: 'manual.docx', target_language: 'it-it',
      assignee_name: 'John Translator', assignee_email: 'john@x.test', deadline: '2026-08-30T17:00:00Z',
    }))
    expect(text).toBe('assigned step “Translation” on manual.docx (it-it) to John Translator — deadline Aug 30, 2026')
  })

  it('step assignment recorded with only an email shows the email', () => {
    const text = sentenceText(makeEvent('workflow.step_assigned', {
      step: 'Review', file_name: 'manual.docx', target_language: 'de',
      assignee_email: 'jane@x.test',
    }))
    expect(text).toBe('assigned step “Review” on manual.docx (de) to jane@x.test')
  })
})

// workflow_steps_set carries full before/after step snapshots; the diff goes
// in the Changes cell (workflowStepsChangesOf), not the sentence.
describe('project.workflow_steps_set changes', () => {
  const step = (name: string, extra: Record<string, unknown> = {}) =>
    ({ name, role_required: 'translator', ...extra })
  const users = new Map([[12, 'Jane Doe'], [15, 'john@x.test']])
  const changesFor = (before: unknown, after: unknown) =>
    workflowStepsChangesOf(makeEvent('project.workflow_steps_set', { before, after }),
      (id) => users.get(id) ?? null)

  it('renders the sentence without the redundant name dump', () => {
    const text = sentenceText(makeEvent('project.workflow_steps_set', {
      before: [step('translation')], after: [step('translation')],
    }))
    expect(text).toBe('edited workflow steps')
  })

  it('membership change renders one Steps group', () => {
    expect(changesFor(
      [step('translation')],
      [step('translation'), step('review', { role_required: 'reviewer' })],
    )).toEqual([{ label: 'Steps', entries: [{ from: 'translation', to: 'translation, review' }] }])
  })

  it('reorder renders as a Steps group', () => {
    expect(changesFor(
      [step('translation'), step('review')],
      [step('review'), step('translation')],
    )).toEqual([{ label: 'Steps', entries: [{ from: 'translation, review', to: 'review, translation' }] }])
  })

  it('deadline edit renders a per-step group (raw ISO; formatChangeValue formats it)', () => {
    expect(changesFor(
      [step('translation'), step('review')],
      [step('translation'), step('review', { deadline: '2026-09-03T17:00:00Z' })],
    )).toEqual([{
      label: '“review”',
      entries: [{ field: 'deadline', from: null, to: '2026-09-03T17:00:00Z' }],
    }])
  })

  it('assignee edit resolves ids via the members callback', () => {
    expect(changesFor(
      [step('review', { default_assignee: 12 })],
      [step('review', { default_assignee: 15 })],
    )).toEqual([{
      label: '“review”',
      entries: [{ field: 'assignee', from: 'Jane Doe', to: 'john@x.test' }],
    }])
  })

  it('unresolvable assignee falls back to "user #id"', () => {
    expect(changesFor(
      [step('review')],
      [step('review', { default_assignee: 99 })],
    )).toEqual([{
      label: '“review”',
      entries: [{ field: 'assignee', from: null, to: 'user #99' }],
    }])
  })

  it('role edit renders a role entry', () => {
    expect(changesFor(
      [step('review')],
      [step('review', { role_required: 'reviewer' })],
    )).toEqual([{
      label: '“review”',
      entries: [{ field: 'role', from: 'translator', to: 'reviewer' }],
    }])
  })

  it('AI router edit prefers the enriched name, falls back to #id', () => {
    expect(changesFor(
      [step('mt', { ai_router_id: 3, ai_router_name: 'Production' })],
      [step('mt', { ai_router_id: 4 })],
    )).toEqual([{
      label: '“mt”',
      entries: [{ field: 'AI router', from: 'Production', to: '#4' }],
    }])
  })

  // Older stored payloads carry ai_engine_* or ai_system_* keys; both render under the same column.
  it('reads the legacy ai_engine_* and ai_system_* keys of stored payloads', () => {
    expect(changesFor(
      [step('mt', { ai_engine_id: 3, ai_engine_name: 'QE strict' })],
      [step('mt', { ai_engine_id: 4 })],
    )).toEqual([{
      label: '“mt”',
      entries: [{ field: 'AI router', from: 'QE strict', to: '#4' }],
    }])
    expect(changesFor(
      [step('mt', { ai_system_id: 3, ai_system_name: 'QE strict' })],
      [step('mt', { ai_system_id: 4 })],
    )).toEqual([{
      label: '“mt”',
      entries: [{ field: 'AI router', from: 'QE strict', to: '#4' }],
    }])
  })

  it('groups several field edits on one step into ONE row', () => {
    // Assignee + deadline edited in one save render as one “Translation” row,
    // not two.
    expect(changesFor(
      [step('Translation', { default_assignee: 15 }), step('Review')],
      [
        step('Translation', { default_assignee: 12, deadline: '2026-08-27T14:50:00Z' }),
        step('Review', { deadline: '2026-08-28T14:50:00Z' }),
      ],
    )).toEqual([
      {
        label: '“Translation”',
        entries: [
          { field: 'assignee', from: 'john@x.test', to: 'Jane Doe' },
          { field: 'deadline', from: null, to: '2026-08-27T14:50:00Z' },
        ],
      },
      {
        label: '“Review”',
        entries: [{ field: 'deadline', from: null, to: '2026-08-28T14:50:00Z' }],
      },
    ])
  })

  it('duplicate step names diff positionally, not first-with-both', () => {
    expect(changesFor(
      [step('review'), step('review')],
      [step('review'), step('review', { role_required: 'reviewer' })],
    )).toEqual([{
      label: '“review”',
      entries: [{ field: 'role', from: 'translator', to: 'reviewer' }],
    }])
  })

  it('combines membership and field changes, Steps group first', () => {
    expect(changesFor(
      [step('translation'), step('proofread')],
      [step('translation', { deadline: '2026-09-03T17:00:00Z' })],
    )).toEqual([
      { label: 'Steps', entries: [{ from: 'translation, proofread', to: 'translation' }] },
      { label: '“translation”', entries: [{ field: 'deadline', from: null, to: '2026-09-03T17:00:00Z' }] },
    ])
  })

  it('returns null for unparseable payloads', () => {
    expect(changesFor(undefined, undefined)).toBeNull()
    expect(changesFor([{ role_required: 'translator' }], [])).toBeNull()
  })
})

// Deadline values in details.changes are RFC 3339 strings (project deadline
// in metadata_updated, step deadlines in step_deadline_set and
// workflow_steps_set) — the Changes cell must render them as dates, not raw
// ISO. Time-of-day is kept so a time-only edit doesn't render as "Aug 30,
// 2026 → Aug 30, 2026".
describe('formatChangeValue timestamps', () => {
  it('formats a full ISO timestamp as a date with time (UTC)', () => {
    expect(formatChangeValue('2026-09-03T17:00:00Z')).toBe('Sep 3, 2026, 05:00 PM')
  })

  it('formats offset and fractional-second variants', () => {
    expect(formatChangeValue('2026-09-03T17:00:00+02:00')).toBe('Sep 3, 2026, 03:00 PM')
    expect(formatChangeValue('2026-09-03T17:00:00.000Z')).toBe('Sep 3, 2026, 05:00 PM')
  })

  it('leaves ordinary and date-prefixed strings alone', () => {
    expect(formatChangeValue('Booking Q3 batch')).toBe('Booking Q3 batch')
    expect(formatChangeValue('2026-09-03 delivery notes')).toBe('2026-09-03 delivery notes')
  })

  it('still renders empties as an em dash and booleans as on/off', () => {
    expect(formatChangeValue(null)).toBe('—')
    expect(formatChangeValue('')).toBe('—')
    expect(formatChangeValue(true)).toBe('on')
  })
})

// job.exported carries details.format ("source" | "xliff") — the two export
// buttons must render as distinct history sentences.
describe('job.exported rendering', () => {
  it('renders the final-file export', () => {
    const text = sentenceText(
      makeEvent('job.exported', { format: 'source', file_name: 'manual.docx', target_language: 'de' }),
    )
    expect(text).toBe('exported the final file for manual.docx (de)')
  })

  it('renders the bilingual XLIFF export', () => {
    const text = sentenceText(
      makeEvent('job.exported', { format: 'xliff', file_name: 'manual.docx', target_language: 'de' }),
    )
    expect(text).toBe('exported a bilingual XLIFF for manual.docx (de)')
  })
})

// Downloading the untouched upload is its own event. Without
// a renderer entry the History tab drops the row entirely, so the audit trail
// would silently omit source-file downloads.
describe('file.downloaded rendering', () => {
  it('names the file and distinguishes itself from an export', () => {
    const text = sentenceText(makeEvent('file.downloaded', { file_name: 'brief.docx' }))
    expect(text).toBe('downloaded the source file brief.docx')
  })
})

describe('project.published rendering', () => {
  it('says the project was published', () => {
    expect(sentenceText(makeEvent('project.published', {}))).toBe('published the project')
  })
})
