import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return { ...actual, listTasksPage: vi.fn(), listTaskSteps: vi.fn(), getMe: vi.fn() }
})

import { getMe, listTasksPage, listTaskSteps, type Task, type TasksPage as Page, type UserProfile } from '../api'
import TasksPage from './TasksPage'
import { chipTextClass } from '@/components/ui/chip'

const listTasksPageMock = vi.mocked(listTasksPage)
vi.mocked(listTaskSteps).mockResolvedValue(['Translation', 'Review'])
const getMeMock = vi.mocked(getMe)

function asRole(role: string) {
  getMeMock.mockResolvedValue({ id: 1, role } as UserProfile)
}
asRole('linguist')

function page(data: Task[], next = ''): Page {
  return { data, cursor: next, has_more: next !== '' }
}

function task(jobId: number, deadline: string | null): Task {
  return {
    job_id: jobId,
    project_id: 1,
    project_name: 'Demo Project',
    file_id: jobId,
    file_name: `file-${jobId}.docx`,
    source_language: 'en',
    target_language: 'fr',
    status: 'translating',
    current_step: 'translation',
    step_name: 'Translation',
    customer_name: null,
    word_count: 100,
    translated_count: 0,
    total_count: 10,
    deadline,
  }
}

function renderPage(url = '/tasks') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/tasks" element={<TasksPage />} />
          <Route path="/projects/:projectId/jobs/:jobId/edit" element={<div data-testid="editor-route" />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Each deadline shows its exact time next to the relative part, because a
// linguist plans their day by the hour.
describe('TasksPage deadline', () => {
  it('shows the relative deadline and the exact time', async () => {
    const inThreeDays = new Date(Date.now() + 3 * 24 * 60 * 60 * 1000 - 60 * 60 * 1000).toISOString()
    listTasksPageMock.mockResolvedValue(page([task(1, inThreeDays)]))

    renderPage()

    const row = await screen.findByTestId('task-row-1')
    const deadline = within(row).getByTestId('deadline-pill')
    expect(deadline.closest('[data-slot="chip"]'), 'deadline is plain text, not a chip').toBeNull()
    expect(deadline.textContent).toMatch(/Due in \d+d/)
    expect(deadline.textContent).toMatch(/·.*\d{1,2}:\d{2}/)
  })

  it('shows an overdue deadline in red, one due within a day in amber, a later one in grey', async () => {
    const yesterday = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString()
    const inThreeHours = new Date(Date.now() + 3 * 60 * 60 * 1000).toISOString()
    const nextWeek = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString()
    listTasksPageMock.mockResolvedValue(page([task(3, yesterday), task(4, nextWeek), task(5, inThreeHours)]))

    renderPage()

    const overdue = within(await screen.findByTestId('task-row-3')).getByTestId('deadline-pill')
    const future = within(screen.getByTestId('task-row-4')).getByTestId('deadline-pill')
    expect(overdue.textContent).toMatch(/^Overdue/)
    expect(overdue).toHaveClass(...chipTextClass('red').split(' '))
    expect(future).not.toHaveClass(chipTextClass('red').split(' ')[0])
    expect(future).toHaveClass('text-gray-500')

    const soon = within(screen.getByTestId('task-row-5')).getByTestId('deadline-pill')
    expect(soon).toHaveClass(...chipTextClass('yellow').split(' '))
    expect(soon).not.toHaveClass('text-gray-500')
  })

  it('says "No due date" for a task without a deadline', async () => {
    listTasksPageMock.mockResolvedValue(page([task(2, null)]))

    renderPage()

    const row = await screen.findByTestId('task-row-2')
    expect(within(row).getByTestId('deadline-pill').textContent).toBe('No due date')
  })
})

describe('TasksPage opens the CAT editor in a new tab', () => {
  afterEach(() => vi.restoreAllMocks())

  it('a card click opens the editor in a new tab and keeps the list', async () => {
    listTasksPageMock.mockResolvedValue(page([task(3, null)]))
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)

    renderPage()
    fireEvent.click(await screen.findByTestId('task-row-3'))

    expect(open).toHaveBeenCalledTimes(1)
    expect(open).toHaveBeenCalledWith('/projects/1/jobs/3/edit', '_blank')
    expect(screen.getByTestId('task-row-3')).toBeInTheDocument()
    expect(screen.queryByTestId('editor-route')).not.toBeInTheDocument()
  })

  it('a blocked popup falls back to opening the editor in this tab', async () => {
    listTasksPageMock.mockResolvedValue(page([task(4, null)]))
    vi.spyOn(window, 'open').mockReturnValue(null)

    renderPage()
    fireEvent.click(await screen.findByTestId('task-row-4'))

    expect(await screen.findByTestId('editor-route')).toBeInTheDocument()
  })

  it('the file name is a new-tab link, and clicking it does not open a second tab', async () => {
    listTasksPageMock.mockResolvedValue(page([task(5, null)]))
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)

    renderPage()
    const link = await screen.findByTestId('task-open-5')
    expect(link).toHaveAttribute('href', '/projects/1/jobs/5/edit')
    expect(link).toHaveAttribute('target', '_blank')
    // One tab stop: nothing inside the link takes focus on its own.
    expect(link.querySelector('[tabindex]')).toBeNull()

    fireEvent.click(link)
    expect(open).not.toHaveBeenCalled()
  })
})

