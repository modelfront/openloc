// EventSource for tests: jsdom ships none. Records every instance so a test
// can push server events (`emit`), drop the connection (`fail`) and check
// what was opened and closed.

export class MockEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: MockEventSource[] = []
  url: string
  withCredentials: boolean
  closed = false
  readyState: number = MockEventSource.CONNECTING
  listeners = new Map<string, Array<(e: MessageEvent) => void>>()
  onopen: ((e: Event) => void) | null = null
  onerror: ((e: Event) => void) | null = null

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = !!init?.withCredentials
    MockEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, [])
    this.listeners.get(type)!.push(fn)
  }

  /** The server pushes an event. */
  emit(type: string, data: unknown) {
    const listeners = this.listeners.get(type) ?? []
    for (const l of listeners) l(new MessageEvent(type, { data: JSON.stringify(data) }))
  }

  /** The server answers 200: the stream is open. */
  connect() {
    this.readyState = MockEventSource.OPEN
    this.onopen?.(new Event('open'))
  }

  /**
   * The connection drops. The browser retries on its own, unless the server
   * answered with something other than 200: then it gives up (`giveUp`).
   */
  fail({ giveUp = false }: { giveUp?: boolean } = {}) {
    this.readyState = giveUp ? MockEventSource.CLOSED : MockEventSource.CONNECTING
    if (giveUp) this.closed = true
    this.onerror?.(new Event('error'))
  }

  close() {
    this.closed = true
  }
}

type WithEventSource = { EventSource?: typeof MockEventSource }

/** Installs the mock as the global EventSource; pair with uninstall. */
export function installMockEventSource() {
  MockEventSource.instances = []
  ;(globalThis as unknown as WithEventSource).EventSource = MockEventSource
}

export function uninstallMockEventSource() {
  delete (globalThis as unknown as WithEventSource).EventSource
}
