// Gating + stopPropagation tests for JobPipelineActions. The component's
// only job is to decide which of the three buttons to render and to wrap
// click handlers so they don't bubble into an enclosing <Link>. Mutation
// behavior (which API gets called, which toast fires, which queries get
// invalidated) is owned by useJobPipelineActions and tested in
// useJobPipelineActions.test.tsx — splitting the layers keeps each test
// focused on one responsibility.
//
// What this file pins (and why each matters):
//   - "all-zero counts": Apply TM + Apply MT still render (always-available
//     per QA feedback 2026-05-19 — translator may clear a segment and want
//     to re-pretranslate).
//   - "not in first step": Apply TM/MT hidden. Workflow contract:
//     pre-translation belongs in step 0.
//   - "step_order derivation from job.workflow_step": this is the
//     project-row's gating path (no override). Without `step_order`
//     surfaced from the backend, the project page would have to either
//     always show or always hide the buttons. We derive from active
//     step.step_order; the test proves it works both directions.
//   - "Re-run AI step": manager-only, shown only when the active
//     step is a QE+APE step (workflow_step.is_ai_step). Not tied to
//     first-step gating — a QE+APE step can be any step in the workflow.
//   - "disabled while pipeline running": prevents stacking concurrent
//     applies against the same job.
//   - Stop: the one button live DURING a run. It renders alone when nothing
//     else qualifies, flips to a disabled "Stopping…" once the backend
//     reports stop_requested_at, and is absent while idle.
//   - In-progress affordances: the trigger carries a spinner (so progress is
//     visible with the menu closed) while the menu names WHICH action runs.
//     The trigger stays operable during our own apply — otherwise the menu
//     can't be reopened to read that — so the anti-stacking gate lives on the
//     menu items instead. That split is the load-bearing part: get it wrong in
//     one direction and progress is unreadable, in the other and concurrent
//     runs can stack.
//   - "stopPropagationOnClick suppresses parent Link click": the project
//     page row IS a <Link>, so without stopPropagation, clicking a
//     button would also navigate into the CAT editor — confusing.
//
// Anti-tautology RED proofs:
//   - Re-add `counts.untranslated > 0` gating to showApplyMT in the
//     component → "all-zero counts" test fails (MT button no longer
//     appears when nothing's untranslated).
//   - Drop the `is_ai_step` gate on showRunAIStep → "Re-run AI step hidden
//     on a plain step" test fails.
//   - Replace step_order=0 check with `true` → "later step" test fails
//     (buttons appear on step 1).
//   - Remove the e.preventDefault/stopPropagation wrap →
//     "stopPropagationOnClick" test fails (parent click fires).
//   - Point the trigger back at `disabled` instead of `triggerDisabled`, drop
//     `disabled` from the menu rows, or delete the button spinner → the
//     "while an apply is in flight" test fails on each (all three verified
//     RED individually).
//   - Wire Stop to the shared `disabled` → "Stop is enabled while running"
//     fails. Drop `showStop` from the early return → "Stop renders alone"
//     fails. Drop the stop_requested_at gate → "Stopping…" test fails.
//   - Drop `writable` from the Pre-translate / Clear / Re-run AI step gates →
//     the "failed job" and "canEdit=false" tests fail.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { JobPipelineActions } from './JobPipelineActions'
import { makeJob as makeJobFixture, makeJobCounts } from '@/test/fixtures/job'
import { queryKeys } from '../lib/query-keys'
import type { Job } from '../api'

// Mocked api so the rendered hook doesn't make real network calls. The
// component is supposed to be presentation-only; we don't assert on the
// mocks here (that's the hook test's job).
const mockGetMe = vi.fn().mockResolvedValue({ role: 'owner' })
// Module-scope so the in-flight tests below can hold a call open with
// mockImplementationOnce; the resolved-immediately default is restored in
// beforeEach.
const mockApplyTM = vi.fn()
const mockApplyMT = vi.fn()
const mockStopJob = vi.fn()
const mockAdvanceJobStep = vi.fn()
const mockCompleteJob = vi.fn()

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    applyJobTM: (...args: unknown[]) => mockApplyTM(...args),
    applyJobMT: (...args: unknown[]) => mockApplyMT(...args),
    runJobAIStep: vi.fn().mockResolvedValue({ message: 'ok' }),
    stopJob: (...args: unknown[]) => mockStopJob(...args),
    advanceJobStep: (...args: unknown[]) => mockAdvanceJobStep(...args),
    completeJob: (...args: unknown[]) => mockCompleteJob(...args),
    clearJobTranslations: vi.fn().mockResolvedValue({ cleared: 0 }),
    getMe: () => mockGetMe(),
  }
})

