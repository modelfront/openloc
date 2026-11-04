import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('@/lib/errtrack', async () => ({
  ...(await vi.importActual<typeof import('@/lib/errtrack')>('@/lib/errtrack')),
  notify: vi.fn(),
}))

import { notify } from '@/lib/errtrack'
import { ApiError } from '@/api/request'
import { reportSendFailure } from './chat-send-failure'

const notifyMock = vi.mocked(notify)

describe('reportSendFailure', () => {
  beforeEach(() => {
    notifyMock.mockReset()
  })

  it("shows an API error's own message and does not report it", () => {
    expect(reportSendFailure(new ApiError(400, 'The message is too long.'))).toBe('The message is too long.')
    expect(notifyMock).not.toHaveBeenCalled()
  })

  it('shows a plain sentence for anything else and reports the error', () => {
    const bug = new TypeError("Cannot read properties of undefined (reading 'path')")
    expect(reportSendFailure(bug)).toBe('Something went wrong. Try again.')
    expect(notifyMock).toHaveBeenCalledWith(bug, expect.objectContaining({ context: { action: 'chat-send' } }))
  })
})
