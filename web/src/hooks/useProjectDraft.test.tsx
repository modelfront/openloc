// useProjectDraft — the New Project wizard's autosave.
// Asserts on the requests the hook makes: one POST that creates the draft,
// PUTs after that, collapsed by the debounce, flushed on demand, and none at
// all while a resumed draft is hydrating.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { ProjectCreatePayload } from '../api'

const api = vi.hoisted(() => ({
  createProject: vi.fn(),
  saveProjectDraft: vi.fn(),
  deleteProject: vi.fn(),
}))
vi.mock('../api', () => api)
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

import { useProjectDraft, DRAFT_SAVE_DELAY_MS } from './useProjectDraft'

const payload = (name: string): ProjectCreatePayload => ({ name, source_language: 'en', target_languages: ['fr'] })

// The hook invalidates the project-list caches after each save, so it needs a
// client in context.
function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

// Runs the debounce timer AND the promise chain behind it.
async function settle(ms = DRAFT_SAVE_DELAY_MS) {
  await act(async () => {
    vi.advanceTimersByTime(ms)
    await Promise.resolve()
    await Promise.resolve()
  })
}

describe('useProjectDraft', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    api.createProject.mockResolvedValue({ id: 42, status: 'draft' })
    api.saveProjectDraft.mockResolvedValue({ id: 42, status: 'draft' })
    api.deleteProject.mockResolvedValue(undefined)
    window.history.replaceState(null, '', '/projects/new')
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('creates the draft once on the first save, then replays with PUT', async () => {
    let name = 'A'
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload(name) }), { wrapper })

    act(() => { result.current.scheduleSave() })
    expect(api.createProject).not.toHaveBeenCalled()
    await settle()

    expect(api.createProject).toHaveBeenCalledTimes(1)
    expect(api.createProject).toHaveBeenCalledWith({ ...payload('A'), status: 'draft' })
    expect(result.current.draftId).toBe(42)
    expect(result.current.state).toBe('saved')
    expect(window.location.search).toBe('?draft=42')

    name = 'B'
    act(() => { result.current.scheduleSave() })
    await settle()
    expect(api.createProject).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).toHaveBeenCalledWith(42, payload('B'))
  })

  it('collapses rapid edits into one request', async () => {
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

    act(() => {
      result.current.scheduleSave()
      result.current.scheduleSave()
      result.current.scheduleSave()
    })
    await settle(DRAFT_SAVE_DELAY_MS - 1)
    expect(api.createProject).not.toHaveBeenCalled()
    await settle(1)
    expect(api.createProject).toHaveBeenCalledTimes(1)
  })

  it('flushPending fires a pending save immediately and is a no-op otherwise', async () => {
    const { result } = renderHook(() => useProjectDraft({ initialId: 7, buildPayload: () => payload('x') }), { wrapper })
    act(() => { result.current.endHydration() })

    act(() => { result.current.flushPending() })
    expect(api.saveProjectDraft).not.toHaveBeenCalled()

    act(() => { result.current.scheduleSave() })
    act(() => { result.current.flushPending() })
    await settle(0)
    expect(api.saveProjectDraft).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).toHaveBeenCalledWith(7, payload('x'))
  })

  it('ignores changes while a resumed draft is hydrating, then saves again', async () => {
    const { result } = renderHook(() => useProjectDraft({ initialId: 7, buildPayload: () => payload('x') }), { wrapper })

    act(() => { result.current.scheduleSave() })
    await settle()
    expect(api.saveProjectDraft).not.toHaveBeenCalled()
    expect(api.createProject).not.toHaveBeenCalled()

    act(() => { result.current.endHydration() })
    act(() => { result.current.scheduleSave() })
    await settle()
    expect(api.saveProjectDraft).toHaveBeenCalledWith(7, payload('x'))
  })

  it('ensureDraft creates the draft when there is none and reuses it after', async () => {
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

    let id: number | undefined
    await act(async () => { id = await result.current.ensureDraft() })
    expect(id).toBe(42)
    expect(api.createProject).toHaveBeenCalledTimes(1)

    await act(async () => { id = await result.current.ensureDraft() })
    expect(id).toBe(42)
    expect(api.createProject).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).not.toHaveBeenCalled()
  })

  // The "Start from" choices rewrite the form before the
  // user has typed anything, so they use this instead of scheduleSave.
  it('scheduleSaveExisting saves an open draft but never creates one', async () => {
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

    act(() => { result.current.scheduleSaveExisting() })
    await settle()
    expect(api.createProject).not.toHaveBeenCalled()
    expect(api.saveProjectDraft).not.toHaveBeenCalled()

    // Once the draft exists, the same call keeps it in step.
    await act(async () => { await result.current.ensureDraft() })
    expect(api.createProject).toHaveBeenCalledTimes(1)
    act(() => { result.current.scheduleSaveExisting() })
    await settle()
    expect(api.saveProjectDraft).toHaveBeenCalledWith(42, payload('x'))
  })

  describe('discardDraft', () => {
    it('deletes the draft, drops ?draft= and leaves the hook ready for a new one', async () => {
      const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

      await act(async () => { await result.current.ensureDraft() })
      expect(window.location.search).toBe('?draft=42')

      await act(async () => { await result.current.discardDraft() })
      expect(api.deleteProject).toHaveBeenCalledWith(42)
      expect(result.current.draftId).toBeNull()
      expect(result.current.state).toBe('idle')
      expect(window.location.search).toBe('')

      // The next save creates a second draft rather than PUTting the dead id.
      api.createProject.mockResolvedValue({ id: 43, status: 'draft' })
      act(() => { result.current.scheduleSave() })
      await settle()
      expect(api.createProject).toHaveBeenCalledTimes(2)
      expect(api.saveProjectDraft).not.toHaveBeenCalled()
    })

    it('with no draft yet, deletes nothing', async () => {
      const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })
      await act(async () => { await result.current.discardDraft() })
      expect(api.deleteProject).not.toHaveBeenCalled()
    })

    // The create is what makes the draft; discarding while it is still in
    // flight has to delete the row it produces, not skip it.
    it('waits for an in-flight create, then deletes what it made', async () => {
      let resolveCreate: (v: unknown) => void = () => {}
      api.createProject.mockImplementationOnce(() => new Promise(r => { resolveCreate = r }))
      const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

      act(() => { void result.current.saveNow() })
      await settle(0)
      expect(api.createProject).toHaveBeenCalledTimes(1)

      let discarded: Promise<void> | undefined
      act(() => { discarded = result.current.discardDraft() })
      expect(api.deleteProject).not.toHaveBeenCalled()

      await act(async () => {
        resolveCreate({ id: 42, status: 'draft' })
        await discarded
      })
      expect(api.deleteProject).toHaveBeenCalledWith(42)
      expect(result.current.draftId).toBeNull()
    })

    // The draft exists either way; keeping the id means the next save
    // overwrites it instead of leaving it orphaned in the list.
    it('keeps the draft id when the delete fails', async () => {
      api.deleteProject.mockRejectedValueOnce(new Error('nope'))
      const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

      await act(async () => { await result.current.ensureDraft() })
      await act(async () => { await result.current.discardDraft() })

      expect(result.current.draftId).toBe(42)
      act(() => { result.current.scheduleSave() })
      await settle()
      expect(api.saveProjectDraft).toHaveBeenCalledWith(42, payload('x'))
      expect(api.createProject).toHaveBeenCalledTimes(1)
    })
  })

  it('serializes saves so a second save waits for the create', async () => {
    let resolveCreate: (v: unknown) => void = () => {}
    api.createProject.mockImplementationOnce(() => new Promise(r => { resolveCreate = r }))
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

    let first: Promise<number> | undefined
    let second: Promise<number> | undefined
    act(() => {
      first = result.current.saveNow()
      second = result.current.saveNow()
    })
    await settle(0)
    // Only one create in flight; the second save has not decided yet.
    expect(api.createProject).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).not.toHaveBeenCalled()

    await act(async () => {
      resolveCreate({ id: 42, status: 'draft' })
      await first
      await second
    })
    expect(api.createProject).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).toHaveBeenCalledTimes(1)
    expect(api.saveProjectDraft).toHaveBeenCalledWith(42, payload('x'))
  })

  it('reports an error state when the save fails', async () => {
    api.createProject.mockRejectedValueOnce(new Error('boom'))
    const { result } = renderHook(() => useProjectDraft({ initialId: null, buildPayload: () => payload('x') }), { wrapper })

    act(() => { result.current.scheduleSave() })
    await settle()
    expect(result.current.state).toBe('error')
    expect(result.current.draftId).toBeNull()
  })
})