// The editor tab changes the task (approves segments, completes the step), and
// the app does not refetch on window focus, so this list refetches itself
// once when the user comes back.
describe('TasksPage refreshes after a trip to the editor tab', () => {
  afterEach(() => vi.restoreAllMocks())
  const comeBack = () => act(() => { window.dispatchEvent(new Event('focus')) })

  it('a card click, then coming back to this tab, fetches the tasks again — once', async () => {
    listTasksPageMock.mockReset().mockResolvedValue(page([task(6, null)]))
    vi.spyOn(window, 'open').mockReturnValue({} as Window)

    renderPage()
    fireEvent.click(await screen.findByTestId('task-row-6'))
    expect(listTasksPageMock).toHaveBeenCalledTimes(1)

    comeBack()
    await waitFor(() => expect(listTasksPageMock).toHaveBeenCalledTimes(2))
    comeBack()
    await new Promise(r => setTimeout(r, 0))
    expect(listTasksPageMock).toHaveBeenCalledTimes(2)
  })

  it('the file-name link arms the refresh too', async () => {
    listTasksPageMock.mockReset().mockResolvedValue(page([task(7, null)]))

    renderPage()
    fireEvent.click(await screen.findByTestId('task-open-7'))
    comeBack()
    await waitFor(() => expect(listTasksPageMock).toHaveBeenCalledTimes(2))
  })

  it('focus without opening the editor does not refetch', async () => {
    listTasksPageMock.mockReset().mockResolvedValue(page([task(8, null)]))

    renderPage()
    await screen.findByTestId('task-row-8')
    comeBack()
    await new Promise(r => setTimeout(r, 0))
    expect(listTasksPageMock).toHaveBeenCalledTimes(1)
  })
})

describe('TasksPage card content', () => {
  afterEach(() => asRole('linguist'))

  it('shows project, customer, languages, step and progress', async () => {
    listTasksPageMock.mockResolvedValue(page([{ ...task(9, null), customer_name: 'Acme', translated_count: 4 }]))

    renderPage()

    const card = await screen.findByTestId('task-row-9')
    expect(within(card).getByTestId('task-open-9')).toHaveTextContent('file-9.docx')
    expect(within(card).getByTestId('task-project')).toHaveTextContent(/^Demo Project\s*·\s*Acme$/)
    expect(card).toHaveTextContent(/en.*fr/i)
    expect(within(card).getByTestId('task-step')).toHaveTextContent(/^Step:\s*Translation$/)
    expect(within(card).getByTestId('task-progress')).toHaveTextContent('4 / 10 segments')
  })

  it('falls back to the word count before segments are counted', async () => {
    listTasksPageMock.mockResolvedValue(page([{ ...task(10, null), total_count: null }]))

    renderPage()

    expect(within(await screen.findByTestId('task-row-10')).getByTestId('task-progress')).toHaveTextContent('100 words')
  })

  it('formats counts: singular noun, thousands separators', async () => {
    listTasksPageMock.mockResolvedValue(page([
      { ...task(23, null), total_count: null, word_count: 1 },
      { ...task(24, null), translated_count: 1500, total_count: 2400 },
    ]))

    renderPage()

    expect(within(await screen.findByTestId('task-row-23')).getByTestId('task-progress')).toHaveTextContent(/^1 word$/)
    expect(within(screen.getByTestId('task-row-24')).getByTestId('task-progress'))
      .toHaveTextContent(`${(1500).toLocaleString()} / ${(2400).toLocaleString()} segments`)
  })

  it('names the assignee when the API sends one (owner/admin view)', async () => {
    listTasksPageMock.mockResolvedValue(page([{ ...task(11, null), assignee_name: 'Jane Doe' }]))

    renderPage()

    expect(within(await screen.findByTestId('task-row-11')).getByTestId('task-assignee')).toHaveTextContent('Assignee: Jane Doe')
  })

  it('shows no assignee for a linguist\'s own list', async () => {
    listTasksPageMock.mockResolvedValue(page([task(12, null)]))

    renderPage()

    expect(within(await screen.findByTestId('task-row-12')).queryByTestId('task-assignee')).not.toBeInTheDocument()
  })
})

