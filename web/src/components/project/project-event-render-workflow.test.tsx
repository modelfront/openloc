// History sentences for task reopen, automation re-run, error dismissal, the
// hand-off note and what a TM/MT run filled.
// Each is asserted as the full sentence, so a dropped clause or a raw slug
// ("ai_step") in the copy fails.

import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { renderEventSentence } from './project-event-render'
import type { ProjectEvent } from '@/api/project-events'

afterEach(cleanup)

function sentence(eventType: string, details: Record<string, unknown>): string {
  const ev: ProjectEvent = {
    id: 1, event_type: eventType, actor: null, file_id: 10, job_id: 20, details,
    created_at: '2026-10-01T10:00:00Z',
  }
  const node = renderEventSentence(ev)
  expect(node).not.toBeNull()
  return render(<>{node}</>).container.textContent ?? ''
}

const file = { file_name: 'manual.docx', target_language: 'de' }

describe('workflow.step_reopened', () => {
  it('names the step and the file', () => {
    expect(sentence('workflow.step_reopened', { ...file, step: 'Review', step_type: 'human', job_reopened: false }))
      .toBe('reopened “Review” on manual.docx (de) by editing it')
  })

  it('says the job is open again when the reopen took the job with it', () => {
    expect(sentence('workflow.step_reopened', { ...file, step: 'Review', step_type: 'human', job_reopened: true }))
      .toBe('reopened “Review” on manual.docx (de) by editing it — the job is open again')
  })
})

describe('workflow.step_auto_completed', () => {
  it('a Pre-translation step that finished the workflow', () => {
    expect(sentence('workflow.step_auto_completed', {
      ...file, from_step: 'Pre-translation', step_type: 'pretranslate', phase: 'pretranslation', workflow_completed: true,
    })).toBe('pre-translated manual.docx (de) and completed “Pre-translation” — the job is complete')
  })

  it('an AI step that hands over to the next step', () => {
    expect(sentence('workflow.step_auto_completed', {
      ...file, from_step: 'Auto APE', step_type: 'ai', to_step: 'Review', auto_approved: 7, needs_review: 3,
    })).toBe('completed the AI step “Auto APE” on manual.docx (de), then moved it to “Review”')
  })

  it('an AI step event without a step type still reads as an AI step', () => {
    expect(sentence('workflow.step_auto_completed', { ...file, from_step: 'Auto APE', workflow_completed: true }))
      .toBe('completed the AI step “Auto APE” on manual.docx (de) — the job is complete')
  })
})

describe('job.rerun_started', () => {
  it.each([
    ['ai_step', 're-ran the AI step on manual.docx (de)'],
    ['pretranslation', 're-ran pre-translation on manual.docx (de)'],
  ])('phase %s', (phase, want) => {
    expect(sentence('job.rerun_started', { ...file, phase, error: 'engine timeout' })).toBe(want)
  })
})

describe('job.ai_step_skipped', () => {
  it('names the file and the dismissed error', () => {
    expect(sentence('job.ai_step_skipped', { ...file, error_dismissed: 'engine timeout' }))
      .toBe('skipped the AI step on manual.docx (de) — dismissed the error “engine timeout”')
  })
})

describe('error dismissal on advance and reject', () => {
  it('advance names the dismissed error', () => {
    expect(sentence('workflow.step_advanced', {
      ...file, from_step: 'Translation', to_step: 'Review', error_dismissed: 'engine timeout',
    })).toBe('advanced manual.docx (de) from “Translation” to “Review” — dismissed the error “engine timeout”')
  })

  it('completing the final step names the dismissed error', () => {
    expect(sentence('workflow.step_advanced', {
      ...file, from_step: 'Review', workflow_completed: true, error_dismissed: 'engine timeout',
    })).toBe('completed the final step “Review” on manual.docx (de) — dismissed the error “engine timeout”')
  })

  it('reject names the dismissed error after the reason', () => {
    expect(sentence('workflow.step_rejected', {
      ...file, from_step: 'Review', to_step: 'Translation', reason: 'Wrong terms', error_dismissed: 'engine timeout',
    })).toBe('rejected step “Review” on manual.docx (de) back to “Translation” — “Wrong terms” — dismissed the error “engine timeout”')
  })

  it('without a dismissed error, the sentences are unchanged', () => {
    expect(sentence('workflow.step_advanced', { ...file, from_step: 'Translation', to_step: 'Review' }))
      .toBe('advanced manual.docx (de) from “Translation” to “Review”')
    expect(sentence('workflow.step_rejected', { ...file, from_step: 'Review', to_step: 'Translation' }))
      .toBe('rejected step “Review” on manual.docx (de) back to “Translation”')
  })
})

