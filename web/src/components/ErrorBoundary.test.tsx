import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'

// notifyThen included because transitively-imported lazy-reload.ts binds it
// from the same mocked module — without it that import is undefined.
vi.mock('@/lib/errtrack', () => ({
  notify: vi.fn(),
  notifyThen: vi.fn(),
}))
import { notify } from '@/lib/errtrack'

function Bomb({ message = 'kaboom' }: { message?: string }): never {
  throw new Error(message)
}

describe('ErrorBoundary', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // React logs caught render errors; keep test output clean.
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders the fallback with the default title instead of a blank page', () => {
    render(
      <ErrorBoundary>
        <Bomb />
      </ErrorBoundary>,
    )
    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    expect(screen.getByText('kaboom')).toBeInTheDocument()
  })

  it('uses a custom title when provided', () => {
    render(
      <ErrorBoundary title="CAT editor crashed">
        <Bomb />
      </ErrorBoundary>,
    )
    expect(screen.getByText('CAT editor crashed')).toBeInTheDocument()
  })

  it('reports render errors as ReactRenderError with the component stack', () => {
    render(
      <ErrorBoundary title="CAT editor crashed">
        <Bomb />
      </ErrorBoundary>,
    )
    expect(notify).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'kaboom' }),
      expect.objectContaining({
        name: 'ReactRenderError',
        context: expect.objectContaining({
          boundary: 'CAT editor crashed',
          componentStack: expect.any(String),
        }),
      }),
    )
  })

  it('reports chunk-load errors under their own class — the reload budget is spent by the time they get here', () => {
    render(
      <ErrorBoundary>
        <Bomb message="Failed to fetch dynamically imported module: /assets/CATEditor-abc.js" />
      </ErrorBoundary>,
    )
    expect(notify).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ name: 'ChunkLoadError' }),
    )
  })

  it('Retry resets the boundary and re-renders children', () => {
    let shouldThrow = true
    function Flaky() {
      if (shouldThrow) throw new Error('once')
      return <div>recovered</div>
    }
    render(
      <ErrorBoundary>
        <Flaky />
      </ErrorBoundary>,
    )
    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    shouldThrow = false
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(screen.getByText('recovered')).toBeInTheDocument()
  })

  it('renders children untouched when nothing throws', () => {
    render(
      <ErrorBoundary>
        <div>all good</div>
      </ErrorBoundary>,
    )
    expect(screen.getByText('all good')).toBeInTheDocument()
    expect(notify).not.toHaveBeenCalled()
  })
})
