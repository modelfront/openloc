// A file's readiness is derived from its jobs, one per target language. The
// Files tab only renders these summaries, so the rules are pinned here.

import { describe, it, expect } from 'vitest'
import {
  FILE_READINESS_DISPLAY,
  fileMatchesFilters,
  jobReadiness,
  parseFileFilters,
  summarizeFiles,
} from './file-readiness'
import { TASK_STATE_DISPLAY } from './task-matrix'
import { makeJob } from '@/test/fixtures/job'
import type { Job } from '../api'

const step = (over: Partial<NonNullable<Job['workflow_step']>> = {}): NonNullable<Job['workflow_step']> => ({
  id: 1, step_name: 'review', step_order: 1, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null, ...over,
})

describe('jobReadiness', () => {
  it('reads a failed pipeline as failed, before anything else', () => {
    expect(jobReadiness(makeJob({ pipeline_status: 'failed' }))).toBe('failed')
  })

  it('reads a running pipeline as processing, but not one waiting on a person', () => {
    expect(jobReadiness(makeJob({ pipeline_status: 'in_progress', current_step: 'Translating 3 / 10' }))).toBe('processing')
    expect(jobReadiness(makeJob({ pipeline_status: 'in_progress', current_step: 'Pipeline complete' }))).toBe('in_progress')
  })

  it('is ready once the workflow is done: job completed, or the last step completed', () => {
    expect(jobReadiness(makeJob({ pipeline_status: 'completed' }))).toBe('ready')
    expect(jobReadiness(makeJob({ workflow_step: step({ is_last_step: true, status: 'completed' }) }))).toBe('ready')
    expect(jobReadiness(makeJob({ workflow_step: step({ is_last_step: true, status: 'in_progress' }) }))).toBe('in_progress')
  })

  it('reads a stopped run as stopped, until the workflow finishes anyway', () => {
    expect(jobReadiness(makeJob({ pipeline_status: 'stopped', workflow_step: step() }))).toBe('stopped')
    expect(jobReadiness(makeJob({ pipeline_status: 'stopped', workflow_step: step({ is_last_step: true, status: 'completed' }) }))).toBe('ready')
  })

  it('reads a finished job as ready even when its MT found no engine', () => {
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_route', pipeline_status: 'completed' }))).toBe('ready')
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_route', workflow_step: step({ is_last_step: true, status: 'completed' }) }))).toBe('ready')
  })

  it('reads a job whose MT found no engine as no_mt, below failed and processing', () => {
    const open = step({ step_name: 'Pre-translation', step_order: 0, is_last_step: true })
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_route', workflow_step: open }))).toBe('no_mt')
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_provider', workflow_step: open }))).toBe('no_mt')
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_route', pipeline_status: 'failed' }))).toBe('failed')
    expect(jobReadiness(makeJob({ mt_skip_reason: 'no_route', pipeline_status: 'in_progress', current_step: 'Translating 3 / 10' }))).toBe('processing')
  })

  it('shows stopped in the colour of a task that has not started', () => {
    expect(FILE_READINESS_DISPLAY.stopped.color).toBe(TASK_STATE_DISPLAY.not_started.color)
  })
})

describe('summarizeFiles', () => {
  const jobs = [
    makeJob({ id: 1, file_id: 10, file_name: 'guide.docx', target_language: 'fr', pipeline_status: 'completed' }),
    makeJob({ id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'de', workflow_step: step() }),
    makeJob({ id: 3, file_id: 10, file_name: 'guide.docx', target_language: 'es', workflow_step: step() }),
    makeJob({ id: 4, file_id: 20, file_name: 'api.md', target_language: 'de', pipeline_status: 'completed' }),
    makeJob({ id: 5, file_id: 30, file_name: 'broken.json', target_language: 'de', pipeline_status: 'failed' }),
    makeJob({ id: 6, file_id: 30, file_name: 'broken.json', target_language: 'fr', pipeline_status: 'in_progress', current_step: 'Translating 1 / 9' }),
  ]
  const [guide, api, broken] = summarizeFiles(jobs)

  it('makes one summary per file, keeping the order the jobs came in', () => {
    expect([guide.name, api.name, broken.name]).toEqual(['guide.docx', 'api.md', 'broken.json'])
  })

  it('counts ready languages and lists each state\'s codes', () => {
    expect(guide).toMatchObject({ readiness: 'in_progress', readyCount: 1, languageCount: 3 })
    expect(guide.languages).toEqual({ processing: [], in_progress: ['de', 'es'], no_mt: [], stopped: [], ready: ['fr'], failed: [] })
  })

  it('is ready only when every language is', () => {
    expect(api.readiness).toBe('ready')
  })

  it('lets a failure outrank processing', () => {
    expect(broken.readiness).toBe('failed')
  })

  it('reads a file with a stopped language as stopped, below failed and processing', () => {
    const [paused] = summarizeFiles([
      makeJob({ id: 7, file_id: 40, file_name: 'paused.xliff', target_language: 'de', pipeline_status: 'stopped', workflow_step: step() }),
      makeJob({ id: 8, file_id: 40, file_name: 'paused.xliff', target_language: 'fr', workflow_step: step() }),
      makeJob({ id: 9, file_id: 40, file_name: 'paused.xliff', target_language: 'es', pipeline_status: 'completed' }),
    ])
    expect(paused).toMatchObject({ readiness: 'stopped', readyCount: 1 })
    expect(paused.languages.stopped).toEqual(['de'])
    expect(fileMatchesFilters(paused, parseFileFilters(new URLSearchParams({ file_status: 'stopped' })))).toBe(true)
  })

  it('reads a file with a language that got no MT as no_mt, below failed and processing', () => {
    const [partial] = summarizeFiles([
      makeJob({ id: 10, file_id: 50, file_name: 'faq.json', target_language: 'es', pipeline_status: 'completed' }),
      makeJob({ id: 11, file_id: 50, file_name: 'faq.json', target_language: 'de', mt_skip_reason: 'no_route', workflow_step: step() }),
    ])
    expect(partial).toMatchObject({ readiness: 'no_mt', readyCount: 1, languageCount: 2 })
    expect(partial.languages.no_mt).toEqual(['de'])
    expect(fileMatchesFilters(partial, parseFileFilters(new URLSearchParams({ file_status: 'no_mt' })))).toBe(true)
  })

  it('filters by name and by readiness, both from the URL', () => {
    const f = parseFileFilters(new URLSearchParams({ q: ' GUIDE ', file_status: 'in_progress' }))
    expect(f).toEqual({ q: 'GUIDE', readiness: 'in_progress' })
    expect([guide, api, broken].filter(x => fileMatchesFilters(x, f)).map(x => x.name)).toEqual(['guide.docx'])
    expect(parseFileFilters(new URLSearchParams({ file_status: 'nope' }))).toEqual({})
  })
})
