import { describe, it, expect } from 'vitest'
import {
  isPipelineRunning,
  isAIStepRunning,
  pipelineBadge,
  jobInFirstStep,
  canApplyTMorMT,
  canClearJob,
  canCompleteJob,
  canStopJob,
  isStopRequested,
  stoppedNote,
} from './project-logic'

// pipeline_status='in_progress' covers both "workers running" and
// "workers done, awaiting human Complete". The helper distinguishes them via
// current_step so polling/badges/refetch triggers don't fire forever.
describe('isPipelineRunning', () => {
  it('true while a phase is mid-flight (current_step shows progress)', () => {
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'MT: 5 / 100' })).toBe(true)
    expect(isPipelineRunning({ pipeline_status: 'queued', current_step: undefined })).toBe(true)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'TM lookup: 22 matches' })).toBe(true)
  })

  it('false once any phase wrote a "* complete" current_step (pipeline done, awaiting Complete)', () => {
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'Translation complete' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'Predict complete' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'Pipeline complete' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'tm-apply complete' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'mt-apply complete' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'predict complete' })).toBe(false)
  })

  it('false for terminal states (completed / failed / created / stopped)', () => {
    expect(isPipelineRunning({ pipeline_status: 'completed', current_step: 'completed' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'failed', current_step: 'error' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'created', current_step: undefined })).toBe(false)
    // 'stopped' is terminal even though its current_step does not end in
    // "complete" — polling and the running badge must go quiet on it.
    expect(isPipelineRunning({
      pipeline_status: 'stopped',
      current_step: 'Stopped — 1,230 / 22,857 segments translated; run Pre-translate again to resume',
    })).toBe(false)
  })

  it('case-insensitive match on "complete"', () => {
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'TRANSLATION COMPLETE' })).toBe(false)
    expect(isPipelineRunning({ pipeline_status: 'in_progress', current_step: 'pipeline Complete' })).toBe(false)
  })
})

describe('isAIStepRunning (editor lock)', () => {
  it('true only while a QE+APE step is running', () => {
    expect(isAIStepRunning({ pipeline_status: 'queued', current_step: 'Queued for QE+APE' })).toBe(true)
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'Running QE+APE' })).toBe(true)
  })

  // The label is stored on the job row, so a job enqueued under the older
  // "QE/APE" spelling is still in flight after the deploy that renamed it. The
  // lock has to hold for those rows too.
  //
  // Anti-tautology RED proof: narrow the matcher to /QE\+APE/ → this fails.
  it('true for the legacy "QE/APE" spelling on an in-flight job', () => {
    expect(isAIStepRunning({ pipeline_status: 'queued', current_step: 'Queued for QE/APE' })).toBe(true)
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'Running QE/APE' })).toBe(true)
  })

  // The label names the mode that is actually running, so the lock must not
  // key on any one mode's name. A QE-only step that stopped locking would let
  // people edit rows the step is still writing.
  it('locks on every mode, not just QE+APE', () => {
    for (const step of [
      'Queued for AI step',
      'Running AI step',
      'AI step (QE): 20 / 100',
      'AI step (APE): 20 / 100',
      'AI step (QE+APE): 20 / 100',
    ]) {
      expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: step })).toBe(true)
    }
  })

  it('false when QE+APE has finished (label ends in "complete")', () => {
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'QE+APE complete' })).toBe(false)
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'AI step complete' })).toBe(false)
  })

  it('false for a running MT/pre-translation step — QE+APE only', () => {
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'Running MT' })).toBe(false)
    expect(isAIStepRunning({ pipeline_status: 'queued', current_step: 'Queued for pre-translation' })).toBe(false)
    expect(isAIStepRunning({ pipeline_status: 'in_progress', current_step: 'MT: 5 / 100' })).toBe(false)
  })

  it('false when idle / no step label', () => {
    expect(isAIStepRunning({ pipeline_status: 'created', current_step: undefined })).toBe(false)
    expect(isAIStepRunning({ pipeline_status: 'completed', current_step: 'completed' })).toBe(false)
  })
})

