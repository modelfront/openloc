// The Tasks page only works if these URLs match what GET /api/tasks and
// /api/tasks/steps read (internal/job/handler_tasks.go); the page tests mock
// these functions, so the query strings are pinned here.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockRequest = vi.fn()
vi.mock('./request', () => ({
  request: (...a: unknown[]) => mockRequest(...a),
  getToken: () => null,
  BASE: '/api',
}))

import { listTasksPage, listTaskSteps } from './tasks'

const url = () => String(mockRequest.mock.calls[0][0])
const query = () => Object.fromEntries(new URLSearchParams(url().split('?')[1]))

beforeEach(() => {
  mockRequest.mockReset()
  mockRequest.mockResolvedValue({ data: [], cursor: '', has_more: false })
})

describe('listTasksPage', () => {
  it('first page: only the limit, no cursor', async () => {
    await listTasksPage({})
    expect(url().startsWith('/tasks?')).toBe(true)
    expect(query()).toEqual({ limit: '50' })
  })

  it('sends every filter and the cursor under the names the API reads', async () => {
    await listTasksPage({ q: 'manual', step: 'Review', deadline: 'overdue' }, 'inf:14', 20)
    expect(query()).toEqual({ cursor: 'inf:14', limit: '20', q: 'manual', step: 'Review', deadline: 'overdue' })
  })
})

describe('listTaskSteps', () => {
  it('reads the step names from /tasks/steps', async () => {
    mockRequest.mockResolvedValue({ data: ['Review', 'Translation'] })
    await expect(listTaskSteps()).resolves.toEqual(['Review', 'Translation'])
    expect(url()).toBe('/tasks/steps')
  })
})
