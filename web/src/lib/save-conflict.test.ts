import { describe, it, expect } from 'vitest'
import { isSaveConflictError, isSegmentHeldError } from './save-conflict'
import { ApiError } from '../api/request'

describe('isSaveConflictError', () => {
  it('returns true for ApiError with status 409', () => {
    expect(isSaveConflictError(new ApiError(409, 'translation was updated by another user'))).toBe(true)
  })

  it('returns false for ApiError with other 4xx status', () => {
    expect(isSaveConflictError(new ApiError(400, 'bad request'))).toBe(false)
    expect(isSaveConflictError(new ApiError(403, 'forbidden'))).toBe(false)
    expect(isSaveConflictError(new ApiError(404, 'not found'))).toBe(false)
  })

  it('returns false for ApiError with 5xx status', () => {
    expect(isSaveConflictError(new ApiError(500, 'boom'))).toBe(false)
    expect(isSaveConflictError(new ApiError(503, 'unavailable'))).toBe(false)
  })

  it('returns false for plain Error (no status property)', () => {
    expect(isSaveConflictError(new Error('plain error'))).toBe(false)
  })

  it('returns false for non-Error values', () => {
    expect(isSaveConflictError(null)).toBe(false)
    expect(isSaveConflictError(undefined)).toBe(false)
    expect(isSaveConflictError('string error')).toBe(false)
    expect(isSaveConflictError({ status: 409 })).toBe(false) // not an ApiError instance
    expect(isSaveConflictError(409)).toBe(false)
  })

  // A task-edit guard refusal is a 409 too, but there is no newer row to put
  // in the conflict banner. Treating it as a conflict swallowed it silently.
  it.each(['task_errored', 'ai_step_running', 'task_not_started'])(
    'returns false for a task-lock 409 (%s)',
    (code) => {
      expect(isSaveConflictError(new ApiError(409, 'locked', { error: { code, message: 'locked' } }))).toBe(false)
    },
  )

  it('still returns true for a version conflict that carries the conflict row', () => {
    const body = { error: { message: 'updated by another user' }, conflict: { id: 5 } }
    expect(isSaveConflictError(new ApiError(409, 'updated by another user', body))).toBe(true)
  })

  it('regression guard: a string-typed status of "409" must NOT match', () => {
    // If someone refactors ApiError.status from number to string, this test
    // catches it. The instanceof check passes, but the strict === comparison
    // prevents the false positive that would break optimistic locking.
    const fakeApiError = new ApiError(409, 'msg')
    // @ts-expect-error — deliberately corrupting the type for the regression test
    fakeApiError.status = '409'
    expect(isSaveConflictError(fakeApiError)).toBe(false)
  })
})

describe('isSegmentHeldError', () => {
  const held = () => new ApiError(409, 'Ana is editing this segment.', {
    error: { code: 'segment_held', message: 'Ana is editing this segment.' },
    holder: { id: 7, name: 'Ana' },
  })

  it('matches a 409 with code segment_held, which is then not a version conflict', () => {
    expect(isSegmentHeldError(held())).toBe(true)
    expect(isSaveConflictError(held())).toBe(false)
  })

  it('does not match a version conflict or another 409', () => {
    expect(isSegmentHeldError(new ApiError(409, 'translation was updated by another user'))).toBe(false)
    expect(isSegmentHeldError(new ApiError(409, 'x', { error: { code: 'task_completed' } }))).toBe(false)
    expect(isSegmentHeldError(new ApiError(400, 'x', { error: { code: 'segment_held' } }))).toBe(false)
  })
})