describe('pipelineBadge', () => {
  it('null when nothing is running', () => {
    expect(pipelineBadge({ pipeline_status: 'in_progress', current_step: 'Translation complete' })).toBeNull()
    expect(pipelineBadge({ pipeline_status: 'created', current_step: undefined })).toBeNull()
  })

  it('QE+APE → label is the current_step (blue + pulsing like every running badge)', () => {
    expect(pipelineBadge({ pipeline_status: 'queued', current_step: 'Queued for QE+APE' })).toEqual({
      label: 'Queued for QE+APE', color: 'blue', pulse: true,
    })
    expect(pipelineBadge({ pipeline_status: 'in_progress', current_step: 'Running QE+APE' })).toEqual({
      label: 'Running QE+APE', color: 'blue', pulse: true,
    })
  })

  it('MT with counts → blue "Translating N / M"', () => {
    expect(pipelineBadge({
      pipeline_status: 'in_progress', current_step: 'Running MT',
      translated_count: 5, total_count: 100,
    })).toEqual({ label: 'Translating 5 / 100', color: 'blue', pulse: true })
  })

  it('MT without counts → blue, falls back to current_step', () => {
    expect(pipelineBadge({ pipeline_status: 'queued', current_step: 'Queued for pre-translation' })).toEqual({
      label: 'Queued for pre-translation', color: 'blue', pulse: true,
    })
    expect(pipelineBadge({ pipeline_status: 'in_progress', current_step: undefined })).toEqual({
      label: 'Processing...', color: 'blue', pulse: true,
    })
  })

  it('pending stop → yellow "Stopping…", beating both the MT counter and the QE+APE label', () => {
    const stopping = { label: 'Stopping…', color: 'yellow', pulse: true }
    expect(pipelineBadge({
      pipeline_status: 'in_progress', current_step: 'Running MT',
      translated_count: 5, total_count: 100, stop_requested_at: '2026-09-07T10:00:00Z',
    })).toEqual(stopping)
    expect(pipelineBadge({
      pipeline_status: 'in_progress', current_step: 'Running QE+APE', stop_requested_at: '2026-09-07T10:00:00Z',
    })).toEqual(stopping)
  })

  it('a stale stop_requested_at on a non-running job renders no badge', () => {
    expect(pipelineBadge({ pipeline_status: 'stopped', current_step: 'Stopped — 5 / 100', stop_requested_at: '2026-09-07T10:00:00Z' })).toBeNull()
  })
})

describe('stop helpers', () => {
  const running = { pipeline_status: 'in_progress', current_step: 'Translating' }
  const at = '2026-09-07T10:00:00Z'

  describe('isStopRequested', () => {
    it('true only while running with stop_requested_at set', () => {
      expect(isStopRequested({ ...running, stop_requested_at: at })).toBe(true)
      expect(isStopRequested({ pipeline_status: 'queued', current_step: null, stop_requested_at: at })).toBe(true)
    })
    it('false while running without a pending stop (null or absent)', () => {
      expect(isStopRequested({ ...running })).toBe(false)
      expect(isStopRequested({ ...running, stop_requested_at: null })).toBe(false)
    })
    it('false once the run is no longer live, even if the flag lingers', () => {
      expect(isStopRequested({ pipeline_status: 'stopped', current_step: 'Stopped — 5 / 100', stop_requested_at: at })).toBe(false)
      expect(isStopRequested({ pipeline_status: 'in_progress', current_step: 'Translation complete', stop_requested_at: at })).toBe(false)
    })
  })

  describe('stoppedNote', () => {
    const note = 'Stopped — 1,230 / 22,857 segments translated; run Pre-translate again to resume'
    it('returns current_step for a stopped job', () => {
      expect(stoppedNote({ pipeline_status: 'stopped', current_step: note })).toBe(note)
    })
    it('null for every other status, and for a stopped job with no step text', () => {
      expect(stoppedNote({ pipeline_status: 'in_progress', current_step: note })).toBeNull()
      expect(stoppedNote({ pipeline_status: 'failed', current_step: 'error' })).toBeNull()
      expect(stoppedNote({ pipeline_status: 'stopped', current_step: null })).toBeNull()
      expect(stoppedNote({ pipeline_status: 'stopped' })).toBeNull()
    })
  })
})

