import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, cleanup } from '@testing-library/react'
import { LoadingPlaceholder } from './LoadingPlaceholder'

describe('LoadingPlaceholder', () => {
  // performance.now drives the handoff window; each test starts far past the
  // previous test's placeholders so they never count as a wait continuing.
  let now = 0
  beforeEach(() => {
    now += 60_000
    vi.spyOn(performance, 'now').mockImplementation(() => now)
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  // The rendered-output tests use delayMs={0} to render immediately; the delay
  // behavior itself is covered by its own test below.
  it('renders the default "Loading..." text', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} />)
    expect(getByText('Loading...')).toBeInTheDocument()
  })

  it('uses the standard muted text classes', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} />)
    const el = getByText('Loading...')
    expect(el.className).toContain('text-gray-500')
    expect(el.className).toContain('dark:text-gray-400')
  })

  it('renders as a <p> by default (keeping the block layout of the original)', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} />)
    const el = getByText('Loading...')
    expect(el.tagName).toBe('P')
  })

  it('allows overriding the message', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} message="Fetching entries..." />)
    expect(getByText('Fetching entries...')).toBeInTheDocument()
  })

  it('merges extra className with the base classes (e.g., for px-6 padding)', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} className="px-6" />)
    const el = getByText('Loading...')
    expect(el.className).toContain('px-6')
    expect(el.className).toContain('text-gray-500')
    expect(el.className).toContain('dark:text-gray-400')
  })

  it('supports the "sm" size variant used in dialogs/auth pages', () => {
    const { getByText } = render(<LoadingPlaceholder delayMs={0} size="sm" />)
    const el = getByText('Loading...')
    expect(el.className).toContain('text-sm')
  })

  it('renders a centered, spinning status region (no longer glued to the top-left)', () => {
    const { getByRole } = render(<LoadingPlaceholder delayMs={0} />)
    const status = getByRole('status')
    expect(status.className).toContain('justify-center')
    expect(status.querySelector('svg.animate-spin')).toBeInTheDocument()
  })

  it('stays hidden until the delay elapses, then appears (no flash on fast loads)', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const { queryByText } = render(<LoadingPlaceholder delayMs={1000} />)
      // Nothing rendered before the delay — a fast load that unmounts now never flashes.
      expect(queryByText('Loading...')).toBeNull()
      act(() => {
        vi.advanceTimersByTime(999)
      })
      expect(queryByText('Loading...')).toBeNull()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(queryByText('Loading...')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('LoadingPlaceholder handoff', () => {
  let now = 1_000_000
  beforeEach(() => {
    now += 60_000
    vi.spyOn(performance, 'now').mockImplementation(() => now)
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('shows at once when it takes over from a placeholder that is still showing', () => {
    const { rerender, queryByText } = render(<LoadingPlaceholder continues delayMs={0} message="Checking" />)
    // The page swaps one wait for the next: the new placeholder renders while
    // the old one is still mounted, as with a Suspense fallback inside a guard.
    rerender(<><LoadingPlaceholder continues delayMs={0} message="Checking" key="a" /><LoadingPlaceholder continues message="Loading editor…" key="b" /></>)
    rerender(<LoadingPlaceholder continues message="Loading editor…" key="b" />)
    expect(queryByText('Loading editor…')).toBeInTheDocument()
  })

  it('shows at once right after a visible placeholder went away', () => {
    const first = render(<LoadingPlaceholder continues delayMs={0} />)
    first.unmount()
    now += 100
    const { queryByText } = render(<LoadingPlaceholder continues message="Next" />)
    expect(queryByText('Next')).toBeInTheDocument()
  })

  it('waits its delay again once the previous wait is long over', () => {
    const first = render(<LoadingPlaceholder continues delayMs={0} />)
    first.unmount()
    now += 1_000
    const { queryByText } = render(<LoadingPlaceholder continues message="Later" />)
    expect(queryByText('Later')).toBeNull()
  })

  it('does not count a placeholder that never became visible', () => {
    const hidden = render(<LoadingPlaceholder continues message="Hidden" />)
    hidden.unmount()
    const { queryByText } = render(<LoadingPlaceholder continues message="Next" />)
    expect(queryByText('Next')).toBeNull()
  })
})

describe('LoadingPlaceholder outside the handoff', () => {
  let now = 2_000_000
  beforeEach(() => {
    now += 60_000
    vi.spyOn(performance, 'now').mockImplementation(() => now)
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('keeps its delay while an unrelated placeholder is showing', () => {
    // An always-visible inline spinner (say, a chart still loading) must not
    // make a dialog's spinner skip its delay and flash on a quick load.
    const { rerender, queryByText } = render(<LoadingPlaceholder delayMs={0} message="Chart" key="chart" />)
    rerender(
      <>
        <LoadingPlaceholder delayMs={0} message="Chart" key="chart" />
        <LoadingPlaceholder message="Dialog" key="dialog" />
      </>,
    )
    expect(queryByText('Chart')).toBeInTheDocument()
    expect(queryByText('Dialog')).toBeNull()
  })

  it('does not skip its own delay even next to a showing handoff placeholder', () => {
    const { rerender, queryByText } = render(<LoadingPlaceholder continues delayMs={0} message="Page" key="page" />)
    rerender(
      <>
        <LoadingPlaceholder continues delayMs={0} message="Page" key="page" />
        <LoadingPlaceholder message="Panel" key="panel" />
      </>,
    )
    expect(queryByText('Panel')).toBeNull()
  })
})