// This spec's job identity: the stop/apply assertions match on job id 7.
function makeJob(overrides: Partial<Job> = {}): Job {
  return makeJobFixture({ id: 7, file_id: 1, target_language: 'fr', project_id: 3, ...overrides })
}

function renderWithJob(job: Job, extra: Partial<React.ComponentProps<typeof JobPipelineActions>> = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={qc}>
      <JobPipelineActions job={job} projectId={42} {...extra} />
    </QueryClientProvider>,
  )
  return { ...view, qc }
}

// Re-run AI step and Clear both wait on the role query. A test that asserts
// one of them is absent has to wait for the role first, otherwise it passes
// before the role has arrived.
async function waitForRole(qc: QueryClient) {
  await waitFor(() => expect(qc.getQueryState(queryKeys.auth.me)?.status).toBe('success'))
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetMe.mockResolvedValue({ role: 'owner' })
  mockApplyTM.mockResolvedValue({ message: 'ok' })
  mockApplyMT.mockResolvedValue({ message: 'ok' })
  mockStopJob.mockResolvedValue({ status: 'stopping', job_id: 7 })
})

// Apply TM / Apply MT live inside the Pre-translate menu now.
// The menu is portalled to document.body, so items are
// only queryable via the global `screen` — never via a render's scoped
// queries. Tests that only care about show/hide assert on the trigger.
async function openPretranslate() {
  await userEvent.click(screen.getByTestId('pretranslate-btn'))
  return screen.findByTestId('apply-tm-btn')
}

