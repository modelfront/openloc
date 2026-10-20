import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import Honeybadger from '@honeybadger-io/js'
import { initErrtrack, resetThrottle } from './errtrack'
import { ApiError } from '@/api/request'

vi.mock('@honeybadger-io/js', () => ({
  default: { configure: vi.fn(), beforeNotify: vi.fn(), notify: vi.fn() },
}))

function reject(reason: unknown) {
  window.dispatchEvent(Object.assign(new Event('unhandledrejection'), { reason }))
}

describe('unhandled promise rejections', () => {
  beforeAll(async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response(JSON.stringify({ error_reporting: true }))))
    await initErrtrack()
    vi.unstubAllGlobals()
  })
  beforeEach(() => {
    resetThrottle()
    vi.mocked(Honeybadger.notify).mockClear()
  })

  it('leaves the SDK hook off, so every rejection goes through shouldReport', () => {
    expect(vi.mocked(Honeybadger.configure).mock.calls[0][0]).toMatchObject({ enableUnhandledRejection: false })
  })

  it.each([
    ['Playground refusal', new ApiError(403, 'This is the Playground. Changes won’t be saved.', { error: { code: 'demo_read_only' } })],
    ['404', new ApiError(404, 'not found')],
    ['409', new ApiError(409, 'conflict')],
  ])('does not report an expected error: %s', (_, err) => {
    reject(err)
    expect(Honeybadger.notify).not.toHaveBeenCalled()
  })

  it.each([
    ['generic Error', new Error('something broke')],
    ['ApiError 400', new ApiError(400, 'bad request')],
    ['network failure', new ApiError(0, 'network')],
  ])('reports an unexpected error: %s', (_, err) => {
    reject(err)
    expect(Honeybadger.notify).toHaveBeenCalledTimes(1)
    expect(vi.mocked(Honeybadger.notify).mock.calls[0][0]).toBe(err)
  })
})
