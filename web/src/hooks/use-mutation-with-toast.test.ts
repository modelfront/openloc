import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { useMutationWithToast } from './use-mutation-with-toast'

// Mock sonner toast
vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

import { toast } from 'sonner'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children)
}

describe('useMutationWithToast', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows success toast and invalidates queries on success', async () => {
    const mutationFn = vi.fn().mockResolvedValue({ id: 1 })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      createElement(QueryClientProvider, { client: queryClient }, children)

    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Created!',
          invalidateKeys: [['items']],
        }),
      { wrapper },
    )

    await act(async () => {
      result.current.mutate(undefined as never)
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(toast.success).toHaveBeenCalledWith('Created!')
    expect(mutationFn).toHaveBeenCalled()
    expect(invalidateSpy).toHaveBeenCalledExactlyOnceWith({ queryKey: ['items'] })
  })

  it('shows error toast on failure', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error('Network error'))
    const wrapper = createWrapper()

    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Done',
          errorMessage: 'Failed',
        }),
      { wrapper },
    )

    await act(async () => {
      result.current.mutate(undefined as never)
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('Failed: Network error')
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('calls custom onSuccess callback', async () => {
    const onSuccess = vi.fn()
    const mutationFn = vi.fn().mockResolvedValue({ id: 42 })
    const wrapper = createWrapper()

    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Done',
          onSuccess,
        }),
      { wrapper },
    )

    await act(async () => {
      result.current.mutate(undefined as never)
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(onSuccess).toHaveBeenCalledWith({ id: 42 }, undefined)
  })

  it('shows raw error message when no errorMessage prefix given', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error('Forbidden'))
    const wrapper = createWrapper()

    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Done',
        }),
      { wrapper },
    )

    await act(async () => {
      result.current.mutate(undefined as never)
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(toast.error).toHaveBeenCalledWith('Forbidden')
  })

  it('errorToast: false leaves the rejection to the caller and still rejects mutateAsync', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error('Forbidden'))
    const wrapper = createWrapper()

    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Done',
          errorToast: false,
        }),
      { wrapper },
    )

    await act(async () => {
      await expect(result.current.mutateAsync(undefined as never)).rejects.toThrow('Forbidden')
    })

    expect(toast.error).not.toHaveBeenCalled()
  })
})

describe('useMutationWithToast per-call details', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('passes the variables to successMessage and onSuccess', async () => {
    const onSuccess = vi.fn()
    const { result } = renderHook(
      () =>
        useMutationWithToast<number, string>({
          mutationFn: async (name) => name.length,
          successMessage: (n, name) => `${name}: ${n}`,
          onSuccess,
        }),
      { wrapper: createWrapper() },
    )
    await act(async () => { result.current.mutate('abc') })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(toast.success).toHaveBeenCalledExactlyOnceWith('abc: 3')
    expect(onSuccess).toHaveBeenCalledExactlyOnceWith(3, 'abc')
  })

  it('errorToast as a function skips the toast only for the errors it rejects', async () => {
    const onError = vi.fn()
    const mutationFn = vi.fn()
      .mockRejectedValueOnce(new Error('locked'))
      .mockRejectedValueOnce(new Error('broken'))
    const { result } = renderHook(
      () =>
        useMutationWithToast({
          mutationFn,
          successMessage: 'Done',
          errorToast: err => err.message !== 'locked',
          onError,
        }),
      { wrapper: createWrapper() },
    )
    await act(async () => { result.current.mutate(undefined) })
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1))
    expect(toast.error).not.toHaveBeenCalled()

    await act(async () => { result.current.mutate(undefined) })
    await waitFor(() => expect(onError).toHaveBeenCalledTimes(2))
    expect(toast.error).toHaveBeenCalledExactlyOnceWith('broken')
  })
})

describe('useMutationWithToast in the demo organization', () => {
  it('leaves the demo refusal to the shared notice (no second toast)', async () => {
    vi.clearAllMocks()
    const { ApiError } = await import('../api')
    const mutationFn = vi.fn().mockRejectedValue(new ApiError(403, 'refused', { error: { code: 'demo_read_only' } }))
    const { result } = renderHook(() => useMutationWithToast({ mutationFn, successMessage: 'Saved' }), { wrapper: createWrapper() })
    await act(async () => { result.current.mutate(undefined) })
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(toast.error).not.toHaveBeenCalled()
  })
})
