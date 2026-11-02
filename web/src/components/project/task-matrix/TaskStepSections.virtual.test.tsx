// A long step section mounts only the rows near the screen: a project with no
// workflow has one section, open by default, holding every job. Runs the real
// TanStack virtualizer (ProjectTasksTab.test.tsx stubs it to mount all rows).

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

import { ProjectTasksTab } from './ProjectTasksTab'
import { makeJob } from '@/test/fixtures/job'

vi.mock('@/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api')>(),
  listProjectTasks: vi.fn().mockResolvedValue([]),
  listMembers: vi.fn().mockResolvedValue([]),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function renderJobs(count: number) {
  // jsdom has no layout: give every box (the page's scroller among them) a
  // 600px viewport.
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
    { top: 0, left: 0, right: 1000, bottom: 600, width: 1000, height: 600, x: 0, y: 0, toJSON: () => ({}) } as DOMRect)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
  const jobs = Array.from({ length: count }, (_, i) =>
    makeJob({ id: i + 1, file_id: i + 1, file_name: `file-${i + 1}.txt`, target_language: 'de', workflow_step: undefined }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <div style={{ overflowY: 'auto', height: 600 }}>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <ProjectTasksTab projectId={42} jobs={jobs} jobsUpdatedAt={0} isManager />
        </MemoryRouter>
      </QueryClientProvider>
    </div>,
  )
}

const taskRows = () => document.querySelectorAll('tr[data-testid^="task-row-"]')

describe('TaskStepSections — long sections', () => {
  it('mounts a window of a 300-row section, not every row', async () => {
    renderJobs(300)
    await screen.findByTestId('task-step-sections')
    await screen.findAllByTestId(/^task-row-/)
    const rows = taskRows()
    expect(rows.length).toBeGreaterThan(0)
    expect(rows.length).toBeLessThan(100)
  })

  it('mounts every row of a short section', async () => {
    renderJobs(20)
    await screen.findByTestId('task-step-sections')
    await screen.findAllByTestId(/^task-row-/)
    expect(taskRows()).toHaveLength(20)
  })
})