describe('hand-off note on advance', () => {
  it('quotes the comment written for the next step', () => {
    expect(sentence('workflow.step_advanced', {
      ...file, from_step: 'Translation', to_step: 'Review', handoff_note: 'Please check terminology',
    })).toBe('advanced manual.docx (de) from “Translation” to “Review” — “Please check terminology”')
  })
})

describe('what a TM/MT run used', () => {
  const run = { ...file, phase: 'TM + MT pre-translation' }

  it('a pre-translation names the memories and the MT engine, without counts', () => {
    expect(sentence('job.pipeline_completed', { ...run, tm_exact: 12, tm_fuzzy: 3, tm_names: ['Default TM'], mt_translated: 40, mt_engine: 'DeepL Pro' }))
      .toBe('finished TM + MT pre-translation for manual.docx (de) with Default TM and DeepL Pro')
    expect(sentence('job.pipeline_completed', { ...run, tm_names: ['Default TM', 'Client TM'], mt_engine: 'DeepL Pro' }))
      .toBe('finished TM + MT pre-translation for manual.docx (de) with Default TM, Client TM and DeepL Pro')
  })

  it('a skipped phase keeps its reason next to what ran', () => {
    expect(sentence('job.pipeline_completed', { ...run, tm_skipped: 'no_assignment', mt_translated: 1, mt_engine: 'DeepL Pro' }))
      .toBe('finished TM + MT pre-translation for manual.docx (de) with DeepL Pro (TM skipped: no TM for this language)')
  })

  it('Apply TM and Apply MT name what they used', () => {
    expect(sentence('job.tm_apply_started', { ...file, tm_exact: 0, tm_fuzzy: 2, tm_names: ['Default TM'] }))
      .toBe('ran Apply TM on manual.docx (de) with Default TM')
    expect(sentence('job.mt_apply_started', { ...file, mt_translated: 7, mt_engine: 'Acme MT' }))
      .toBe('ran Apply MT on manual.docx (de) with Acme MT')
  })

  it('a TM run that matched nothing says so', () => {
    expect(sentence('job.tm_apply_started', { ...file, tm_exact: 0, tm_fuzzy: 0, tm_names: [] }))
      .toBe('ran Apply TM on manual.docx (de) (no TM matches)')
  })

  it('names the person who triggered the run, or their email when the event has no name', () => {
    expect(sentence('job.pipeline_completed', { ...run, mt_engine: 'DeepL Pro', triggered_by_name: 'Olivia Owner', triggered_by_email: 'olivia@x.test' }))
      .toBe('finished TM + MT pre-translation for manual.docx (de) with DeepL Pro (triggered by Olivia Owner)')
    expect(sentence('job.pipeline_completed', { ...run, triggered_by_email: 'olivia@x.test' }))
      .toBe('finished TM + MT pre-translation for manual.docx (de) (triggered by olivia@x.test)')
  })

  it('an event with no memory or engine names reads as a plain sentence', () => {
    expect(sentence('job.tm_apply_started', file)).toBe('ran Apply TM on manual.docx (de)')
    expect(sentence('job.pipeline_completed', run)).toBe('finished TM + MT pre-translation for manual.docx (de)')
  })
})

describe('job.assigned', () => {
  it('names the assignee, or their email when the event has no name', () => {
    expect(sentence('job.assigned', { ...file, assignee_id: 7, assignee_name: 'Jane Linguist', assignee_email: 'jane@x.test' }))
      .toBe('assigned manual.docx (de) to Jane Linguist')
    expect(sentence('job.assigned', { ...file, assignee_id: 7, assignee_email: 'jane@x.test' }))
      .toBe('assigned manual.docx (de) to jane@x.test')
  })
})