describe('TasksPage filters and paging', () => {
  afterEach(() => { listTasksPageMock.mockReset(); asRole('linguist') })

  it('sends the URL filters to the API', async () => {
    listTasksPageMock.mockResolvedValue(page([task(13, null)]))

    renderPage('/tasks?q=manual&step=Review&deadline=overdue')

    await screen.findByTestId('task-row-13')
    expect(listTasksPageMock).toHaveBeenCalledWith({ step: 'Review', deadline: 'overdue', q: 'manual' }, '', 50)
    expect(screen.getByTestId('filter-step')).toHaveTextContent('Review')
    expect(screen.getByTestId('filter-deadline')).toHaveTextContent('Overdue')
    expect(screen.getByTestId('task-search')).toHaveValue('manual')
  })

  it('Load more fetches the next page with the returned cursor and appends it', async () => {
    listTasksPageMock
      .mockResolvedValueOnce(page([task(14, null)], 'inf:14'))
      .mockResolvedValueOnce(page([task(15, null)]))

    renderPage()

    await screen.findByTestId('task-row-14')
    fireEvent.click(screen.getByTestId('load-more-tasks'))
    await screen.findByTestId('task-row-15')
    expect(listTasksPageMock).toHaveBeenLastCalledWith({}, 'inf:14', 50)
    expect(screen.getByTestId('task-row-14')).toBeInTheDocument()
    expect(screen.queryByTestId('load-more-tasks')).not.toBeInTheDocument()
  })

  it('says no task matches when a filter is set', async () => {
    listTasksPageMock.mockResolvedValue(page([]))

    renderPage('/tasks?deadline=none')

    expect(await screen.findByText('No tasks match these filters.')).toBeInTheDocument()
  })

  it('an owner with no open tasks reads about the organization, not "assigned to you"', async () => {
    asRole('owner')
    listTasksPageMock.mockResolvedValue(page([]))

    renderPage()

    expect(await screen.findByText('No open tasks in your organization yet.')).toBeInTheDocument()
  })

  it('a linguist with no tasks reads the "assigned to you" message', async () => {
    listTasksPageMock.mockResolvedValue(page([]))

    renderPage()

    expect(await screen.findByText(/No tasks assigned to you yet/)).toBeInTheDocument()
  })
})

// A failed load must say so: on a vendor's home page, "No tasks assigned to
// you" for a server error reads as "you have no work".
describe('TasksPage when the list fails to load', () => {
  afterEach(() => { listTasksPageMock.mockReset(); asRole('linguist') })

  it('shows the error with a Retry, not the empty-list message', async () => {
    listTasksPageMock
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValueOnce(page([task(20, null)]))

    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the tasks: server unavailable")
    expect(screen.queryByText(/No tasks assigned to you yet/)).not.toBeInTheDocument()

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('task-row-20')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a failed Load more keeps the loaded cards and says the next page failed', async () => {
    listTasksPageMock
      .mockResolvedValueOnce(page([task(21, null)], 'inf:21'))
      .mockRejectedValueOnce(new Error('timeout'))
      .mockResolvedValueOnce(page([task(22, null)]))

    renderPage()

    await screen.findByTestId('task-row-21')
    fireEvent.click(screen.getByTestId('load-more-tasks'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the next tasks: timeout")
    expect(screen.getByTestId('task-row-21')).toBeInTheDocument()

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('task-row-22')).toBeInTheDocument()
  })
})
