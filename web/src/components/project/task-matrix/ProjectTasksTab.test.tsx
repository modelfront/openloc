// The Tasks tab as a user drives it: what a cell shows, how clicks with and
// without Ctrl/Cmd select, and that the selection bar's Assign reaches the exact step picked — the case the jobs
// list cannot do, since it only knows each job's active step.
//
// Anti-tautology RED proofs:
//   - Build stepTargets from each job's active step (`t.job.workflow_step.id`)
//     instead of the picked task → "Assign reaches the exact step" fails
//     (assignJobStep gets 21, not 22).
//   - Make selectModeFor ignore ctrlKey → the Ctrl+click test fails.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'

import { chipSurfaceClass, chipVariants } from '@/components/ui/chip'
import { ProjectTasksTab } from './ProjectTasksTab'
import { makeJob } from '@/test/fixtures/job'
import { makeProjectTask } from '@/test/fixtures/project-task'
import { menuItem } from '@/test/menu'
import { clickPreset, openPicker } from '@/test/deadline-picker'

const mockListProjectTasks = vi.fn()
const mockAssignJobStep = vi.fn()
const mockSetStepDeadline = vi.fn()

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    listProjectTasks: (...a: unknown[]) => mockListProjectTasks(...a),
    assignJobStep: (...a: unknown[]) => mockAssignJobStep(...a),
    setStepDeadline: (...a: unknown[]) => mockSetStepDeadline(...a),
    listMembers: vi.fn().mockResolvedValue([{ id: 8, name: 'Bob', email: 'bob@test.com', role: 'linguist' }]),
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

// jsdom has no layout, so the virtualizer would mount no rows. Render all of
// them; the windowing itself is TanStack's and is checked in the browser.
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count, estimateSize }: { count: number; estimateSize: () => number }) => ({
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({
      index, key: index, size: estimateSize(), start: index * estimateSize(), end: (index + 1) * estimateSize(),
    })),
    getTotalSize: () => count * estimateSize(),
    measure: () => {},
  }),
}))

const TRANSLATE = { workflow_step_id: 100, step_name: 'translate', step_order: 0 }
const REVIEW = { workflow_step_id: 200, step_name: 'review', step_order: 1 }
const activeStep = (id: number) =>
  ({ id, step_name: 'translate', step_order: 0, status: 'in_progress', is_last_step: false, is_ai_step: false, deadline: null })

const jobs = [
  makeJob({ id: 1, file_id: 10, file_name: 'guide.docx', target_language: 'de', word_count: 120, workflow_step: activeStep(12) }),
  makeJob({ id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'fr', word_count: 120, workflow_step: activeStep(21) }),
  makeJob({ id: 3, file_id: 20, file_name: 'api.md', target_language: 'de', word_count: 40, pipeline_status: 'failed', error_message: 'MT engine rejected the API key.', workflow_step: activeStep(31) }),
]
const tasks = [
  makeProjectTask({ id: 11, job_id: 1, ...TRANSLATE, status: 'completed', assigned_to: 7, assignee_name: 'Ann' }),
  makeProjectTask({ id: 12, job_id: 1, ...REVIEW, status: 'in_progress', assigned_to: 8, assignee_name: 'Bob', deadline: '2030-01-15T12:00:00Z' }),
  makeProjectTask({ id: 21, job_id: 2, ...TRANSLATE, status: 'in_progress', assigned_to: 7, assignee_name: 'Ann' }),
  makeProjectTask({ id: 22, job_id: 2, ...REVIEW, status: 'pending' }),
  makeProjectTask({ id: 31, job_id: 3, ...TRANSLATE, status: 'in_progress' }),
  makeProjectTask({ id: 32, job_id: 3, ...REVIEW, status: 'pending' }),
]

// A step row opens the editor with window.open, which jsdom does not have.
// By default the popup is "blocked", so the row falls back to this tab's
// router and the editor route shows; a test that wants the new tab says so.
const openSpy = vi.fn<(url?: string | URL, target?: string) => Window | null>(() => null)

beforeEach(() => {
  mockListProjectTasks.mockReset().mockResolvedValue(tasks)
  mockAssignJobStep.mockReset().mockResolvedValue({ assigned: true })
  mockSetStepDeadline.mockReset().mockResolvedValue({ updated: true })
  openSpy.mockReset().mockReturnValue(null)
  vi.stubGlobal('open', openSpy)
})
afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.unstubAllGlobals()
})