describe('bulk job-action eligibility', () => {
  const idle = { pipeline_status: 'ready', current_step: null }
  const running = { pipeline_status: 'in_progress', current_step: 'Translating' }

  describe('jobInFirstStep', () => {
    it('true when no workflow step', () => {
      expect(jobInFirstStep({})).toBe(true)
      expect(jobInFirstStep({ workflow_step: null })).toBe(true)
    })
    it('true only when active step_order is 0', () => {
      expect(jobInFirstStep({ workflow_step: { step_order: 0 } })).toBe(true)
      expect(jobInFirstStep({ workflow_step: { step_order: 1 } })).toBe(false)
    })
  })

  describe('canApplyTMorMT', () => {
    it('eligible in first step and not running', () => {
      expect(canApplyTMorMT({ ...idle })).toBe(true)
      expect(canApplyTMorMT({ ...idle, workflow_step: { step_order: 0 } })).toBe(true)
    })
    it('blocked while pipeline runs', () => {
      expect(canApplyTMorMT({ ...running })).toBe(false)
    })
    it('blocked past the first step', () => {
      expect(canApplyTMorMT({ ...idle, workflow_step: { step_order: 2 } })).toBe(false)
    })
  })

  describe('canClearJob', () => {
    it('eligible in first step, not running, with clearable content', () => {
      expect(canClearJob({ ...idle, counts: { total: 10, untranslated: 3 } })).toBe(true)
    })
    it('blocked when nothing is clearable (all untranslated)', () => {
      expect(canClearJob({ ...idle, counts: { total: 10, untranslated: 10 } })).toBe(false)
    })
    it('blocked while running or past the first step', () => {
      expect(canClearJob({ ...running, counts: { total: 10, untranslated: 3 } })).toBe(false)
      expect(canClearJob({ ...idle, workflow_step: { step_order: 1 }, counts: { total: 10, untranslated: 3 } })).toBe(false)
    })
  })

  describe('canStopJob', () => {
    it('eligible while queued/running with no stop pending', () => {
      expect(canStopJob({ ...running })).toBe(true)
      expect(canStopJob({ ...running, stop_requested_at: null })).toBe(true)
      expect(canStopJob({ pipeline_status: 'queued', current_step: null })).toBe(true)
    })
    it('blocked once a stop is pending', () => {
      expect(canStopJob({ ...running, stop_requested_at: '2026-09-07T10:00:00Z' })).toBe(false)
    })
    it('blocked when nothing is running (idle, stopped, or phase already complete)', () => {
      expect(canStopJob({ ...idle })).toBe(false)
      expect(canStopJob({ pipeline_status: 'stopped', current_step: 'Stopped — 5 / 100' })).toBe(false)
      expect(canStopJob({ pipeline_status: 'in_progress', current_step: 'Translation complete' })).toBe(false)
    })
  })

  describe('canCompleteJob', () => {
    it('eligible with an active step, not completed, not running', () => {
      expect(canCompleteJob({ ...idle, workflow_step: { step_order: 0 } })).toBe(true)
    })
    it('blocked without an active workflow step', () => {
      expect(canCompleteJob({ ...idle })).toBe(false)
    })
    it('blocked once the job is completed or while running', () => {
      expect(canCompleteJob({ pipeline_status: 'completed', current_step: 'completed', workflow_step: { step_order: 0 } })).toBe(false)
      expect(canCompleteJob({ ...running, workflow_step: { step_order: 0 } })).toBe(false)
    })
  })
})
