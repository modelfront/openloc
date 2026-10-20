import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  TRAIL_MAX_EVENTS, actionTrail, clearActionTrail, clickTarget, installActionTrail, recordTrail, trailApiPath, trailText,
} from './action-trail'

const html = (markup: string) => {
  document.body.innerHTML = markup
  return document.body
}

beforeEach(() => clearActionTrail())

describe('recordTrail', () => {
  it('keeps the newest 50 events', () => {
    for (let i = 0; i < 70; i++) recordTrail({ kind: 'click', target: `Button ${i}` })
    const trail = actionTrail()
    expect(trail).toHaveLength(TRAIL_MAX_EVENTS)
    expect(trail[0]).toMatchObject({ target: 'Button 20' })
    expect(trail.at(-1)).toMatchObject({ target: 'Button 69' })
  })

  it('caps a click target at 60 characters and a message at 200', () => {
    recordTrail({ kind: 'click', target: 'x'.repeat(100) })
    recordTrail({ kind: 'console', message: 'e'.repeat(500) })
    const [click, error] = actionTrail()
    expect(click.kind === 'click' && click.target.length).toBe(60)
    expect(error.kind === 'console' && error.message.length).toBe(200)
  })

  // The support team's pages show other organizations' reports.
  it('records nothing on the support team’s pages', () => {
    window.history.replaceState({}, '', '/Platform/issues/12')
    try {
      recordTrail({ kind: 'click', target: 'Resolve' })
      recordTrail({ kind: 'page', path: '/Platform/issues/12' })
    } finally {
      window.history.replaceState({}, '', '/')
    }
    expect(actionTrail()).toEqual([])
  })
})

describe('clickTarget', () => {
  it('names a control by its label, else the nearest data-guide id', () => {
    html('<button id="a" aria-label="Close panel">×</button><div data-guide="editor.toolbar"><button id="b"><svg id="icon"></svg></button></div><a id="c" href="/x">Projects</a>')
    expect(clickTarget(document.getElementById('a'))).toBe('Close panel')
    expect(clickTarget(document.getElementById('icon'))).toBe('editor.toolbar')
    expect(clickTarget(document.getElementById('c'))).toBe('Projects')
  })

  it('records no typed text: not a text field, not a list option, nothing marked private', () => {
    html('<input id="t" type="text" value="secret" /><textarea id="ta">secret</textarea>'
      + '<div role="option" id="o">Add tag "secret"</div><div data-private><button id="p">secret</button></div>')
    expect(clickTarget(document.getElementById('t'))).toBeUndefined()
    expect(clickTarget(document.getElementById('ta'))).toBeUndefined()
    expect(clickTarget(document.getElementById('o'))).toBe('a list option')
    expect(clickTarget(document.getElementById('p'))).toBeUndefined()
  })
})

describe('trailApiPath', () => {
  it('keeps the path of a same-origin API call, without the query or a token', () => {
    expect(trailApiPath('/api/tms/3/entries?q=hello world')).toBe('/api/tms/3/entries')
    expect(trailApiPath('/api/chat/uploads/3f1c9a2e-0b4d-4c7e-9a51-6d2f8e7b1c04')).toBe('/api/chat/uploads/:token')
    expect(trailApiPath('/api/shared/charts/Zx81kQp0aLm3nB7vT2cR9w')).toBe('/api/shared/charts/:token')
    expect(trailApiPath('/api/auth/notification-preferences')).toBe('/api/auth/notification-preferences')
    expect(trailApiPath('https://api.anthropic.com/v1/messages')).toBeUndefined()
    expect(trailApiPath('/assets/app.js')).toBeUndefined()
  })
})

describe('installActionTrail', () => {
  const realFetch = window.fetch
  const realError = console.error
  let stop: () => void
  afterEach(() => {
    stop?.()
    window.fetch = realFetch
    console.error = realError
  })

  it('records failed API calls, console errors and clicks, then lets go', async () => {
    window.fetch = vi.fn(async (input: RequestInfo | URL) =>
      new Response('{}', { status: String(input).includes('fail') ? 500 : 200 })) as typeof fetch
    console.error = vi.fn()
    stop = installActionTrail()
    html('<button id="save">Save</button>')

    await fetch('/api/projects/1')
    await fetch('/api/projects/fail?x=1', { method: 'post' })
    console.error(new Error('boom'), 'at render')
    document.getElementById('save')!.click()
    expect(actionTrail().map(trailText)).toEqual([
      'POST /api/projects/fail failed (500)',
      'Console error: boom at render',
      'Clicked “Save”',
    ])

    stop()
    await fetch('/api/projects/fail')
    expect(actionTrail()).toHaveLength(3)
  })

  it('shares one recorder between nested installs', async () => {
    window.fetch = vi.fn(async () => new Response('', { status: 404 })) as typeof fetch
    const first = installActionTrail()
    const second = installActionTrail()
    await fetch('/api/x')
    expect(actionTrail()).toHaveLength(1)
    second()
    await fetch('/api/x')
    expect(actionTrail()).toHaveLength(2)
    first()
    stop = () => {}
  })
})
