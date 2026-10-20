import { describe, it, expect, vi } from 'vitest'
import { ApiError } from '../api'
import { DEMO_READ_ONLY_MESSAGE, goToDemo, isDemoReadOnly, notifyDemoReadOnly, tourFromHash, tourPath } from './demo'

vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  enterDemo: () => Promise.resolve(),
}))

const refused = new ApiError(403, 'This is the Playground. Changes won’t be saved.', { error: { code: 'demo_read_only' } })

describe('demo read-only refusal', () => {
  it('recognizes only the demo refusal, not other 403s', () => {
    expect(isDemoReadOnly(refused)).toBe(true)
    expect(isDemoReadOnly(new ApiError(403, 'forbidden', { error: { code: 'forbidden' } }))).toBe(false)
    expect(isDemoReadOnly(new ApiError(400, 'bad', { error: { code: 'demo_read_only' } }))).toBe(false)
    expect(isDemoReadOnly(new Error('x'))).toBe(false)
  })

  it('shows one notice for a refusal and nothing for other errors', () => {
    const show = vi.fn()
    expect(notifyDemoReadOnly(refused, show)).toBe(true)
    expect(show).toHaveBeenCalledWith(DEMO_READ_ONLY_MESSAGE)
    show.mockClear()
    expect(notifyDemoReadOnly(new ApiError(500, 'boom'), show)).toBe(false)
    expect(show).not.toHaveBeenCalled()
  })
})

describe('goToDemo', () => {
  // A tour lands on its entry address, which shows no organization's data.
  it('lands on the tour entry address, or on the project list without a tour', async () => {
    const loc = { href: '' }
    vi.stubGlobal('location', loc)
    try {
      await goToDemo('tour-ai-prompts')
      expect(loc.href).toBe('/tour/tour-ai-prompts/1')
      await goToDemo()
      expect(loc.href).toBe('/projects')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

describe('tour addresses', () => {
  it('builds the entry address with the step counted from 1', () => {
    expect(tourPath('demo-overview')).toBe('/tour/demo-overview/1')
    expect(tourPath('demo-overview', 4)).toBe('/tour/demo-overview/5')
  })

  it('reads a running tour from the hash, and nothing else', () => {
    expect(tourFromHash('#tour=demo-overview&step=3')).toEqual({ tour: 'demo-overview', start: 2 })
    expect(tourFromHash('#tour=demo-overview')).toEqual({ tour: 'demo-overview', start: 0 })
    expect(tourFromHash('#section-2')).toBeNull()
    expect(tourFromHash('')).toBeNull()
  })
})
