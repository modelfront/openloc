import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('./screenshot', () => ({
  capturePage: vi.fn(async () => new File(['jpeg'], 'screenshot.jpg', { type: 'image/jpeg' })),
}))

import { capturePage } from './screenshot'
import { clearActionTrail, installActionTrail, recordTrail } from './action-trail'
import {
  RECORDING_MAX_MS, clearHeldRecording, heldRecording, recordableField, startRecording, type HeldRecording,
} from './watch-recording'

const type = (el: HTMLInputElement | HTMLTextAreaElement, value: string) => {
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

beforeEach(() => {
  clearActionTrail()
  clearHeldRecording()
  vi.mocked(capturePage).mockClear()
})

describe('recordableField', () => {
  it('never records a password, card or one-time-code field, nor anything marked private', () => {
    document.body.innerHTML = `
      <input id="name" type="text" />
      <input id="pw" type="password" />
      <input id="cc" autocomplete="cc-number" />
      <input id="cvc" name="card_cvc" />
      <input id="otp" autocomplete="one-time-code" />
      <div data-private><input id="private" type="text" /></div>
      <textarea id="notes"></textarea>`
    const el = (id: string) => document.getElementById(id)!
    expect(recordableField(el('name'))).toBe(true)
    expect(recordableField(el('notes'))).toBe(true)
    for (const id of ['pw', 'cc', 'cvc', 'otp', 'private']) expect(recordableField(el(id)), id).toBe(false)
  })
})

describe('startRecording', () => {
  const realFetch = window.fetch
  let stopTrail: () => void
  afterEach(() => {
    stopTrail?.()
    window.fetch = realFetch
    vi.useRealTimers()
  })

  it('records typed text, the trail and failed responses, with one screenshot at the first error', async () => {
    window.fetch = vi.fn(async () => new Response('{"error":"' + 'x'.repeat(3000) + '"}', { status: 500 })) as typeof fetch
    stopTrail = installActionTrail()
    document.body.innerHTML = `<label for="q">Search</label><input id="q" type="text" />
      <input id="pw" type="password" /><button id="go">Export</button>`
    const done = new Promise<HeldRecording>(resolve => {
      const stop = startRecording(resolve)
      const q = document.getElementById('q') as HTMLInputElement
      type(q, 'ex')
      type(q, 'export job 9')
      type(document.getElementById('pw') as HTMLInputElement, 'hunter2')
      document.getElementById('go')!.click()
      void fetch('/api/projects/9/export', { method: 'POST' })
        .then(() => fetch('/api/projects/9/export', { method: 'POST' }))
        .then(() => new Promise(r => setTimeout(r, 0)))
        .then(stop)
    })
    const held = await done
    const events = held.recording.events
    expect(events.map(e => e.kind)).toEqual(['input', 'click', 'api', 'api'])
    expect(events[0]).toMatchObject({ target: 'Search', value: 'export job 9' })
    expect(JSON.stringify(held.recording)).not.toContain('hunter2')
    expect(events[2].kind === 'api' && events[2].body?.length).toBe(2048)
    expect(capturePage).toHaveBeenCalledTimes(1)
    expect((await held.screenshot)?.name).toBe('recording-first-error.jpg')
    expect(heldRecording()).toBe(held)
  })

  it('records nothing after it stops, and stops by itself after 3 minutes', async () => {
    vi.useFakeTimers()
    const onStop = vi.fn()
    startRecording(onStop)
    recordTrail({ kind: 'click', target: 'Inside' })
    await vi.advanceTimersByTimeAsync(RECORDING_MAX_MS)
    expect(onStop).toHaveBeenCalledTimes(1)
    recordTrail({ kind: 'click', target: 'After' })
    const held = onStop.mock.calls[0][0] as HeldRecording
    expect(held.recording.events.map(e => e.kind === 'click' && e.target)).toEqual(['Inside'])
    expect(await held.screenshot).toBeUndefined()
  })

  it('records nothing on the support team’s pages', async () => {
    window.fetch = vi.fn(async () => new Response('{"error":"other org"}', { status: 500 })) as typeof fetch
    stopTrail = installActionTrail()
    document.body.innerHTML = '<input id="q" type="text" /><button id="resolve">Resolve</button>'
    window.history.replaceState({}, '', '/platform/Issues/12')
    try {
      const stop = startRecording(() => {})
      type(document.getElementById('q') as HTMLInputElement, 'other org title')
      document.getElementById('resolve')!.click()
      console.error('other org error')
      await fetch('/api/platform/issues/12')
      stop()
    } finally {
      window.history.replaceState({}, '', '/')
    }
    expect(heldRecording()?.recording.events).toEqual([])
    expect(capturePage).not.toHaveBeenCalled()
  })
})