function renderTab(url = '/projects/42', projectJobs = jobs) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/projects/:projectId" element={<ProjectTasksTab projectId={42} jobs={projectJobs} jobsUpdatedAt={0} isManager />} />
          <Route path="/projects/:projectId/jobs/:jobId/edit" element={<EditorStandIn />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Where a row click lands: the CAT editor's route, standing in for the editor.
function EditorStandIn() {
  const { projectId, jobId } = useParams()
  return <div data-testid="editor-route">{`project ${projectId} job ${jobId}`}</div>
}

const cell = (key: string) => screen.getByTestId(`task-cell-${key}`)
const selectedCount = () => screen.getByTestId('bulk-selected-count').textContent

// The page: one section per step, all closed at first. Opening them all shows
// every job once, under its current step — job 1 at Review, jobs 2 and 3 at
// Translation.
async function openSteps() {
  await screen.findByTestId('task-step-sections')
  for (const section of screen.getAllByTestId(/^task-step-section-(\d+|none)$/)) {
    const header = section.querySelector<HTMLElement>('[aria-expanded="false"]')
    if (header) await userEvent.click(header)
  }
}

async function renderPage(url?: string, projectJobs?: typeof jobs) {
  const view = renderTab(url, projectJobs)
  await openSteps()
  return view
}

// Expand: the full matrix, every task of every job. The page stays mounted
// underneath, so toolbar controls are looked up inside the modal.
async function renderExpanded(url?: string, projectJobs?: typeof jobs) {
  const view = renderTab(url, projectJobs)
  await userEvent.click(await screen.findByTestId('task-matrix-expand'))
  const modal = await screen.findByTestId('task-matrix-modal')
  await within(modal).findByTestId('task-matrix')
  return { view, modal }
}

describe('ProjectTasksTab', () => {
  it('shows each job once, with no way to fold rows together', async () => {
    await renderPage()
    expect(screen.getAllByTestId(/^task-row-job:\d+$/)).toHaveLength(jobs.length)
    expect(screen.queryByRole('group', { name: 'Group by' })).not.toBeInTheDocument()
    expect(screen.queryByText('Group by')).not.toBeInTheDocument()
  })

  it('the page starts with every step closed, each counting the jobs whose current task is there', async () => {
    renderTab()
    await screen.findByTestId('task-step-sections')
    const translation = screen.getByTestId('task-step-section-100')
    const review = screen.getByTestId('task-step-section-200')
    expect(translation).toHaveTextContent('Translate')
    expect(translation).toHaveTextContent('2 tasks') // jobs 2 and 3
    expect(review).toHaveTextContent('1 task') // job 1: translation done, review in progress
    expect(translation.querySelector('[aria-expanded]')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByRole('row')).toHaveLength(0)

    await openSteps()
    const rowKeys = (section: HTMLElement) =>
      within(section).getAllByTestId(/^task-row-job:\d+$/).map(r => r.dataset.testid)
    expect(rowKeys(translation)).toEqual(['task-row-job:3', 'task-row-job:2'])
    expect(rowKeys(review)).toEqual(['task-row-job:1'])
  })

  it('a step table gives each value its own cell: language, file, assignee, deadline, words, note icons — no Open button', async () => {
    await renderPage()
    const review = screen.getByTestId('task-step-section-200')
    expect(within(review).getAllByRole('columnheader').map(h => h.textContent)).toEqual(
      ['', 'Lang', 'File', 'Assignee', 'Deadline', 'Words', ''],
    )
    const row = screen.getByTestId('task-row-job:1')
    // The first cell's text is the state, there for screen readers only.
    expect(within(row).getAllByRole('cell').map(c => c.textContent)).toEqual(
      ['In progress', 'de', 'guide.docx', 'Bob', expect.stringMatching(/^Due /), '120', ''],
    )
    expect(within(review).getByTestId('task-status-job:1')).toHaveClass('sr-only')
    expect(within(review).getByTestId('task-status-job:1')).toHaveAttribute('data-state', 'in_progress')
    // A click on the row opens the job, so the row needs no Open of its own.
    expect(within(review).queryByRole('link')).not.toBeInTheDocument()
    expect(within(review).queryByTestId('task-row-open-1')).not.toBeInTheDocument()
  })

  it('the list shows deadlines and finished tasks as default-size chips; the grid keeps them small', async () => {
    // Job 4 is done: its last step, review, is its current task.
    const done = makeJob({ id: 4, file_id: 20, file_name: 'api.md', target_language: 'fr', workflow_step: activeStep(42) })
    mockListProjectTasks.mockResolvedValue([
      ...tasks,
      makeProjectTask({ id: 41, job_id: 4, ...TRANSLATE, status: 'completed', completed_at: '2026-09-02T10:00:00Z' }),
      makeProjectTask({ id: 42, job_id: 4, ...REVIEW, status: 'completed', completed_at: '2026-09-03T10:00:00Z' }),
    ])
    const { modal } = await renderExpanded('/projects/42', [...jobs, done])
    // The grid (Expand) first: a small Done chip.
    const gridDone = within(within(modal).getByTestId('task-cell-job:4|200')).getByTestId('task-done-chip')
    expect(gridDone).toHaveClass(...chipVariants({ color: 'green', size: 'small' }).split(' '))
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('task-matrix-modal')).not.toBeInTheDocument())

    await openSteps()
    const row = screen.getByTestId('task-row-job:4')
    const doneChip = within(row).getByTestId('task-done-chip')
    expect(doneChip).toHaveTextContent(/^Done \S+ \d+$/) // "Done Sep 3"
    expect(doneChip).toHaveClass(...chipVariants({ color: 'green', size: 'default' }).split(' '))
    const deadline = within(screen.getByTestId('task-row-job:1')).getByTestId('deadline-pill')
    expect(deadline).toHaveClass(...chipVariants({ size: 'default' }).split(' ').filter(c => /^(px|py|text)-/.test(c)))
  })

  it('a row takes its state\'s colour; an error is a red icon whose tooltip gives the message', async () => {
    await renderPage()
    // Same classes as a matrix cell of that state: the row is the surface.
    expect(screen.getByTestId('task-row-job:1')).toHaveClass(...chipSurfaceClass('yellow').split(' '))
    expect(screen.getByTestId('task-row-job:3')).toHaveClass(...chipSurfaceClass('red').split(' '))
    expect(screen.getByTestId('task-status-job:3')).toHaveTextContent('Error')

    // An icon only: the message is not on the row until the tooltip opens.
    const icon = within(screen.getByTestId('task-row-job:3')).getByLabelText('Error')
    expect(screen.queryByText('MT engine rejected the API key.')).not.toBeInTheDocument()
    await userEvent.hover(icon)
    expect(await screen.findByText('MT engine rejected the API key.')).toBeInTheDocument()
    // Rows without an error carry no icon.
    expect(within(screen.getByTestId('task-row-job:1')).queryByLabelText('Error')).not.toBeInTheDocument()
  })

  it('a row shows one icon: the error wins, and its tooltip carries every note', async () => {
    // Job 3 failed on Translation, which Review had sent back: Review is the
    // rejected task, Translation the one in progress again.
    mockListProjectTasks.mockResolvedValue(tasks.map(t =>
      t.id === 32 ? { ...t, status: 'rejected' as const, rejection_reason: 'Wrong register.' } : t))
    await renderPage()
    const row = screen.getByTestId('task-row-job:3')
    const icon = within(row).getByLabelText('Error, Sent back')
    expect(within(row).getAllByLabelText(/Error|Sent back|AI step/)).toHaveLength(1)
    await userEvent.hover(icon)
    expect(await screen.findByText('MT engine rejected the API key.')).toBeInTheDocument()
    expect(screen.getByText('Wrong register.')).toBeInTheDocument()
  })

  it('a job sent back shows an icon on its row under the reopened step; the tooltip gives the reviewer\'s reason', async () => {
    // As the server leaves it: Review rejected with the reason, Translation in progress again.
    mockListProjectTasks.mockResolvedValue(tasks.map(t =>
      t.id === 22 ? { ...t, status: 'rejected' as const, rejection_reason: 'Terms do not match the glossary.' } : t))
    await renderPage()
    const row = within(screen.getByTestId('task-step-section-100')).getByTestId('task-row-job:2')
    await userEvent.hover(within(row).getByLabelText('Sent back'))
    expect(await screen.findByText('Sent back from Review')).toBeInTheDocument()
    expect(screen.getByText('Terms do not match the glossary.')).toBeInTheDocument()
  })

  it('a row shows the comment its task was handed; the tooltip gives it', async () => {
    // Job 1 sits at Review, handed a comment when Translation was completed.
    mockListProjectTasks.mockResolvedValue(tasks.map(t =>
      t.id === 12 ? { ...t, handoff_note: 'Section 3 uses\nthe new term.' } : t))
    await renderPage()
    const row = within(screen.getByTestId('task-step-section-200')).getByTestId('task-row-job:1')
    await userEvent.hover(within(row).getByLabelText('Comment'))
    expect(await screen.findByText('Comment from the previous step')).toBeInTheDocument()
    expect(screen.getByText('Section 3 uses the new term.')).toHaveClass('whitespace-pre-line')
    // Rows whose task was handed no comment carry no comment icon.
    expect(within(screen.getByTestId('task-row-job:2')).queryByLabelText(/Comment/)).not.toBeInTheDocument()
  })

  it('a row sent back whose task was handed a comment shows one icon with both notes', async () => {
    // Prep handed Translation a comment; Review later sent the job back to Translation.
    mockListProjectTasks.mockResolvedValue([
      makeProjectTask({ id: 20, job_id: 2, workflow_step_id: 300, step_name: 'prep', step_order: 0, status: 'completed' }),
      makeProjectTask({ id: 21, job_id: 2, workflow_step_id: 100, step_name: 'translate', step_order: 1, status: 'in_progress', handoff_note: 'Glossary attached.' }),
      makeProjectTask({ id: 22, job_id: 2, workflow_step_id: 200, step_name: 'review', step_order: 2, status: 'rejected', rejection_reason: 'Fix the terms.' }),
    ])
    await renderPage('/projects/42', [jobs[1]])
    const row = screen.getByTestId('task-row-job:2')
    expect(within(row).getAllByLabelText(/Sent back|Comment/)).toHaveLength(1)
    await userEvent.hover(within(row).getByLabelText('Sent back, Comment'))
    expect(await screen.findByText('Fix the terms.')).toBeInTheDocument()
    expect(screen.getByText('Glossary attached.')).toBeInTheDocument()
  })

  it('a row click opens the job in the CAT editor; the box beside it only selects', async () => {
    await renderPage()
    // The box (and its cell) select without opening anything.
    fireEvent.click(screen.getByTestId('task-select-job:3'))
    expect(selectedCount()).toBe('1 task selected')
    expect(screen.queryByTestId('editor-route')).not.toBeInTheDocument()

    fireEvent.click(within(screen.getByTestId('task-row-job:2')).getByText('guide.docx'))
    expect(openSpy).toHaveBeenCalledWith('/projects/42/jobs/2/edit', '_blank')
    // The popup was blocked here, so the job opens in this tab instead.
    expect(screen.getByTestId('editor-route')).toHaveTextContent('project 42 job 2')
  })

  it('a row click opens the editor in a new tab, and coming back fetches the tasks again', async () => {
    openSpy.mockReturnValue({} as Window)
    await renderPage()
    fireEvent.click(within(screen.getByTestId('task-row-job:2')).getByText('guide.docx'))
    expect(openSpy).toHaveBeenCalledWith('/projects/42/jobs/2/edit', '_blank')
    expect(screen.queryByTestId('editor-route')).not.toBeInTheDocument()
    expect(mockListProjectTasks).toHaveBeenCalledTimes(1)

    act(() => { window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(mockListProjectTasks).toHaveBeenCalledTimes(2))
  })

  it('a row whose AI step runs does not open, and its note says why', async () => {
    const running = makeJob({
      id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'fr', word_count: 120,
      pipeline_status: 'in_progress', current_step: 'AI step (QE+APE): 3 / 8', workflow_step: activeStep(21),
    })
    await renderPage('/projects/42', [jobs[0], running, jobs[2]])
    const row = screen.getByTestId('task-row-job:2')
    expect(row).toHaveClass('cursor-not-allowed')
    fireEvent.click(row)
    expect(screen.queryByTestId('editor-route')).not.toBeInTheDocument()

    const icon = within(row).getByLabelText('AI step running')
    await userEvent.hover(icon)
    expect(await screen.findByText('The AI step is running. The job opens when it finishes.')).toBeInTheDocument()
  })

  it('a row box selects the row; the header box selects the whole step', async () => {
    await renderPage()
    fireEvent.click(screen.getByTestId('task-select-job:2'))
    expect(selectedCount()).toBe('1 task selected')
    expect(screen.getByTestId('task-select-job:2')).toBeChecked()
    expect(screen.getByTestId('task-select-all-100')).toHaveProperty('indeterminate', true)

    // The box adds the other row; the header is then full.
    fireEvent.click(screen.getByTestId('task-select-job:3'))
    expect(selectedCount()).toBe('2 tasks selected')
    expect(screen.getByTestId('task-select-all-100')).toBeChecked()

    // The header box clears the step; a second click selects it all.
    fireEvent.click(screen.getByTestId('task-select-all-100'))
    expect(screen.queryByTestId('bulk-selected-count')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('task-select-all-100'))
    expect(selectedCount()).toBe('2 tasks selected')

    // The box of a selected row takes it back out.
    fireEvent.click(screen.getByTestId('task-select-job:3'))
    expect(selectedCount()).toBe('1 task selected')
  })

  it('a step header says what a click does: "Show 2 tasks" while closed, "Hide 2 tasks" while open', async () => {
    renderTab()
    const translation = await screen.findByTestId('task-step-section-100')
    const header = translation.querySelector<HTMLElement>('[aria-expanded]')!
    expect(header).toHaveTextContent('Show 2 tasks')
    await userEvent.click(header)
    expect(header).toHaveAttribute('aria-expanded', 'true')
    expect(header).toHaveTextContent('Hide 2 tasks')
    expect(header).not.toHaveTextContent('Show')
  })

  it('a step with no current tasks says "No tasks" and does not open', async () => {
    // Job 1 back at Translation: nothing is current at Review.
    mockListProjectTasks.mockResolvedValue(tasks.map(t =>
      t.id === 11 ? { ...t, status: 'in_progress' } : t.id === 12 ? { ...t, status: 'pending' } : t))
    renderTab()
    const review = await screen.findByTestId('task-step-section-200')
    expect(review).toHaveTextContent('No tasks')
    expect(review).not.toHaveTextContent('Show')
    expect(within(review).queryByRole('button')).not.toBeInTheDocument()
    expect(review.querySelector('[aria-expanded]')).toBeNull()
    await userEvent.click(within(review).getByText('Review'))
    expect(within(review).queryByRole('table')).not.toBeInTheDocument()
    // The steps with work still open.
    expect(screen.getByTestId('task-step-section-100')).toHaveTextContent('Show 3 tasks')
  })

  it('a step header is yellow with a task in progress, green once all its tasks are done, plain otherwise', async () => {
    const header = (stepId: number) => screen.getByTestId(`task-step-section-${stepId}`).firstElementChild as HTMLElement
    const surfaces = [...chipSurfaceClass('yellow').split(' '), ...chipSurfaceClass('green').split(' ')]
    let view = renderTab()
    await screen.findByTestId('task-step-sections')
    // Translation has jobs 2 and 3 in progress; Review has job 1.
    expect(header(100)).toHaveClass(...chipSurfaceClass('yellow').split(' '))
    expect(header(200)).toHaveClass(...chipSurfaceClass('yellow').split(' '))

    // No review started: Review has only tasks not started yet.
    view.unmount()
    mockListProjectTasks.mockResolvedValue(tasks.map(t => (t.workflow_step_id === REVIEW.workflow_step_id ? { ...t, status: 'pending' } : t)))
    view = renderTab()
    await screen.findByTestId('task-step-sections')
    expect(header(100)).toHaveClass(...chipSurfaceClass('yellow').split(' '))
    expect(surfaces.filter(c => header(200).classList.contains(c))).toEqual([])

    // Every translation done, both reviews started: no job is at Translation
    // now, and it is green all the same.
    view.unmount()
    const healthy = jobs.filter(j => j.pipeline_status !== 'failed')
    mockListProjectTasks.mockResolvedValue(tasks
      .filter(t => healthy.some(j => j.id === t.job_id))
      .map(t => (t.workflow_step_id === TRANSLATE.workflow_step_id ? { ...t, status: 'completed' } : { ...t, status: 'in_progress' })))
    renderTab('/projects/42', healthy)
    await screen.findByTestId('task-step-sections')
    expect(screen.getByTestId('task-step-section-100')).toHaveTextContent('No tasks')
    expect(header(100)).toHaveClass(...chipSurfaceClass('green').split(' '))
    expect(header(200)).toHaveClass(...chipSurfaceClass('yellow').split(' '))
  })

  it('a dragged column width applies to every step table and is remembered', async () => {
    localStorage.clear()
    // jsdom has no layout: File shows 300px wide.
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { width: 300, height: 20, x: 0, y: 0, top: 0, left: 0, right: 300, bottom: 20, toJSON: () => ({}) },
    )
    try {
      const view = await renderPage()
      const fileWidths = () => screen.getAllByRole('separator', { name: 'Resize file column' })
        .map(h => (h.parentElement as HTMLElement).style.width)
      expect(fileWidths()).toEqual(['', '']) // Translation and Review, sharing the space

      const [edge] = screen.getAllByRole('separator', { name: 'Resize file column' })
      fireEvent.mouseDown(edge, { clientX: 300 })
      fireEvent.mouseMove(document.body, { clientX: 360 })
      fireEvent.mouseUp(document.body, { clientX: 360 })
      expect(fileWidths()).toEqual(['360px', '360px'])
      // Assignee takes what is left, so it is never dragged.
      expect(screen.queryByRole('separator', { name: 'Resize assignee column' })).not.toBeInTheDocument()

      view.unmount()
      await renderPage()
      expect(fileWidths()).toEqual(['360px', '360px'])
    } finally {
      rect.mockRestore()
    }
  })

  it('a lone section starts open: a one-step workflow, or no workflow at all', async () => {
    mockListProjectTasks.mockResolvedValue(tasks.filter(t => t.workflow_step_id === TRANSLATE.workflow_step_id))
    const view = renderTab()
    const only = await screen.findByTestId('task-step-section-100')
    expect(screen.getAllByTestId(/^task-step-section-(\d+|none)$/)).toHaveLength(1)
    expect(only.querySelector('[aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
    // Its rows show without a click.
    expect(within(only).getAllByTestId(/^task-row-job:\d+$/)).toHaveLength(jobs.length)

    view.unmount()
    mockListProjectTasks.mockResolvedValue([])
    renderTab()
    const none = await screen.findByTestId('task-step-section-none')
    expect(none.querySelector('[aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
  })

  it('a project without a workflow still lists its jobs, and a row opens its job', async () => {
    mockListProjectTasks.mockResolvedValue([])
    await renderPage()
    const section = screen.getByTestId('task-step-section-none')
    expect(section).toHaveTextContent('No workflow step')
    expect(section).toHaveTextContent('3 jobs')
    expect(screen.queryByText('No tasks match the current filters.')).not.toBeInTheDocument()
    fireEvent.click(within(section).getByTestId('task-row-job:1'))
    expect(screen.getByTestId('editor-route')).toHaveTextContent('project 42 job 1')
  })

  it('a job with nothing started sits under the first step', async () => {
    const fresh = makeJob({ id: 4, file_id: 30, file_name: 'new.md', target_language: 'it', workflow_step: activeStep(41) })
    mockListProjectTasks.mockResolvedValue([
      ...tasks,
      makeProjectTask({ id: 41, job_id: 4, ...TRANSLATE, status: 'pending' }),
      makeProjectTask({ id: 42, job_id: 4, ...REVIEW, status: 'pending' }),
    ])
    await renderPage('/projects/42', [...jobs, fresh])
    const translation = screen.getByTestId('task-step-section-100')
    expect(within(translation).getByTestId('task-status-job:4')).toHaveAttribute('data-state', 'not_started')
    expect(within(screen.getByTestId('task-step-section-200')).queryByTestId('task-row-job:4')).not.toBeInTheDocument()
  })

  it('on the page, a row selects its current task and the bar acts on it', async () => {
    await renderPage()
    fireEvent.click(screen.getByTestId('task-select-job:2'))
    expect(selectedCount()).toBe('1 task selected')
    await userEvent.click(screen.getByTestId('bulk-assign-btn'))
    await userEvent.click(await menuItem('bulk-assign-user-8'))
    await waitFor(() => expect(mockAssignJobStep).toHaveBeenCalledTimes(1))
    expect(mockAssignJobStep).toHaveBeenCalledWith(2, 21, 8)
  })

  it('on the page, the legend and the filters go by the current task', async () => {
    await renderPage()
    // Current tasks: job 1 review in progress, job 2 translation in progress, job 3 failed.
    expect(screen.getByTestId('task-legend-in_progress')).toHaveTextContent('In progress 2')
    expect(screen.getByTestId('task-legend-error')).toHaveTextContent('Error 1')
    expect(screen.getByTestId('task-legend-completed')).toHaveTextContent('Completed 0')

    // Job 2's pending review is unassigned, but its current task is Ann's
    // translation: it does not show.
    await userEvent.click(screen.getByTestId('task-filter-menu'))
    await userEvent.click(await screen.findByTestId('task-filter-menu-option-assignee'))
    await userEvent.click(await screen.findByRole('button', { name: 'Unassigned' }))
    expect(screen.getByTestId('task-filter-assignee')).toHaveTextContent('Assignee:Unassigned')
    expect(screen.getByTestId('task-row-job:3')).toBeInTheDocument()
    expect(screen.queryByTestId('task-row-job:2')).not.toBeInTheDocument()
    expect(screen.queryByTestId('task-row-job:1')).not.toBeInTheDocument()
  })

  it('starts with no filter chips, only the filter icon', async () => {
    await renderPage()
    expect(screen.queryByTestId('task-filter-status')).not.toBeInTheDocument()
    expect(screen.queryByTestId('task-filter-language')).not.toBeInTheDocument()
    expect(screen.getByTestId('task-filter-menu')).toBeInTheDocument()
  })

  it('the filter icon opens the picked filter\'s options at once; the chip appears only with a value', async () => {
    await renderPage()
    await userEvent.click(screen.getByTestId('task-filter-menu'))
    const popup = await screen.findByTestId('task-filter-menu-popup')
    // One file per project here would hide File; this project has two.
    expect(within(popup).getAllByRole('button').map(b => b.textContent)).toEqual(['Status', 'File', 'Language', 'Assignee', 'Deadline'])

    await userEvent.click(within(popup).getByTestId('task-filter-menu-option-target'))
    // Same popup, now the Language options — and still no chip.
    expect(popup).toHaveTextContent('Language')
    expect(within(popup).getAllByRole('button').map(b => b.textContent)).toEqual(['All', 'de', 'fr'])
    expect(screen.queryByTestId('task-filter-language')).not.toBeInTheDocument()

    await userEvent.click(within(popup).getByRole('button', { name: 'fr' }))
    await waitFor(() => expect(screen.queryByTestId('task-filter-menu-popup')).not.toBeInTheDocument())
    expect(screen.getByTestId('task-filter-language')).toHaveTextContent('Language:fr')
    expect(screen.getByTestId('task-row-job:2')).toBeInTheDocument()
    expect(screen.queryByTestId('task-row-job:1')).not.toBeInTheDocument()
    expect(screen.queryByText('Target')).not.toBeInTheDocument()

    // A set filter leaves the list; closing midway adds nothing.
    await userEvent.click(screen.getByTestId('task-filter-menu'))
    const again = await screen.findByTestId('task-filter-menu-popup')
    expect(within(again).queryByTestId('task-filter-menu-option-target')).not.toBeInTheDocument()
    await userEvent.click(within(again).getByTestId('task-filter-menu-option-state'))
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('task-filter-menu-popup')).not.toBeInTheDocument())
    expect(screen.queryByTestId('task-filter-status')).not.toBeInTheDocument()
  })

  it('clearing a filter — its × or "All" — takes its chip off the bar', async () => {
    // Nothing is fr and failed, so the page starts empty; the chips still show.
    renderTab('/projects/42?target=fr&task_status=error')
    expect(await screen.findByTestId('task-filter-language')).toHaveTextContent('Language:fr')
    // Filters clear one at a time; there is no clear-all button.
    expect(screen.queryByRole('button', { name: /clear filters/i })).not.toBeInTheDocument()

    await userEvent.click(screen.getByTestId('task-filter-language-remove'))
    expect(screen.queryByTestId('task-filter-language')).not.toBeInTheDocument()

    await userEvent.click(screen.getByTestId('task-filter-status'))
    await userEvent.click(await screen.findByRole('button', { name: 'All' }))
    expect(screen.queryByTestId('task-filter-status')).not.toBeInTheDocument()
    await openSteps()
    expect(screen.getByTestId('task-row-job:1')).toBeInTheDocument()
  })

  it('with every filter set, the filter icon goes away; clearing one brings it back', async () => {
    renderTab('/projects/42?task_status=in_progress&file=10&target=de&assignee=8&due=week')
    await screen.findByTestId('task-filter-due')
    expect(screen.queryByTestId('task-filter-menu')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('task-filter-due-remove'))
    expect(screen.getByTestId('task-filter-menu')).toBeInTheDocument()
  })

  it('the page leaves Not started out of its legend and its Status filter; the expanded view keeps it', async () => {
    const view = await renderPage()
    const legend = (root: HTMLElement) => within(root).getAllByTestId(/^task-legend-/).map(c => c.dataset.testid)
    expect(legend(screen.getByTestId('task-matrix-legend'))).toEqual(['task-legend-in_progress', 'task-legend-completed', 'task-legend-error'])
    await userEvent.click(screen.getByTestId('task-filter-menu'))
    await userEvent.click(await screen.findByTestId('task-filter-menu-option-state'))
    const popup = await screen.findByTestId('task-filter-menu-popup')
    expect(within(popup).getAllByRole('button').map(b => b.textContent)).toEqual(['All', 'In progress', 'Completed', 'Error'])
    await userEvent.keyboard('{Escape}')

    view.unmount()
    const { modal } = await renderExpanded()
    expect(legend(within(modal).getByTestId('task-matrix-legend'))).toEqual(
      ['task-legend-not_started', 'task-legend-in_progress', 'task-legend-completed', 'task-legend-error'])
    await userEvent.click(within(modal).getByTestId('task-filter-status'))
    expect(await screen.findByRole('button', { name: 'Not started' })).toBeInTheDocument()
  })

  it('a legend chip toggles the Status filter, putting its chip on the bar and taking it off', async () => {
    await renderPage()
    expect(screen.queryByTestId('task-filter-status')).not.toBeInTheDocument()

    expect(screen.getByTestId('task-legend-error')).toHaveAttribute('aria-pressed', 'false')
    await userEvent.click(screen.getByTestId('task-legend-error'))
    expect(screen.getByTestId('task-filter-status')).toHaveTextContent('Error')
    expect(screen.getByTestId('task-legend-error')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('task-row-job:3')).toBeInTheDocument() // the failed job
    expect(screen.queryByTestId('task-row-job:1')).not.toBeInTheDocument()

    // Another state only changes the value.
    await userEvent.click(screen.getByTestId('task-legend-in_progress'))
    expect(screen.getByTestId('task-filter-status')).toHaveTextContent('In progress')
    expect(screen.getByTestId('task-legend-error')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('task-row-job:1')).toBeInTheDocument()
    expect(screen.queryByTestId('task-row-job:3')).not.toBeInTheDocument()

    // Clicked again, it clears the filter, and the chip goes with it.
    await userEvent.click(screen.getByTestId('task-legend-in_progress'))
    expect(screen.queryByTestId('task-filter-status')).not.toBeInTheDocument()
    expect(screen.getByTestId('task-legend-in_progress')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('task-row-job:3')).toBeInTheDocument()
  })

  it('hides File from the menu when there is only one file', async () => {
    await renderPage('/projects/42', [jobs[0], jobs[1]])
    await userEvent.click(screen.getByTestId('task-filter-menu'))
    expect(await screen.findByTestId('task-filter-menu-option-target')).toBeInTheDocument()
    expect(screen.queryByTestId('task-filter-menu-option-file')).not.toBeInTheDocument()
  })

  it('a filter set in the URL shows its chip', async () => {
    await renderPage('/projects/42?assignee=none')
    expect(screen.getByTestId('task-filter-assignee')).toHaveTextContent('Unassigned')
  })

  it('gives the language and the file a column each', async () => {
    await renderExpanded()
    const titles = screen.getAllByRole('columnheader').map(h => h.textContent)
    expect(titles.slice(0, 3)).toEqual(['#', 'Language', 'File'])

    // job:3 opens the de run, so its language cell shows the code.
    expect(screen.getByTestId('task-row-language-job:3')).toHaveTextContent('de')
    const file = screen.getByTestId('task-row-header-job:1')
    expect(within(file).getByText('guide.docx')).toBeInTheDocument()
    expect(within(file).queryByText('de')).not.toBeInTheDocument()
    expect(within(file).getByTestId('task-row-open-1')).toHaveAttribute('href', '/projects/42/jobs/1/edit')
  })

  it('a job whose AI step runs keeps a disabled Open that says why on hover', async () => {
    const running = makeJob({
      id: 2, file_id: 10, file_name: 'guide.docx', target_language: 'fr', word_count: 120,
      pipeline_status: 'in_progress', current_step: 'AI step (QE+APE): 3 / 8', workflow_step: activeStep(21),
    })
    await renderExpanded('/projects/42', [jobs[0], running, jobs[2]])

    const open = screen.getByTestId('task-row-open-2')
    expect(open).toHaveAttribute('aria-disabled', 'true')
    expect(open).not.toHaveAttribute('href')
    // It opens at once: the default hover delay is ~600 ms.
    await userEvent.hover(open)
    expect(await screen.findByText('The AI step is running. The job opens when it finishes.', {}, { timeout: 150 })).toBeInTheDocument()

    // A click neither opens the editor nor selects the row it sits in.
    await userEvent.click(open)
    expect(screen.queryByTestId('bulk-selected-count')).toBeNull()

    // A job with nothing running still links straight to the editor.
    expect(screen.getByTestId('task-row-open-1')).toHaveAttribute('href', '/projects/42/jobs/1/edit')
  })

  it('Open in the expanded view starts the CAT editor in a new tab', async () => {
    const { modal } = await renderExpanded()
    const open = within(modal).getByTestId('task-row-open-1')
    expect(open).toHaveAttribute('target', '_blank')
    expect(open).toHaveAttribute('rel', 'noopener noreferrer')
  })

  // The editor tab changes this project's tasks; the app does not refetch on
  // window focus, so the tab refetches itself once on the way back.
  it('Open, then coming back to this tab, fetches the tasks again — once', async () => {
    const { modal } = await renderExpanded()
    fireEvent.click(within(modal).getByTestId('task-row-open-1'))
    expect(mockListProjectTasks).toHaveBeenCalledTimes(1)

    act(() => { window.dispatchEvent(new Event('focus')) })
    await waitFor(() => expect(mockListProjectTasks).toHaveBeenCalledTimes(2))
    act(() => { window.dispatchEvent(new Event('focus')) })
    await new Promise(r => setTimeout(r, 0))
    expect(mockListProjectTasks).toHaveBeenCalledTimes(2)
  })

  it('shows each language once, over all its rows; a click selects them all', async () => {
    await renderExpanded()
    // Sorted by language, then file: de api.md (job 3), de guide.docx (job 1), fr guide.docx (job 2).
    expect(screen.getByTestId('task-row-language-job:3')).toHaveTextContent('de')
    expect(screen.getByTestId('task-row-language-job:1')).toHaveTextContent('')
    expect(screen.getByTestId('task-row-language-job:2')).toHaveTextContent('fr')

    fireEvent.click(screen.getByTestId('task-row-language-job:1'))
    expect(selectedCount()).toBe('4 tasks selected')
  })

  it('a column header edge drags the column wider; double-click resets it; the width is remembered', async () => {
    localStorage.clear()
    const { view } = await renderExpanded()
    const width = () => screen.getByTestId('task-matrix-col-file').style.width
    const before = width()

    const edge = screen.getByTestId('task-matrix-resize-file')
    fireEvent.mouseDown(edge, { clientX: 300 })
    fireEvent.mouseMove(document.body, { clientX: 360 })
    fireEvent.mouseUp(document.body, { clientX: 360 })
    expect(width()).toBe(`${parseInt(before) + 60}px`)

    // A fresh mount (another visit) comes back at the dragged width.
    view.unmount()
    await renderExpanded()
    expect(width()).toBe(`${parseInt(before) + 60}px`)

    fireEvent.doubleClick(screen.getByTestId('task-matrix-resize-file'))
    expect(width()).toBe(before)
  })

  it('dragging a step header edge resizes the column without selecting the step', async () => {
    localStorage.clear()
    await renderExpanded()
    const edge = screen.getByTestId('task-matrix-resize-step-200')
    fireEvent.mouseDown(edge, { clientX: 500 })
    fireEvent.mouseMove(document.body, { clientX: 450 })
    fireEvent.mouseUp(document.body, { clientX: 450 })
    fireEvent.click(screen.getByTestId('task-matrix-step-200'))
    expect(screen.queryByTestId('bulk-actions-bar')).not.toBeInTheDocument()
    // The step can still be selected with a real click afterwards.
    fireEvent.click(screen.getByTestId('task-matrix-step-200'))
    expect(selectedCount()).toBe('3 tasks selected')
  })

  it('shows one cell per job and step, with assignee, deadline, words and state — no segment count', async () => {
    await renderExpanded()

    const review = cell('job:1|200')
    expect(review).toHaveAttribute('data-state', 'in_progress')
    expect(within(review).getByText('Bob')).toBeInTheDocument()
    expect(within(review).getByTestId('deadline-pill')).toBeInTheDocument()
    expect(within(review).getByText('120 words')).toBeInTheDocument()
    expect(within(review).queryByText(/segments?$/)).not.toBeInTheDocument()
    // The colour carries the state; the label is there for screen readers only.
    expect(within(review).getByText('In progress')).toHaveClass('sr-only')

    expect(cell('job:1|100')).toHaveAttribute('data-state', 'completed')
    expect(cell('job:2|200')).toHaveAttribute('data-state', 'not_started')
    expect(within(cell('job:2|200')).getByText('Unassigned')).toBeInTheDocument()

    const failed = cell('job:3|100')
    expect(failed).toHaveAttribute('data-state', 'error')
    expect(within(failed).getByTestId('task-cell-error')).toHaveTextContent('MT engine rejected the API key.')
  })

  it('in the expanded view, a task handed a comment says so, finished or not; hover gives the comment', async () => {
    mockListProjectTasks.mockResolvedValue(tasks.map(t =>
      t.id === 12 ? { ...t, handoff_note: 'Section 3 uses the new term.' }
        : t.id === 11 ? { ...t, handoff_note: 'Glossary attached.' } : t))
    const { modal } = await renderExpanded()
    const review = within(modal).getByTestId('task-cell-job:1|200')
    await userEvent.hover(within(review).getByText('Comment'))
    expect(await screen.findByText('Section 3 uses the new term.')).toBeInTheDocument()
    expect(screen.getByText('Comment from the previous step')).toBeInTheDocument()
    // A finished task keeps the comment it was handed.
    expect(within(within(modal).getByTestId('task-cell-job:1|100')).getByText('Comment')).toBeInTheDocument()
    // A task handed none has no comment line.
    expect(within(within(modal).getByTestId('task-cell-job:2|100')).queryByText('Comment')).not.toBeInTheDocument()
  })

  it('click selects a task, Ctrl/Cmd+click adds another, and a plain click replaces both', async () => {
    await renderExpanded()

    fireEvent.click(cell('job:1|200'))
    expect(cell('job:1|200')).toHaveAttribute('aria-selected', 'true')
    expect(selectedCount()).toBe('1 task selected')

    fireEvent.click(cell('job:2|200'), { ctrlKey: true })
    expect(selectedCount()).toBe('2 tasks selected')
    fireEvent.click(cell('job:2|100'), { metaKey: true })
    expect(selectedCount()).toBe('3 tasks selected')

    // Cmd+click on a selected cell takes it back out.
    fireEvent.click(cell('job:2|100'), { metaKey: true })
    expect(selectedCount()).toBe('2 tasks selected')

    fireEvent.click(cell('job:3|200'))
    expect(selectedCount()).toBe('1 task selected')
    expect(cell('job:1|200')).toHaveAttribute('aria-selected', 'false')
  })

  it('Shift+click is a plain click: there is no range selection', async () => {
    await renderExpanded()
    fireEvent.click(cell('job:3|100'))
    fireEvent.click(cell('job:2|200'), { shiftKey: true })
    expect(selectedCount()).toBe('1 task selected')
    expect(cell('job:3|100')).toHaveAttribute('aria-selected', 'false')
    expect(cell('job:2|200')).toHaveAttribute('aria-selected', 'true')
  })

  it('Escape clears the selection', async () => {
    await renderExpanded()
    fireEvent.click(cell('job:1|200'))
    fireEvent.keyDown(cell('job:1|200'), { key: 'Escape' })
    expect(screen.queryByTestId('bulk-actions-bar')).not.toBeInTheDocument()
  })

  it('Assign reaches the exact step picked, including one that has not started', async () => {
    await renderExpanded()

    // A pending Review step, plus a finished Translate step (nothing to hand over).
    fireEvent.click(cell('job:2|200'))
    fireEvent.click(cell('job:1|100'), { metaKey: true })
    expect(selectedCount()).toBe('2 tasks selected')

    await userEvent.click(screen.getByTestId('bulk-assign-btn'))
    await userEvent.click(await menuItem('bulk-assign-user-8'))

    await waitFor(() => expect(mockAssignJobStep).toHaveBeenCalledTimes(1))
    expect(mockAssignJobStep).toHaveBeenCalledWith(2, 22, 8)
  })

  it('Set deadline skips AI steps, which run by themselves and take no deadline', async () => {
    const AUTO = { workflow_step_id: 300, step_name: 'auto ape', step_order: 2, step_type: 'ai' as const }
    mockListProjectTasks.mockResolvedValue([...tasks, makeProjectTask({ id: 13, job_id: 1, ...AUTO, status: 'pending' })])
    await renderExpanded()

    // The cell has no deadline to show, not even "No due date".
    expect(within(cell('job:1|300')).queryByTestId('deadline-pill')).not.toBeInTheDocument()
    expect(within(cell('job:2|200')).getByTestId('deadline-pill')).toBeInTheDocument()

    // Only the AI step: nothing it could apply to.
    fireEvent.click(cell('job:1|300'))
    expect(screen.getByTestId('bulk-deadline-btn')).toBeDisabled()

    // With a human step beside it, only that one gets the date.
    fireEvent.click(cell('job:2|200'), { metaKey: true })
    expect(screen.getByTestId('bulk-deadline-btn')).toBeEnabled()
    // A preset: one click, where paging the calendar to a far month is slow.
    openPicker('bulk-deadline-btn')
    clickPreset('2 days')

    await waitFor(() => expect(mockSetStepDeadline).toHaveBeenCalledTimes(1))
    expect(mockSetStepDeadline).toHaveBeenCalledWith(2, 22, expect.any(String))
  })

  it('Assign skips AI steps, which run by themselves and take nobody', async () => {
    const AUTO = { workflow_step_id: 300, step_name: 'auto ape', step_order: 2, step_type: 'ai' as const }
    mockListProjectTasks.mockResolvedValue([...tasks, makeProjectTask({ id: 13, job_id: 1, ...AUTO, status: 'pending' })])
    await renderExpanded()

    fireEvent.click(cell('job:1|300'))
    expect(screen.getByTestId('bulk-assign-btn')).toBeDisabled()

    fireEvent.click(cell('job:2|200'), { metaKey: true })
    await userEvent.click(screen.getByTestId('bulk-assign-btn'))
    await userEvent.click(await menuItem('bulk-assign-user-8'))

    await waitFor(() => expect(mockAssignJobStep).toHaveBeenCalledTimes(1))
    expect(mockAssignJobStep).toHaveBeenCalledWith(2, 22, 8)
  })

  it('numbers the rows in the order shown, and renumbers after filtering', async () => {
    const { modal } = await renderExpanded()
    const numberOf = (rowKey: string) =>
      within(screen.getByTestId(`task-row-${rowKey}`)).getByTestId('task-row-number').textContent

    // Language, then file: de/api.md, de/guide.docx, fr/guide.docx.
    expect([numberOf('job:3'), numberOf('job:1'), numberOf('job:2')]).toEqual(['1', '2', '3'])

    await userEvent.type(within(modal).getByTestId('task-matrix-search'), 'guide')
    expect([numberOf('job:1'), numberOf('job:2')]).toEqual(['1', '2'])

    // Like a spreadsheet's gutter, a row number selects its row.
    fireEvent.click(within(screen.getByTestId('task-row-job:2')).getByTestId('task-row-number'))
    expect(selectedCount()).toBe('2 tasks selected')
  })

  it('a step header selects the whole column', async () => {
    await renderExpanded()
    fireEvent.click(screen.getByTestId('task-matrix-step-200'))
    expect(selectedCount()).toBe('3 tasks selected')
  })

  it('opens the matrix full screen', async () => {
    renderTab()
    await screen.findByTestId('task-step-sections')
    await userEvent.click(screen.getByTestId('task-matrix-expand'))
    const modal = await screen.findByTestId('task-matrix-modal')
    expect(within(modal).getByTestId('task-matrix')).toBeInTheDocument()
    // The modal's own toolbar has no second full-screen button.
    expect(within(modal).queryByTestId('task-matrix-expand')).not.toBeInTheDocument()
  })

  it('the expanded view always shows every filter chip, with no × and no filter icon', async () => {
    const { modal } = await renderExpanded()
    for (const id of ['status', 'file', 'language', 'assignee', 'due']) {
      expect(within(modal).getByTestId(`task-filter-${id}`)).toBeInTheDocument()
      expect(within(modal).queryByTestId(`task-filter-${id}-remove`)).not.toBeInTheDocument()
    }
    expect(within(modal).queryByTestId('task-filter-menu')).not.toBeInTheDocument()

    // Its chips still filter, through the same URL as the page.
    await userEvent.click(within(modal).getByTestId('task-filter-language'))
    await userEvent.click(await screen.findByRole('button', { name: 'fr' }))
    expect(within(modal).queryByTestId('task-row-job:1')).not.toBeInTheDocument()
    expect(within(modal).getByTestId('task-row-job:2')).toBeInTheDocument()

    // The page shows a chip only for what is set: Language, now.
    const page = screen.getAllByTestId('task-matrix-toolbar').find(t => !modal.contains(t))!
    expect(within(page).getByTestId('task-filter-language')).toHaveTextContent('fr')
    expect(within(page).queryByTestId('task-filter-status')).not.toBeInTheDocument()
  })

  it('only full screen has help text, at the far right, and the widest search', async () => {
    renderTab()
    await screen.findByTestId('task-step-sections')
    const page = screen.getByTestId('task-matrix-toolbar')
    // The width sits on the Input's frame, around the text field.
    const searchBox = (root: HTMLElement) => within(root).getByTestId('task-matrix-search').closest('[data-slot="input"]')
    expect(searchBox(page)).toHaveClass('w-80')
    expect(within(page).queryByTestId('task-matrix-help')).not.toBeInTheDocument()

    await userEvent.click(screen.getByTestId('task-matrix-expand'))
    const modal = await screen.findByTestId('task-matrix-modal')
    expect(searchBox(modal)).toHaveClass('w-96')
    expect(within(modal).getByTestId('task-matrix-help')).toHaveClass('ml-auto')
  })

  it('in full screen, Escape only clears a selection — wherever focus is — and closes once nothing is selected', async () => {
    renderTab()
    await screen.findByTestId('task-step-sections')
    await userEvent.click(screen.getByTestId('task-matrix-expand'))
    const modal = await screen.findByTestId('task-matrix-modal')

    // A step header is not focusable, and the search box is outside the grid:
    // focus is nowhere near a cell when Escape lands.
    fireEvent.click(within(modal).getByTestId('task-matrix-step-200'))
    expect(within(modal).getByTestId('bulk-selected-count')).toHaveTextContent('3 tasks selected')
    await userEvent.click(within(modal).getByTestId('task-matrix-search'))

    await userEvent.keyboard('{Escape}')
    expect(screen.getByTestId('task-matrix-modal')).toBeInTheDocument()
    expect(within(modal).queryByTestId('bulk-actions-bar')).not.toBeInTheDocument()

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('task-matrix-modal')).not.toBeInTheDocument())
  })

  it('in full screen, a selection the search has hidden does not hold Escape back', async () => {
    renderTab()
    await screen.findByTestId('task-step-sections')
    await userEvent.click(screen.getByTestId('task-matrix-expand'))
    const modal = await screen.findByTestId('task-matrix-modal')

    fireEvent.click(within(modal).getByTestId('task-cell-job:1|200')) // a de task
    await userEvent.type(within(modal).getByTestId('task-matrix-search'), 'fr')
    expect(within(modal).queryByTestId('bulk-actions-bar')).not.toBeInTheDocument()

    // Focus inside the grid, so the grid's own Escape handler sees it first.
    within(modal).getByTestId('task-cell-job:2|100').focus()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('task-matrix-modal')).not.toBeInTheDocument())
  })
})