describe('JobPipelineActions gating', () => {
  it('offers Apply TM + Apply MT even when all counts are zero (QA 2026-05-19: clear-then-reapply workflow)', async () => {
    renderWithJob(makeJob())
    expect(screen.queryByTestId('pretranslate-btn')).toBeTruthy()
    // No AI step attached → no Re-run button.
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()

    // Both actions are still reachable, one click deeper.
    await openPretranslate()
    expect(screen.queryByTestId('apply-mt-btn')).toBeTruthy()
  })

  it('offers TM + MT when there are untranslated segments', async () => {
    renderWithJob(makeJob({ counts: makeJobCounts({ untranslated: 5, total: 10 }) }))
    await openPretranslate()
    expect(screen.queryByTestId('apply-mt-btn')).toBeTruthy()
  })

  // Manager Re-run of a QE+APE step. Gated on workflow_step.is_ai_step
  // + manager role, independent of first-step gating.
  it('shows Re-run AI step for a manager on a QE+APE step (is_ai_step=true)', async () => {
    const { findByTestId } = renderWithJob(
      makeJob({
        workflow_step: { id: 1, step_name: 'QE', step_order: 1, status: 'in_progress', is_last_step: false, is_ai_step: true, deadline: null },
      }),
    )
    expect(await findByTestId('run-ai-step-btn')).toBeTruthy()
  })

  it('hides Re-run AI step on a plain (non-AI) step', async () => {
    const { qc } = renderWithJob(
      makeJob({
        workflow_step: { id: 1, step_name: 'Review', step_order: 1, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null },
      }),
    )
    await waitForRole(qc)
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()
  })

  it('hides Re-run AI step for non-managers even on a QE+APE step', async () => {
    mockGetMe.mockResolvedValueOnce({ role: 'member' })
    const { qc } = renderWithJob(
      makeJob({
        counts: makeJobCounts({ untranslated: 5, total: 10 }),
        workflow_step: { id: 1, step_name: 'QE', step_order: 0, status: 'in_progress', is_last_step: false, is_ai_step: true, deadline: null },
      }),
    )
    await waitForRole(qc)
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()
  })

  it('hides all buttons when not in first workflow step (inFirstStep=false override)', () => {
    const { container } = renderWithJob(
      makeJob({ counts: makeJobCounts({ untranslated: 5, pre_translated: 2, total: 10 }) }),
      { inFirstStep: false },
    )
    expect(container.firstChild).toBeNull()
  })

  it('derives inFirstStep from job.workflow_step.step_order (project-row path)', () => {
    // step_order = 0 → in first step → Apply TM/MT buttons render
    const firstStep = renderWithJob(
      makeJob({
        counts: makeJobCounts({ untranslated: 5, total: 10 }),
        workflow_step: { id: 1, step_name: 'translation', step_order: 0, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null },
      }),
    )
    expect(firstStep.queryByTestId('pretranslate-btn')).toBeTruthy()
    firstStep.unmount()

    // step_order = 1 → not first → Apply buttons hidden but Complete-Step
    // button still shows (every active workflow step exposes a
    // per-job advance control on the project row).
    const laterStep = renderWithJob(
      makeJob({
        counts: makeJobCounts({ untranslated: 5, total: 10 }),
        workflow_step: { id: 1, step_name: 'review', step_order: 1, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null },
      }),
    )
    expect(laterStep.queryByTestId('pretranslate-btn')).toBeNull()
    expect(laterStep.queryByTestId('run-ai-step-btn')).toBeNull()
    expect(laterStep.queryByTestId('complete-step-btn')).toBeTruthy()
  })

  it('final-step dialog says the job completes and approved segments go to TM', async () => {
    const { getByTestId, findByRole } = renderWithJob(
      makeJob({
        counts: makeJobCounts({ approved: 9, total: 10 }),
        workflow_step: { id: 1, step_name: 'review', step_order: 1, status: 'in_progress', is_last_step: true, is_ai_step: false, deadline: null },
      }),
    )
    getByTestId('complete-step-btn').click()
    const dialog = await findByRole('alertdialog')
    expect(dialog.textContent).toContain('as completed and save 9 human-approved segments to Translation memory')
  })

  it('disables all visible buttons while pipeline_status is in_progress', () => {
    renderWithJob(
      makeJob({
        counts: makeJobCounts({ untranslated: 5, pre_translated: 2, total: 10 }),
        pipeline_status: 'in_progress',
      }),
    )
    // A pipeline run we didn't start disables the trigger outright, and an
    // unopenable menu means neither action can fire. (An apply of our own is
    // the one exception — see the in-flight test above.)
    expect(screen.getByTestId('pretranslate-btn')).toBeDisabled()
  })

  it('Stop is enabled while running, and every other visible button is disabled', async () => {
    renderWithJob(
      makeJob({
        counts: makeJobCounts({ untranslated: 5, pre_translated: 2, total: 10 }),
        pipeline_status: 'in_progress',
        current_step: 'Translating 5 / 10',
        workflow_step: { id: 1, step_name: 'translation', step_order: 0, status: 'in_progress', is_last_step: true, is_ai_step: false, deadline: null },
      }),
    )
    const stopBtn = screen.getByTestId('stop-job-btn')
    expect(stopBtn).toBeEnabled()
    expect(stopBtn).toHaveTextContent('Stop')
    expect(screen.queryByTestId('stop-job-spinner')).toBeNull()
    expect(screen.getByTestId('pretranslate-btn')).toBeDisabled()
    // Clear waits on the getMe role query, so it appears a tick later.
    expect(await screen.findByTestId('clear-translations-btn')).toBeDisabled()

    await userEvent.click(stopBtn)
    await waitFor(() => expect(mockStopJob).toHaveBeenCalledExactlyOnceWith(7))
  })

  it('reads a disabled "Stopping…" with a spinner once stop_requested_at is set', () => {
    renderWithJob(
      makeJob({
        pipeline_status: 'in_progress',
        current_step: 'Translating 5 / 10',
        stop_requested_at: '2026-09-07T10:00:00Z',
      }),
    )
    const stopBtn = screen.getByTestId('stop-job-btn')
    expect(stopBtn).toBeDisabled()
    expect(stopBtn).toHaveTextContent('Stopping…')
    expect(screen.getByTestId('stop-job-spinner')).toBeTruthy()
  })

  it('renders Stop alone when a run is live and no other action qualifies (later step, non-manager)', async () => {
    mockGetMe.mockResolvedValueOnce({ role: 'linguist' })
    const { container } = renderWithJob(
      makeJob({
        pipeline_status: 'in_progress',
        current_step: 'Translating 5 / 10',
        workflow_step: { id: 2, step_name: 'review', step_order: 1, status: 'in_progress', is_last_step: true, is_ai_step: false, deadline: null },
      }),
      { hideComplete: true },
    )
    await waitFor(() => expect(screen.getByTestId('stop-job-btn')).toBeEnabled())
    expect(screen.queryByTestId('pretranslate-btn')).toBeNull()
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()
    expect(screen.queryByTestId('clear-translations-btn')).toBeNull()
    expect(screen.queryByTestId('complete-step-btn')).toBeNull()
    expect(container.querySelectorAll('button')).toHaveLength(1)
  })

  it('hides Stop while idle and once the run has stopped', () => {
    renderWithJob(makeJob({ counts: makeJobCounts({ untranslated: 5, total: 10 }) }))
    expect(screen.queryByTestId('stop-job-btn')).toBeNull()
    cleanup()
    renderWithJob(makeJob({ pipeline_status: 'stopped', current_step: 'Stopped — 5 / 10 segments translated; run Pre-translate again to resume' }))
    expect(screen.queryByTestId('stop-job-btn')).toBeNull()
    expect(screen.getByTestId('pretranslate-btn')).toBeEnabled()
  })

  it('shows no in-progress affordances while idle', async () => {
    renderWithJob(makeJob({ counts: makeJobCounts({ untranslated: 5, total: 10 }) }))
    expect(screen.queryByTestId('pretranslate-spinner')).toBeNull()
    await openPretranslate()
    expect(screen.queryByText('Running…')).toBeNull()
    expect(screen.getByTestId('apply-tm-btn')).not.toHaveAttribute('data-disabled')
  })

  it('while an apply is in flight: button spins, the menu reopens and names the running action', async () => {
    // Hold the MT call open so the in-flight state is observable.
    let resolveMT: (v: { message: string }) => void = () => {}
    mockApplyMT.mockImplementationOnce(() => new Promise(res => { resolveMT = res }))

    renderWithJob(makeJob({ counts: makeJobCounts({ untranslated: 5, total: 10 }) }))
    await openPretranslate()
    await userEvent.click(screen.getByTestId('apply-mt-btn'))

    // (1) The spinner rides inside the trigger, so progress is visible with
    //     the menu closed.
    await waitFor(() => expect(screen.getByTestId('pretranslate-spinner')).toBeTruthy())
    // The label is deliberately unchanged — which action it is lives in the menu.
    expect(screen.getByTestId('pretranslate-btn')).toHaveTextContent('Pre-translate')

    // (2) The trigger must stay operable during OUR OWN apply, or the menu
    //     can't be reopened and (3) is unreachable.
    expect(screen.getByTestId('pretranslate-btn')).toBeEnabled()
    await userEvent.click(screen.getByTestId('pretranslate-btn'))

    // (3) The reopened menu names the running action — on the MT row only.
    const mtRow = await screen.findByTestId('apply-mt-btn')
    expect(within(mtRow).getByText('Running…')).toBeTruthy()
    expect(within(screen.getByTestId('apply-tm-btn')).queryByText('Running…')).toBeNull()

    // (4) ...and both rows are disabled, so an open menu can't stack a second
    //     concurrent run onto the same job.
    expect(mtRow).toHaveAttribute('data-disabled')
    expect(screen.getByTestId('apply-tm-btn')).toHaveAttribute('data-disabled')

    resolveMT({ message: 'ok' })
    await waitFor(() => expect(screen.queryByTestId('pretranslate-spinner')).toBeNull())
  })

  it('stopPropagationOnClick prevents click from bubbling to parent Link', () => {
    const parentClick = vi.fn()
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={qc}>
        <div onClick={parentClick} data-testid="parent">
          <JobPipelineActions
            job={makeJob({ counts: makeJobCounts({ untranslated: 1, total: 1 }) })}
            projectId={1}
            stopPropagationOnClick
          />
        </div>
      </QueryClientProvider>,
    )
    // Use the native DOM `.click()` method: it dispatches a real click
    // event that bubbles through the React tree the same way a user
    // click does, so the stopPropagation behavior is exercised
    // end-to-end. (fireEvent.click on a base-ui Trigger-rendered button
    // skips the React onClick chain — known infrastructure quirk.)
    // The Pre-translate TRIGGER is the element sitting inside the parent
    // Link — the menu items are portalled out of it, so the trigger is what
    // must not bubble.
    screen.getByTestId('pretranslate-btn').click()
    expect(parentClick).not.toHaveBeenCalled()
  })

  it('shows Clear button when admin AND something is clearable (total > untranslated)', async () => {
    const { findByTestId } = renderWithJob(
      makeJob({ counts: makeJobCounts({ untranslated: 2, pre_translated: 3, total: 5 }) }),
    )
    expect(await findByTestId('clear-translations-btn')).toBeTruthy()
  })

  it('hides Clear when the only state is fully untranslated (nothing to clear)', async () => {
    const { qc } = renderWithJob(makeJob({ counts: makeJobCounts({ untranslated: 5, total: 5 }) }))
    await waitForRole(qc)
    expect(screen.queryByTestId('clear-translations-btn')).toBeNull()
  })

  it('hides Clear for non-admin members even when clearable rows exist', async () => {
    mockGetMe.mockResolvedValueOnce({ role: 'member' })
    const { qc } = renderWithJob(
      makeJob({ counts: makeJobCounts({ untranslated: 2, pre_translated: 3, total: 5 }) }),
    )
    await waitForRole(qc)
    expect(screen.queryByTestId('clear-translations-btn')).toBeNull()
  })

  it('without stopPropagationOnClick, click bubbles to parent (default for CAT editor toolbar)', () => {
    const parentClick = vi.fn()
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={qc}>
        <div onClick={parentClick} data-testid="parent">
          <JobPipelineActions
            job={makeJob({ counts: makeJobCounts({ untranslated: 1, total: 1 }) })}
            projectId={1}
          />
        </div>
      </QueryClientProvider>,
    )
    screen.getByTestId('pretranslate-btn').click()
    expect(parentClick).toHaveBeenCalled()
  })
})

