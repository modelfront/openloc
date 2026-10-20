import { describe, it, expect } from 'vitest'
import { ApiError } from './request'

describe('ApiError', () => {
  it('carries the HTTP status code on the error', () => {
    const err = new ApiError(409, 'translation was updated by another user')
    expect(err.status).toBe(409)
    expect(err.message).toBe('translation was updated by another user')
  })

  it('is an instance of Error (so existing catch blocks still work)', () => {
    const err = new ApiError(500, 'boom')
    expect(err).toBeInstanceOf(Error)
    expect(err).toBeInstanceOf(ApiError)
  })

  it('has a distinctive name for logging / error boundaries', () => {
    const err = new ApiError(404, 'not found')
    expect(err.name).toBe('ApiError')
  })
})

describe('demo_reset', () => {
  it('leaves the old demo, enters the new one and reloads — once', async () => {
    const { request } = await import('./request')
    const { vi } = await import('vitest')
    sessionStorage.clear()
    const calls: string[] = []
    const fetchMock = vi.fn(async (url: string) => {
      calls.push(url)
      if (url.endsWith('/auth/demo/leave') || url.endsWith('/auth/demo/enter')) {
        return new Response(JSON.stringify({ data: { token: `tok-${calls.length}` } }), { status: 200 })
      }
      return new Response(JSON.stringify({ error: { code: 'demo_reset', message: 'The Playground was refreshed.' } }), { status: 403 })
    })
    vi.stubGlobal('fetch', fetchMock)
    const reload = vi.fn()
    const original = window.location
    Object.defineProperty(window, 'location', { configurable: true, value: { ...original, reload } })
    try {
      await expect(request('/projects')).rejects.toThrow('refreshed')
      expect(calls).toEqual(['/api/projects', '/api/auth/demo/leave', '/api/auth/demo/enter'])
      expect(reload).toHaveBeenCalledTimes(1)
      // A second refusal right after does not loop.
      await expect(request('/projects')).rejects.toThrow()
      expect(reload).toHaveBeenCalledTimes(1)
    } finally {
      Object.defineProperty(window, 'location', { configurable: true, value: original })
      vi.unstubAllGlobals()
    }
  })
})
