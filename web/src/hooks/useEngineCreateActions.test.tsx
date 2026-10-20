// useEngineCreateActions — the pinned "New MT engine" / "New AI engine" rows.
//
// The behaviour worth pinning is the return-refresh: the app runs with
// `refetchOnWindowFocus: false`, so an engine created in the other tab would be
// invisible in the still-open project form unless we invalidate explicitly when
// the user comes back. It has to be one-shot too — otherwise every later focus
// refetches the lists forever.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useEngineCreateActions } from './useEngineCreateActions'

const navigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router-dom')>()),
  useNavigate: () => navigate,
}))

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidate = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  )
  return { ...renderHook(() => useEngineCreateActions(), { wrapper }), invalidate }
}

const invalidatedKeys = (spy: { mock: { calls: Parameters<QueryClient['invalidateQueries']>[] } }) =>
  spy.mock.calls.map(c => c[0]?.queryKey)

let openSpy: ReturnType<typeof vi.spyOn>

// Emulate the spec: `noopener` / `noreferrer` suppress the handle, so
// window.open returns NULL even when the tab opened fine. Pins that the hook
// opens without them, so null means blocked.
const browserLikeOpen = (_url: string, _target?: string, features?: string) =>
  (features && /noopener|noreferrer/.test(features) ? null : ({} as Window))

beforeEach(() => {
  navigate.mockClear()
  openSpy = vi.spyOn(window, 'open').mockImplementation(browserLikeOpen as typeof window.open)
})
afterEach(() => vi.restoreAllMocks())

describe('useEngineCreateActions', () => {
  it('opens the engines page in a new tab and leaves the current page alone', () => {
    const { result } = setup()
    act(() => result.current.mtEngine().onSelect())

    expect(openSpy).toHaveBeenCalledWith('/mt/engines', '_blank')
    // The whole point of the new tab is that the in-progress form survives.
    expect(navigate).not.toHaveBeenCalled()
  })

  it('opens the Engines tab in a new tab and leaves the current page alone', () => {
    const { result } = setup()
    act(() => result.current.aiEngine().onSelect())

    expect(openSpy).toHaveBeenCalledWith('/ai/engines/new', '_blank')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('opens the Routers tab in a new tab and leaves the current page alone', () => {
    const { result } = setup()
    act(() => result.current.aiRouter().onSelect())

    expect(openSpy).toHaveBeenCalledWith('/ai/new', '_blank')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('labels default to "New MT engine" / "New AI engine" and accept an override', () => {
    const { result } = setup()
    expect(result.current.mtEngine().label).toBe('New MT engine')
    expect(result.current.aiEngine().label).toBe('New AI engine')
    expect(result.current.aiEngine('New AI translation engine').label).toBe('New AI translation engine')
    expect(result.current.aiRouter().label).toBe('New AI router')
    expect(result.current.mtRouter().label).toBe('New MT router')
  })

  it('refreshes the engine and router lists when the user returns to the tab', () => {
    const { result, invalidate } = setup()
    act(() => result.current.mtEngine().onSelect())
    invalidate.mockClear()

    act(() => { window.dispatchEvent(new Event('focus')) })

    expect(invalidatedKeys(invalidate)).toEqual(
      expect.arrayContaining([['mt-engines'], ['ai-engines'], ['ai-routers'], ['mt-routers']]),
    )
  })

  it('does not refresh on focus when no create trip was started', () => {
    const { invalidate } = setup()
    act(() => { window.dispatchEvent(new Event('focus')) })
    expect(invalidate).not.toHaveBeenCalled()
  })

  it('is one-shot: a later focus does not refetch again', () => {
    const { result, invalidate } = setup()
    act(() => result.current.aiEngine().onSelect())
    act(() => { window.dispatchEvent(new Event('focus')) })
    invalidate.mockClear()

    act(() => { window.dispatchEvent(new Event('focus')) })

    expect(invalidate).not.toHaveBeenCalled()
  })

  it('unmounting mid-trip drops the listener', () => {
    const { result, invalidate, unmount } = setup()
    act(() => result.current.mtEngine().onSelect())
    unmount()
    invalidate.mockClear()

    act(() => { window.dispatchEvent(new Event('focus')) })

    expect(invalidate).not.toHaveBeenCalled()
  })

  it('falls back to in-app navigation when the popup is genuinely blocked', () => {
    openSpy.mockReturnValue(null)
    const { result, invalidate } = setup()
    act(() => result.current.aiEngine().onSelect())

    expect(navigate).toHaveBeenCalledWith('/ai/engines/new')
    // No new tab to come back from, so no listener is armed.
    invalidate.mockClear()
    act(() => { window.dispatchEvent(new Event('focus')) })
    expect(invalidate).not.toHaveBeenCalled()
  })
})

// Every picker that points at something made elsewhere links to that thing's
// CREATE form, not to its list. A picker's whole point is that the author
// already knows what they want to make.
describe('useEngineCreateActions — create routes', () => {
  it.each([
    ['aiEngine', '/ai/engines/new'],
    ['aiRouter', '/ai/new'],
    ['stepDefinition', '/workflows/steps/new'],
  ] as const)('%s opens %s', async (which, path) => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    const { result } = setup()

    result.current[which]().onSelect()

    expect(openSpy).toHaveBeenCalledWith(path, '_blank')
    openSpy.mockRestore()
  })

  it('sends a prompt to the section its output flags put it in', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    const { result } = setup()

    result.current.prompt('qe_ape').onSelect()
    expect(openSpy).toHaveBeenLastCalledWith('/ai/prompts/new', '_blank')

    result.current.prompt('mt').onSelect()
    expect(openSpy).toHaveBeenLastCalledWith('/mt/prompts/new', '_blank')
    openSpy.mockRestore()
  })
})