// The writes this group offers (Apply TM/MT, Re-run AI step, Clear) are hidden
// whenever the current task cannot be edited, and on a failed job, where the
// task controls own Re-run.
describe('JobPipelineActions — task state', () => {
  const aiStep = { id: 1, step_name: 'QE', step_order: 0, status: 'in_progress', is_last_step: false, is_ai_step: true, deadline: null }
  const clearable = makeJobCounts({ total: 10, untranslated: 2, clearable_all: 8, clearable_non_confirmed: 8 })

  it('control: an idle first-step AI job offers Pre-translate, Re-run AI step and Clear to a manager', async () => {
    renderWithJob(makeJob({ workflow_step: aiStep, counts: clearable }))
    expect(await screen.findByTestId('run-ai-step-btn')).toBeInTheDocument()
    expect(screen.getByTestId('pretranslate-btn')).toBeInTheDocument()
    expect(screen.getByTestId('clear-translations-btn')).toBeInTheDocument()
  })

  it('a failed job offers none of them', async () => {
    const { qc } = renderWithJob(makeJob({
      workflow_step: aiStep, counts: clearable,
      pipeline_status: 'failed', current_step: 'Running AI step', failed_phase: 'ai_step', error_message: 'boom',
    }))
    await waitForRole(qc)
    expect(screen.queryByTestId('pretranslate-btn')).toBeNull()
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()
    expect(screen.queryByTestId('clear-translations-btn')).toBeNull()
  })

  it('canEdit=false offers none of them', async () => {
    const { qc } = renderWithJob(makeJob({ workflow_step: aiStep, counts: clearable }), { canEdit: false })
    await waitForRole(qc)
    expect(screen.queryByTestId('pretranslate-btn')).toBeNull()
    expect(screen.queryByTestId('run-ai-step-btn')).toBeNull()
    expect(screen.queryByTestId('clear-translations-btn')).toBeNull()
  })

  it('canEdit=false still offers Stop while a run is live', () => {
    renderWithJob(makeJob({ pipeline_status: 'in_progress', current_step: 'Running AI step' }), { canEdit: false })
    expect(screen.getByTestId('stop-job-btn')).toBeInTheDocument()
  })
})

describe('JobPipelineActions — completing the final step', () => {
  // The completed job may have been the project's last open one, and the
  // backend then completes the project too. Project settings and project
  // events are refreshed only by invalidateProjectStatus, so they prove the
  // project's status gets refetched.
  it('refetches the project status after /complete', async () => {
    mockAdvanceJobStep.mockResolvedValue({ status: 'workflow_completed' })
    mockCompleteJob.mockResolvedValue({ tm_saved_count: 0 })
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    render(
      <QueryClientProvider client={qc}>
        <JobPipelineActions
          job={makeJob({
            counts: makeJobCounts({ approved: 9, total: 10 }),
            workflow_step: { id: 1, step_name: 'review', step_order: 1, status: 'in_progress', is_last_step: true, is_ai_step: false, deadline: null },
          })}
          projectId={42}
        />
      </QueryClientProvider>,
    )
    await userEvent.click(screen.getByTestId('complete-step-btn'))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Complete' }))

    await waitFor(() => expect(mockCompleteJob).toHaveBeenCalledWith(7))
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.project.settings(42) }),
    )
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.projectEvents.byProject(42) })
  })
})
