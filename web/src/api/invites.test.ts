// The Members tab asks for the pending invites only; the server leaves the
// accepted ones out (internal/invite HandleList, ?status=pending).
import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockRequest = vi.fn()
vi.mock('./request', () => ({
  request: (...a: unknown[]) => mockRequest(...a),
  getToken: () => null,
  setToken: () => {},
  BASE: '/api',
}))

import { listInvites } from './auth'

beforeEach(() => {
  mockRequest.mockReset()
  mockRequest.mockResolvedValue({ data: [] })
})

describe('listInvites', () => {
  it('asks the server for the pending invites only', async () => {
    await listInvites({ status: 'pending' })
    expect(mockRequest).toHaveBeenCalledWith('/invites?status=pending')
  })

  it('without a status, asks for every invite', async () => {
    await listInvites()
    expect(mockRequest).toHaveBeenCalledWith('/invites')
  })
})
