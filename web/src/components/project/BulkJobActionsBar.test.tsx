// Stop and Re-run in the bulk bar are the project page's controls for a
// job's automation (rows carry no per-job buttons). This file pins:
//   - Stop only appears when something in the selection is running, acts on
//     the running jobs only, and never stops a job twice
//   - Re-run only appears when a selected task is in error, re-runs each such
//     job once however many of its tasks are selected, and leaves an AI-step
//     error to managers
//   - Skip AI step only appears for a manager whose selection holds a hybrid
//     task whose AI run failed, and skips only those jobs
//   - Import XLIFF shows for every role, takes exactly one task, and imports
//     the picked file into that task's file and language
//
// Eligibility itself (canStopJob, canRerunTask) is unit-tested in
// lib/project-logic.test.ts and lib/current-task.test.ts; here we check the bar
// wires those filters to the buttons and the api calls.
//
// Anti-tautology RED proofs:
//   - Drop the `stopEligible.length > 0` gate → "hidden when nothing runs" fails.
//   - Replace `selectedJobs.filter(canStopJob)` with `selectedJobs` → "stops
//     only the running jobs" fails (idle + stopping ids get sent too).
//   - Drop the `t.state === 'error'` filter → "only error cells" fails.
//   - Drop the Map de-duplication → "once per job" fails (job 1 re-run twice).
//   - Drop the canRerunTask filter → "AI-step error hidden for a linguist" fails.
//   - Drop the canSkipAIStep filter → "skips only hybrid AI-step errors" fails
//     (the AI-only and pre-translation jobs get skipped too).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { BulkJobActionsBar, type StepTarget } from './BulkJobActionsBar'
import type { Job } from '../../api'
import type { TaskState } from '../../lib/task-matrix'
import { makeJob } from '@/test/fixtures/job'
import { menuItem } from '@/test/menu'
import { toast } from 'sonner'

const mockStopJob = vi.fn()
const mockRerunJob = vi.fn()
const mockSkipJobAIStep = vi.fn()
const mockClearJob = vi.fn()
const mockApplyMT = vi.fn()

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>()
  return {
    ...actual,
    clearJobTranslations: (...args: unknown[]) => mockClearJob(...args),
    applyJobMT: (...args: unknown[]) => mockApplyMT(...args),
    stopJob: (...args: unknown[]) => mockStopJob(...args),
    rerunJob: (...args: unknown[]) => mockRerunJob(...args),
    skipJobAIStep: (...args: unknown[]) => mockSkipJobAIStep(...args),
    listMembers: vi.fn().mockResolvedValue([]),
  }
})

const mockReimportXLIFF = vi.fn()
vi.mock('@/api/files', () => ({ reimportXLIFF: (...args: unknown[]) => mockReimportXLIFF(...args) }))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

beforeEach(() => {
  mockStopJob.mockReset().mockResolvedValue({ status: 'stopping', job_id: 0 })
  mockRerunJob.mockReset().mockResolvedValue({ status: 'queued', phase: 'ai_step' })
  mockSkipJobAIStep.mockReset().mockResolvedValue(undefined)
})
afterEach(cleanup)


const idle = (id: number) => makeJob({ id })
const running = (id: number) =>
  makeJob({ id, pipeline_status: 'in_progress', current_step: 'Translating 5 / 10' })
const stopping = (id: number) =>
  makeJob({ id, pipeline_status: 'in_progress', current_step: 'Translating 5 / 10', stop_requested_at: '2026-09-07T10:00:00Z' })

// One task per job, on its active step.
const activeTask = (j: Job, state: TaskState = 'in_progress'): StepTarget =>
  ({ job: j, assignmentId: 900 + j.id, status: 'in_progress', stepType: 'human', state })

function renderBar(jobs: Job[], targets: StepTarget[] = jobs.map(j => activeTask(j)), isManager = false) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <BulkJobActionsBar
        projectId={42}
        selectedJobs={jobs}
        stepTargets={targets}
        isManager={isManager}
        onClear={() => {}}
      />
    </QueryClientProvider>,
  )
}

