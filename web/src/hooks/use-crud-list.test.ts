import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { useCRUDList } from './use-crud-list'

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children)
}

const mockItems = [
  { id: 1, name: 'Item A' },
  { id: 2, name: 'Item B' },
]

describe('useCRUDList', () => {
  const listFn = vi.fn().mockResolvedValue(mockItems)
  const createFn = vi.fn().mockResolvedValue({ id: 3, name: 'New' })
  const renameFn = vi.fn().mockResolvedValue({ id: 1, name: 'Renamed' })
  const deleteFn = vi.fn().mockResolvedValue(undefined)

  beforeEach(() => { vi.clearAllMocks() })

  it('fetches and returns items', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-items'],
        queryFn: listFn,
        createFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    await waitFor(() => expect(result.current.items).toEqual(mockItems))
    expect(result.current.isLoading).toBe(false)
  })

  it('toggles showForm state', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-toggle'],
        queryFn: listFn,
        createFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    expect(result.current.showForm).toBe(false)
    act(() => result.current.setShowForm(true))
    expect(result.current.showForm).toBe(true)
  })

  it('createMut calls createFn and resets showForm on success', async () => {
    const onCreateSuccess = vi.fn()
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-create'],
        queryFn: listFn,
        createFn,
        deleteFn,
        entityName: 'Item',
        onCreateSuccess,
      }),
      { wrapper },
    )

    act(() => result.current.setShowForm(true))
    expect(result.current.showForm).toBe(true)

    await act(async () => {
      result.current.createMut.mutate('new item' as never)
    })

    await waitFor(() => expect(result.current.createMut.isSuccess).toBe(true))
    expect(createFn).toHaveBeenCalled()
    expect(result.current.showForm).toBe(false) // auto-closed
    expect(onCreateSuccess).toHaveBeenCalled()
  })

  it('manages inline rename state', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-rename'],
        queryFn: listFn,
        createFn,
        renameFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    expect(result.current.renamingId).toBeNull()

    const fakeEvent = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    act(() => result.current.startRename(fakeEvent, 1, 'Item A'))

    expect(result.current.renamingId).toBe(1)
    expect(result.current.renameValue).toBe('Item A')

    act(() => result.current.setRenameValue('Item A Updated'))
    expect(result.current.renameValue).toBe('Item A Updated')

    await act(async () => result.current.submitRename(1))

    await waitFor(() => expect(result.current.renamingId).toBeNull())
    expect(renameFn).toHaveBeenCalled()
    expect(renameFn.mock.calls[0][0]).toEqual({ id: 1, name: 'Item A Updated' })
  })

  it('cancelRename resets state without calling API', () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-cancel-rename'],
        queryFn: listFn,
        createFn,
        renameFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    const fakeEvent = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    act(() => result.current.startRename(fakeEvent, 2, 'Item B'))
    expect(result.current.renamingId).toBe(2)

    act(() => result.current.cancelRename())
    expect(result.current.renamingId).toBeNull()
    expect(renameFn).not.toHaveBeenCalled()
  })

  it('manages delete confirmation state', () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-delete'],
        queryFn: listFn,
        createFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    expect(result.current.deleteTarget).toBeNull()

    const fakeEvent = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    act(() => result.current.handleDelete(fakeEvent, 1, 'Item A'))

    expect(result.current.deleteTarget).toEqual({ id: 1, name: 'Item A' })
  })

  it('confirmDelete calls deleteFn with the target id', async () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-confirm-delete'],
        queryFn: listFn,
        createFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    const fakeEvent = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    act(() => result.current.handleDelete(fakeEvent, 2, 'Item B'))

    await act(async () => result.current.confirmDelete())

    await waitFor(() => {
      expect(deleteFn).toHaveBeenCalled()
      expect(deleteFn.mock.calls[0][0]).toBe(2)
    })
  })

  it('submitRename with empty string cancels rename', () => {
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-empty-rename'],
        queryFn: listFn,
        createFn,
        renameFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    const fakeEvent = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    act(() => result.current.startRename(fakeEvent, 1, 'Item A'))
    act(() => result.current.setRenameValue('   ')) // whitespace only

    act(() => result.current.submitRename(1))

    expect(result.current.renamingId).toBeNull()
    expect(renameFn).not.toHaveBeenCalled()
  })

  it('loadFailed is set only when the list has never loaded', async () => {
    const flaky = vi.fn()
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValueOnce(mockItems)
      .mockRejectedValueOnce(new Error('server unavailable'))
    const wrapper = createWrapper()
    const { result } = renderHook(
      () => useCRUDList({
        queryKey: ['test-load-failed'],
        queryFn: flaky,
        createFn,
        deleteFn,
        entityName: 'Item',
      }),
      { wrapper },
    )

    await waitFor(() => expect(result.current.loadFailed).toBe(true))
    expect((result.current.error as Error).message).toBe('server unavailable')

    act(() => result.current.refetch())
    await waitFor(() => expect(result.current.items).toEqual(mockItems))
    expect(result.current.loadFailed).toBe(false)

    // A failed background refetch keeps the cached rows on screen.
    act(() => result.current.refetch())
    await waitFor(() => expect(result.current.error).not.toBeNull())
    expect(result.current.items).toEqual(mockItems)
    expect(result.current.loadFailed).toBe(false)
  })
})
