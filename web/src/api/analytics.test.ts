// The workflow step actions send what the server reads: the advance body's
// `comment` is the hand-off note for the next step (internal/workflow
// HandleAdvanceStep), the reject body's `reason` the sent-back reason.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockRequest = vi.fn()
vi.mock('./request', () => ({
  request: (...a: unknown[]) => mockRequest(...a),
  getToken: () => null,
  BASE: '/api',
}))

import { advanceJobStep, mergeApplyResponses, rejectJobStep } from './analytics'

beforeEach(() => {
  mockRequest.mockReset()
  mockRequest.mockResolvedValue({ data: { status: 'advanced' } })
})

describe('advanceJobStep', () => {
  it('sends the comment for the next step', async () => {
    await advanceJobStep(7, 'Please check terminology')
    expect(mockRequest).toHaveBeenCalledWith('/jobs/7/workflow/advance', {
      method: 'POST', body: JSON.stringify({ comment: 'Please check terminology' }),
    })
  })

  it('without a comment, sends an empty one', async () => {
    await advanceJobStep(7)
    expect(mockRequest).toHaveBeenCalledWith('/jobs/7/workflow/advance', {
      method: 'POST', body: JSON.stringify({ comment: '' }),
    })
  })
})

describe('rejectJobStep', () => {
  it('sends the reason, the target step and the re-run choice', async () => {
    await rejectJobStep(7, { reason: 'Wrong terms', toStepId: 41, rerunAI: true })
    expect(mockRequest).toHaveBeenCalledWith('/jobs/7/workflow/reject', {
      method: 'POST', body: JSON.stringify({ reason: 'Wrong terms', to_step_id: 41, rerun_ai: true }),
    })
  })
})

describe('mergeApplyResponses', () => {
  it('adds up the segments each chunk skipped because someone else has them open', () => {
    const merged = mergeApplyResponses([
      { message: 'ok', job_id: 7, held: 2 },
      { message: 'ok', job_id: 7 },
      { message: 'ok', job_id: 7, held: 1 },
    ])
    expect(merged.held).toBe(3)
  })
})