describe('BulkJobActionsBar — Stop', () => {
  it('is hidden when nothing in the selection is running', () => {
    renderBar([idle(1), idle(2), makeJob({ id: 3, pipeline_status: 'stopped', current_step: 'Stopped — 5 / 10' })])
    expect(screen.queryByTestId('bulk-stop-btn')).toBeNull()
  })

  it('is hidden when every running job already has a stop pending', () => {
    renderBar([stopping(1), idle(2)])
    expect(screen.queryByTestId('bulk-stop-btn')).toBeNull()
  })

  it('stops only the running jobs, skipping idle and already-stopping ones', async () => {
    renderBar([running(1), idle(2), stopping(3), running(4)])
    const btn = screen.getByTestId('bulk-stop-btn')
    expect(btn).toBeEnabled()

    await userEvent.click(btn)

    await waitFor(() => expect(mockStopJob).toHaveBeenCalledTimes(2))
    const ids = mockStopJob.mock.calls.map(([id]) => id).sort()
    expect(ids).toEqual([1, 4])
  })
})

describe('BulkJobActionsBar — Re-run', () => {
  const aiFailed = (id: number) =>
    makeJob({ id, pipeline_status: 'failed', current_step: 'Running AI step', failed_phase: 'ai_step' })
  const preFailed = (id: number) =>
    makeJob({ id, pipeline_status: 'failed', current_step: 'MT: 3 / 9 segments', failed_phase: 'pretranslation' })

  it('is hidden when no selected task is in error', () => {
    renderBar([idle(1), running(2)])
    expect(screen.queryByTestId('bulk-rerun-btn')).toBeNull()
  })

  it('counts only error cells: a failed job selected through a task that is not its current one is skipped', async () => {
    const a = preFailed(1)
    const b = preFailed(2)
    // Job 2's selected task is a later step that has not started, not the errored one.
    renderBar([a, b], [activeTask(a, 'error'), { ...activeTask(b, 'not_started'), assignmentId: 950 }])

    await userEvent.click(screen.getByTestId('bulk-rerun-btn'))

    await waitFor(() => expect(mockRerunJob).toHaveBeenCalledTimes(1))
    expect(mockRerunJob).toHaveBeenCalledWith(1)
  })

  it('re-runs each job once, however many of its tasks are selected', async () => {
    const a = aiFailed(1)
    const b = aiFailed(2)
    renderBar([a, b], [activeTask(a, 'error'), { ...activeTask(a, 'error'), assignmentId: 951 }, activeTask(b, 'error')], true)

    await userEvent.click(screen.getByTestId('bulk-rerun-btn'))

    await waitFor(() => expect(mockRerunJob).toHaveBeenCalledTimes(2))
    expect(mockRerunJob.mock.calls.map(([id]) => id).sort()).toEqual([1, 2])
    // A re-run is only scheduled: the summary says queued, not done.
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Re-run: 2 job(s) queued.'))
  })

  it('leaves an AI-step error to managers: a linguist re-runs only the pre-translation error', async () => {
    const a = aiFailed(1)
    const b = preFailed(2)
    renderBar([a, b], [activeTask(a, 'error'), activeTask(b, 'error')], false)

    await userEvent.click(screen.getByTestId('bulk-rerun-btn'))

    await waitFor(() => expect(mockRerunJob).toHaveBeenCalledTimes(1))
    expect(mockRerunJob).toHaveBeenCalledWith(2)
  })

  it('is hidden for a non-manager whose only errors are AI-step errors', () => {
    const a = aiFailed(1)
    renderBar([a], [activeTask(a, 'error')], false)
    expect(screen.queryByTestId('bulk-rerun-btn')).toBeNull()
  })
})

