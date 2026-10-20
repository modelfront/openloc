import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/request'
import { serverFieldError } from './server-field-error'

describe('serverFieldError', () => {
  it('reads the field the server named', () => {
    const err = new ApiError(409, 'Taken.', { error: { code: 'Conflict', message: 'Taken.', field: 'name' } })
    expect(serverFieldError(err)).toEqual({ field: 'name', message: 'Taken.' })
  })

  it('keeps the message when no field is named', () => {
    expect(serverFieldError(new ApiError(500, 'Boom.', { error: { message: 'Boom.' } }))).toEqual({ message: 'Boom.' })
    expect(serverFieldError(new Error('Offline.'))).toEqual({ message: 'Offline.' })
  })

  it('is null without an error', () => {
    expect(serverFieldError(null)).toBeNull()
  })
})
