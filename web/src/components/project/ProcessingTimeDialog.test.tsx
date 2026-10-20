import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import ProcessingTimeDialog from './ProcessingTimeDialog'
import * as timingsApi from '@/api/timings'
import type { ProjectTimings } from '@/api/timings'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function renderDialog(open = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ProcessingTimeDialog projectId={7} open={open} onOpenChange={() => {}} />
    </QueryClientProvider>,
  )
}

const SAMPLE: ProjectTimings = {
  total_compute_ms: 252_000,
  total_queue_wait_ms: 1_500,
  total_runs: 3,
  job_count: 2,
  total_words: 1200,
  stages: [
    {
      stage: 'mt.engine_call',
      group: 'mt',
      n: 20,
      self_ms: 41_230,
      total_ms: 41_230,
      max_ms: 3_100,
      avg_ms: 2_061,
      err: 1,
      meta: { engine: 'deepl' },
      share: 0.9,
    },
    {
      stage: 'tm.exact_lookup',
      group: 'tm',
      n: 3,
      self_ms: 4_500,
      total_ms: 4_500,
      max_ms: 2_000,
      avg_ms: 1_500,
      err: 0,
      share: 0.06,
    },
    // A span that CONTAINS another: total exceeds self by the nested tag
    // transfer's time, and only self counts toward the share.
    {
      stage: 'mt.db_save',
      group: 'mt',
      n: 20,
      self_ms: 2_200,
      total_ms: 2_556,
      max_ms: 190,
      avg_ms: 127,
      err: 0,
      share: 0.03,
    },
    {
      stage: 'mt.tag_transfer',
      group: 'mt',
      n: 20,
      self_ms: 356,
      total_ms: 356,
      max_ms: 22,
      avg_ms: 17,
      err: 0,
      share: 0.005,
    },
    // Whole-task span living in the SAME list — safe because self time
    // already excludes everything above.
    {
      stage: 'task.pipeline',
      group: 'task',
      n: 4,
      self_ms: 300,
      total_ms: 48_586,
      max_ms: 17_500,
      avg_ms: 12_146,
      err: 0,
      share: 0.005,
    },
  ],

  jobs: [
    {
      job_id: 41,
      file_id: 9,
      file_name: 'handbook.docx',
      target_language: 'fr',
      job_status: 'in_progress',
      word_count: 900,
      compute_ms: 200_000,
      queue_wait_ms: 1_000,
      runs: 2,
      first_started_at: '2026-08-17T10:00:00Z',
      last_finished_at: '2026-08-17T10:05:00Z',
      stages: [
        {
          stage: 'mt.engine_call',
          group: 'mt',
          n: 15,
          self_ms: 30_000,
          total_ms: 30_000,
          max_ms: 3_100,
          avg_ms: 2_000,
          err: 0,
          meta: { engine: 'deepl' },
          share: 1,
        },
      ],
    },
    {
      job_id: 42,
      file_id: 9,
      file_name: 'handbook.docx',
      target_language: 'de',
      job_status: 'completed',
      word_count: 300,
      compute_ms: 52_000,
      queue_wait_ms: 500,
      runs: 1,
      first_started_at: '2026-08-17T10:00:00Z',
      last_finished_at: '2026-08-17T10:02:00Z',
      stages: [],
    },
  ],
}