describe('BulkJobActionsBar — Skip AI step', () => {
  const aiFailed = (id: number) =>
    makeJob({ id, pipeline_status: 'failed', current_step: 'AI step (QE+APE): 3 / 9', failed_phase: 'ai_step' })
  const preFailed = (id: number) =>
    makeJob({ id, pipeline_status: 'failed', current_step: 'MT: 3 / 9 segments', failed_phase: 'pretranslation' })
  const hybridTask = (j: Job): StepTarget => ({ ...activeTask(j, 'error'), stepType: 'hybrid' })
  const aiTask = (j: Job): StepTarget => ({ ...activeTask(j, 'error'), stepType: 'ai' })

  it('skips only hybrid AI-step errors, each job once', async () => {
    const hybrid = aiFailed(1)
    const aiOnly = aiFailed(2)
    const pre = preFailed(3)
    renderBar([hybrid, aiOnly, pre], [hybridTask(hybrid), hybridTask(hybrid), aiTask(aiOnly), hybridTask(pre)], true)

    await userEvent.click(screen.getByTestId('bulk-skip-ai-btn'))

    await waitFor(() => expect(mockSkipJobAIStep).toHaveBeenCalledTimes(1))
    expect(mockSkipJobAIStep).toHaveBeenCalledWith(1)
  })

  it('is hidden for a linguist', () => {
    const a = aiFailed(1)
    renderBar([a], [hybridTask(a)], false)
    expect(screen.queryByTestId('bulk-skip-ai-btn')).toBeNull()
  })

  it('is hidden when the failed AI step is AI-only', () => {
    const a = aiFailed(1)
    renderBar([a], [aiTask(a)], true)
    expect(screen.queryByTestId('bulk-skip-ai-btn')).toBeNull()
  })
})

describe('BulkJobActionsBar — Import XLIFF', () => {
  it('is enabled for a non-manager', () => {
    renderBar([idle(1)], undefined, false)
    expect(screen.getByTestId('bulk-reimport-btn')).toBeEnabled()
  })

  it("imports the picked file into the one selected task's file and language", async () => {
    mockReimportXLIFF.mockResolvedValueOnce({ applied: true, updated: 1 })
    renderBar([makeJob({ id: 1, file_id: 77, target_language: 'es' })], undefined, true)

    const input = screen.getByTestId('import-xliff-input')
    const click = vi.spyOn(input, 'click')
    await userEvent.setup().click(screen.getByTestId('bulk-reimport-btn'))
    expect(click).toHaveBeenCalled()

    fireEvent.change(input, { target: { files: [new File(['<xliff/>'], 'guide_es.xlf')] } })
    await waitFor(() => expect(mockReimportXLIFF).toHaveBeenCalledWith(77, expect.any(File), { lang: 'es', force: false }))
  })

  it('is disabled when more than one task is selected', () => {
    renderBar([idle(1), idle(2)], undefined, true)
    expect(screen.getByTestId('bulk-reimport-btn')).toBeDisabled()
  })
})

describe('BulkJobActionsBar — Clear', () => {
  it('says how many segments it kept, across the jobs, because someone else has them open', async () => {
    mockClearJob
      .mockResolvedValueOnce({ cleared: 4, held: 1 })
      .mockResolvedValueOnce({ cleared: 2, held: 2 })
    renderBar([makeJob({ id: 1 }), makeJob({ id: 2 })], undefined, true)

    const user = userEvent.setup()
    await user.click(screen.getByTestId('bulk-clear-btn'))
    await user.click(await screen.findByTestId('clear-submit'))

    await waitFor(() => expect(mockClearJob).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('3 segments were skipped because someone else is editing them.'))
  })
})

describe('BulkJobActionsBar — Pre-translate', () => {
  it('says how many segments Apply MT skipped, across the jobs, because someone else has them open', async () => {
    mockApplyMT
      .mockResolvedValueOnce({ message: 'ok', job_id: 1, held: 1 })
      .mockResolvedValueOnce({ message: 'ok', job_id: 2 })
    renderBar([makeJob({ id: 1 }), makeJob({ id: 2 })], undefined, true)

    const user = userEvent.setup()
    await user.click(screen.getByTestId('bulk-pretranslate-btn'))
    await user.click(await menuItem('bulk-apply-mt-btn'))

    await waitFor(() => expect(mockApplyMT).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith('1 segment was skipped because someone else is editing it.'))
  })
})
