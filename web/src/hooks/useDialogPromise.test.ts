import { describe, it, expect, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useDialogPromise } from './useDialogPromise'

function setup() {
  return renderHook(() => useDialogPromise<number>())
}

describe('useDialogPromise', () => {
  it('starts closed, opens on request and closes on settle', () => {
    const { result } = setup()
    expect(result.current.open).toBe(false)

    act(() => { void result.current.request() })
    expect(result.current.open).toBe(true)

    act(() => result.current.settle(5))
    expect(result.current.open).toBe(false)
  })

  it('resolves the request with the settled value', async () => {
    const { result } = setup()
    let pending!: Promise<number | null>
    act(() => { pending = result.current.request() })
    act(() => result.current.settle(5))
    await expect(pending).resolves.toBe(5)
  })

  it('resolves once: a settle(null) right after settle(5) changes nothing', async () => {
    const { result } = setup()
    const onResult = vi.fn()
    let pending!: Promise<void>
    act(() => { pending = result.current.request().then(onResult) })
    act(() => {
      result.current.settle(5)
      result.current.settle(null)
    })
    await pending
    expect(onResult).toHaveBeenCalledTimes(1)
    expect(onResult).toHaveBeenCalledWith(5)
  })

  it('resolves a second request while open with null; the first still gets the settled value', async () => {
    const { result } = setup()
    const onOpen = vi.fn()
    let first!: Promise<number | null>
    let second!: Promise<number | null>
    act(() => { first = result.current.request() })
    act(() => { second = result.current.request(onOpen) })

    await expect(second).resolves.toBeNull()
    expect(onOpen).not.toHaveBeenCalled()
    expect(result.current.open).toBe(true)

    act(() => result.current.settle(7))
    await expect(first).resolves.toBe(7)
  })

  it('runs onOpen when the dialog opens', () => {
    const { result } = setup()
    const onOpen = vi.fn()
    act(() => { void result.current.request(onOpen) })
    expect(onOpen).toHaveBeenCalledTimes(1)
  })

  it('closes at once on a promise settle and resolves when that promise does', async () => {
    const { result } = setup()
    let finish!: (v: number) => void
    const later = new Promise<number>((resolve) => { finish = resolve })
    const onResult = vi.fn()
    let pending!: Promise<void>
    act(() => { pending = result.current.request().then(onResult) })

    act(() => result.current.settle(later))
    expect(result.current.open).toBe(false)
    // A close arriving while that promise is pending must not resolve null.
    act(() => result.current.settle(null))
    await Promise.resolve()
    expect(onResult).not.toHaveBeenCalled()

    finish(9)
    await pending
    expect(onResult).toHaveBeenCalledTimes(1)
    expect(onResult).toHaveBeenCalledWith(9)
  })

  it('opens again after a settle', async () => {
    const { result } = setup()
    act(() => { void result.current.request() })
    act(() => result.current.settle(1))

    let again!: Promise<number | null>
    act(() => { again = result.current.request() })
    expect(result.current.open).toBe(true)
    act(() => result.current.settle(2))
    await expect(again).resolves.toBe(2)
  })
})
