import { describe, it, expect, vi, afterEach } from 'vitest'

vi.mock('html-to-image', () => ({ toJpeg: vi.fn(() => new Promise<string>(() => {})) }))

import { toJpeg } from 'html-to-image'
import { capturePage, keptInCapture } from './screenshot'

afterEach(() => {
  vi.useRealTimers()
  window.history.replaceState({}, '', '/')
})

describe('capturePage', () => {
  // The library can wait forever on an image it cannot decode.
  it('gives up after 15 seconds instead of hanging', async () => {
    vi.useFakeTimers()
    const capture = capturePage()
    const outcome = expect(capture).rejects.toThrow('Taking the screenshot took too long.')
    await vi.advanceTimersByTimeAsync(15_000)
    await outcome
  })

  it('leaves out everything marked data-screenshot="exclude"', async () => {
    vi.mocked(toJpeg).mockResolvedValueOnce('data:image/jpeg;base64,/9j/')
    await capturePage()
    const filter = vi.mocked(toJpeg).mock.lastCall?.[1]?.filter
    expect(filter).toBe(keptInCapture)

    const marked = document.createElement('div')
    marked.dataset.screenshot = 'exclude'
    expect(keptInCapture(marked)).toBe(false)
    expect(keptInCapture(document.createElement('div'))).toBe(true)
  })

  // The support team's pages show other organizations' reports.
  it('refuses on the support team’s pages, without rendering anything', async () => {
    vi.mocked(toJpeg).mockClear()
    window.history.replaceState({}, '', '/platform/issues/12')
    await expect(capturePage()).rejects.toThrow('Screenshots are not taken on the support team’s pages.')
    expect(toJpeg).not.toHaveBeenCalled()
  })
})