describe('ProcessingTimeDialog', () => {
  it('headlines the AVERAGE job time, never the sum across jobs', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    renderDialog()

    // Jobs run concurrently, so summing their compute produces a number that
    // reads like elapsed time while overstating it. 252000ms over 2 jobs is
    // 126000 → "2.1m" average, and the sum "4.2m" must NOT appear as a
    // headline figure.
    expect(await screen.findByText('2.1m')).toBeInTheDocument()
    expect(screen.getByText('Avg per job')).toBeInTheDocument()
    expect(screen.queryByText('4.2m')).not.toBeInTheDocument()

    // The slowest job is the closer proxy for what someone actually waited.
    expect(screen.getByText('slowest 3.3m')).toBeInTheDocument()

    // Queue wait is averaged for the same reason: 1500ms over 2 jobs.
    expect(screen.getByText('750ms')).toBeInTheDocument()
  })

  it('lists stages with counts, averages and the engine tag', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    const user = userEvent.setup()
    renderDialog()

    await user.click(await screen.findByTestId('group-row-mt'))
    const mtRow = await screen.findByTestId('stage-row-mt.engine_call')
    expect(mtRow).toHaveTextContent('Engine call')
    expect(mtRow).toHaveTextContent('41.2s')
    expect(mtRow).toHaveTextContent('20 ×')
    expect(mtRow).toHaveTextContent('avg 2.1s')
    expect(mtRow).toHaveTextContent('slowest 3.1s')
    expect(mtRow).toHaveTextContent('deepl')
    // The err counter is the reason failures don't masquerade as fast runs.
    expect(mtRow).toHaveTextContent('1 failed')

    // A clean stage must NOT show a failure badge.
    await user.click(screen.getByTestId('group-row-tm'))
    const tmRow = screen.getByTestId('stage-row-tm.exact_lookup')
    expect(tmRow).not.toHaveTextContent('failed')
  })

  it('shows self time as the headline and only surfaces inclusive time where it differs', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    const user = userEvent.setup()
    renderDialog()

    await user.click(await screen.findByTestId('group-row-mt'))
    // mt.db_save CONTAINS mt.tag_transfer. Its headline is self (2.2s) so the
    // list still partitions; the inclusive 2.6s is offered as context rather
    // than as a number anyone could accidentally sum.
    const saveRow = await screen.findByTestId('stage-row-mt.db_save')
    expect(saveRow).toHaveTextContent('2.2s')
    expect(saveRow).toHaveTextContent('2.6s incl. nested')

    // A leaf has nothing nested, so the qualifier must not appear — otherwise
    // every row would carry noise that means nothing.
    const tagRow = screen.getByTestId('stage-row-mt.tag_transfer')
    expect(tagRow).toHaveTextContent('356ms')
    expect(tagRow).not.toHaveTextContent('incl. nested')
  })

  it('rolls stages up by phase so the panel opens with a handful of bars', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    renderDialog()

    // The point of the rollup: MT contributes three stages but shows as ONE
    // bar until asked.
    const mtGroup = await screen.findByTestId('group-row-mt')
    expect(mtGroup).toHaveTextContent('MT')
    expect(mtGroup).toHaveTextContent('3 stages')
    // 41230 + 2200 + 356 = 43786 self, over a 48586 total → 90%.
    expect(mtGroup).toHaveTextContent('90%')
    expect(mtGroup).toHaveTextContent('43.8s')

    // Collapsed by default — stage rows must not be in the DOM yet.
    expect(screen.queryByTestId('stage-row-mt.engine_call')).not.toBeInTheDocument()

    // Task spans roll into an "Overhead" bar rather than reading as a phase.
    expect(screen.getByTestId('group-row-task')).toHaveTextContent('Overhead')
  })

  it('expands a phase to reveal its stages', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    const user = userEvent.setup()
    renderDialog()

    await user.click(await screen.findByTestId('group-row-mt'))
    expect(await screen.findByTestId('stage-row-mt.engine_call')).toBeInTheDocument()
    expect(screen.getByTestId('stage-row-mt.tag_transfer')).toBeInTheDocument()

    // Expanding MT must not drag TM's stages in with it.
    expect(screen.queryByTestId('stage-row-tm.exact_lookup')).not.toBeInTheDocument()
  })

  it('expands a job to reveal its own stage breakdown', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    const user = userEvent.setup()
    renderDialog()

    const jobRow = await screen.findByTestId('timing-job-41')
    // Collapsed: the job's per-stage numbers aren't rendered yet. 30s is the
    // job's own MT total, distinct from the 41.2s project rollup above.
    expect(screen.queryByText('30.0s')).not.toBeInTheDocument()

    await user.click(jobRow)
    await waitFor(() => expect(screen.getByText('30.0s')).toBeInTheDocument())
  })

  it('tells the user why a project has no timings instead of showing zeros', async () => {
    vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue({
      total_compute_ms: 0,
      total_queue_wait_ms: 0,
      total_runs: 0,
      job_count: 0,
      total_words: 0,
      stages: [],
      jobs: [],
    })
    renderDialog()

    expect(await screen.findByTestId('timings-empty')).toHaveTextContent(
      /No processing recorded yet/i,
    )
  })

  it('a failed load shows the error with a Retry', async () => {
    const spy = vi.spyOn(timingsApi, 'getProjectTimings').mockRejectedValueOnce(new Error('server unavailable'))
    const user = userEvent.setup()
    renderDialog()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the processing times: server unavailable")
    expect(screen.queryByTestId('timings-empty')).not.toBeInTheDocument()

    spy.mockResolvedValueOnce(SAMPLE)
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('Avg per job')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('does not fetch while closed', () => {
    const spy = vi.spyOn(timingsApi, 'getProjectTimings').mockResolvedValue(SAMPLE)
    renderDialog(false)
    expect(spy).not.toHaveBeenCalled()
  })
})
