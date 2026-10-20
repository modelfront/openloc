import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { openEventStream } from './event-stream'
import { MockEventSource, installMockEventSource, uninstallMockEventSource } from '../test/mock-event-source'

let visibility: DocumentVisibilityState = 'visible'
let focused = true

function setProtocol(nextHopProtocol: string) {
  vi.spyOn(performance, 'getEntriesByType').mockReturnValue([{ nextHopProtocol }] as unknown as PerformanceEntryList)
}
function setFocus(f: boolean) {
  focused = f
  window.dispatchEvent(new Event(f ? 'focus' : 'blur'))
}
const openStreams = () => MockEventSource.instances.filter(es => !es.closed)

// Every stream a test opens is closed after it, so no test's focus or
// visibility listeners reach the next one.
const closers: Array<() => void> = []
function start(...args: Parameters<typeof openEventStream>) {
  const close = openEventStream(...args)
  closers.push(close)
  return close
}

beforeEach(() => {
  installMockEventSource()
  vi.useFakeTimers()
  visibility = 'visible'
  focused = true
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
  vi.spyOn(document, 'hasFocus').mockImplementation(() => focused)
})
afterEach(() => {
  for (const close of closers.splice(0)) close()
  vi.useRealTimers()
  vi.restoreAllMocks()
  uninstallMockEventSource()
})

// A proxy's 502 during a deploy, or a 204 while the server cannot stream, makes
// the browser give up on the stream for good.
describe('openEventStream after the browser gives up', () => {
  it('opens the stream again after a pause that doubles', () => {
    setProtocol('h2')
    start('/x/stream', {})
    MockEventSource.instances[0].fail({ giveUp: true })
    vi.advanceTimersByTime(2_999)
    expect(MockEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(MockEventSource.instances).toHaveLength(2)

    MockEventSource.instances[1].fail({ giveUp: true })
    vi.advanceTimersByTime(5_999)
    expect(MockEventSource.instances).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(openStreams()).toHaveLength(1)
    expect(MockEventSource.instances).toHaveLength(3)
  })

  it('starts the pause over once a stream opens', () => {
    setProtocol('h2')
    start('/x/stream', {})
    MockEventSource.instances[0].fail({ giveUp: true })
    vi.advanceTimersByTime(3_000)
    MockEventSource.instances[1].connect()
    MockEventSource.instances[1].fail({ giveUp: true })
    vi.advanceTimersByTime(3_000)
    expect(MockEventSource.instances).toHaveLength(3)
  })

  it('leaves a dropped stream to the browser, which retries by itself', () => {
    setProtocol('h2')
    const onDown = vi.fn()
    start('/x/stream', {}, { onDown })
    MockEventSource.instances[0].fail()
    vi.advanceTimersByTime(60_000)
    expect(MockEventSource.instances).toHaveLength(1)
    expect(onDown).toHaveBeenCalledTimes(1)
  })

  it('waits at most 60 s between attempts', () => {
    setProtocol('h2')
    start('/x/stream', {})
    for (const pause of [3_000, 6_000, 12_000, 24_000, 48_000, 60_000, 60_000]) {
      const opened = MockEventSource.instances.length
      MockEventSource.instances[opened - 1].fail({ giveUp: true })
      vi.advanceTimersByTime(pause - 1)
      expect(MockEventSource.instances).toHaveLength(opened)
      vi.advanceTimersByTime(1)
      expect(MockEventSource.instances).toHaveLength(opened + 1)
    }
  })

  it('opens nothing more once closed', () => {
    setProtocol('h2')
    const close = start('/x/stream', {})
    MockEventSource.instances[0].fail({ giveUp: true })
    close()
    vi.advanceTimersByTime(60_000)
    expect(MockEventSource.instances).toHaveLength(1)
  })
})

// Over HTTP/1.1 every open stream holds one of the browser's ~6 connections
// per host, shared by every tab: a stream that only the window being worked
// in needs is held by that window alone.
describe('openEventStream onlyWhileFocused', () => {
  it('holds the stream only while the window has focus on HTTP/1.1', () => {
    setProtocol('http/1.1')
    const onDown = vi.fn()
    start('/x/stream', {}, { onlyWhileFocused: true, onDown })
    expect(openStreams()).toHaveLength(1)

    setFocus(false)
    expect(openStreams()).toHaveLength(0)
    expect(onDown).toHaveBeenCalledTimes(1)

    setFocus(true)
    expect(openStreams()).toHaveLength(1)
    expect(MockEventSource.instances).toHaveLength(2)
  })

  it('waits for focus in a window opened without it', () => {
    setProtocol('http/1.1')
    focused = false
    start('/x/stream', {}, { onlyWhileFocused: true })
    expect(MockEventSource.instances).toHaveLength(0)
    setFocus(true)
    expect(openStreams()).toHaveLength(1)
  })

  it('stops following focus once closed', () => {
    setProtocol('http/1.1')
    const close = start('/x/stream', {}, { onlyWhileFocused: true })
    close()
    setFocus(false)
    setFocus(true)
    expect(openStreams()).toHaveLength(0)
  })

  it('keeps the stream in a window without focus on HTTP/2', () => {
    setProtocol('h2')
    start('/x/stream', {}, { onlyWhileFocused: true })
    setFocus(false)
    expect(openStreams()).toHaveLength(1)
  })

  it('leaves a stream without the option open in a window without focus', () => {
    setProtocol('http/1.1')
    start('/x/stream', {})
    setFocus(false)
    expect(openStreams()).toHaveLength(1)
  })
})
