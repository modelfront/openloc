import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { SAVED_FLASH_MS, useSavedFlash } from './useSavedFlash'

describe('useSavedFlash', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('shows "saved" for SAVED_FLASH_MS after a flash', () => {
    const { result } = renderHook(() => useSavedFlash())
    expect(result.current[0]).toBe(false)
    act(() => result.current[1]())
    expect(result.current[0]).toBe(true)
    act(() => { vi.advanceTimersByTime(SAVED_FLASH_MS - 1) })
    expect(result.current[0]).toBe(true)
    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current[0]).toBe(false)
  })

  it('restarts the wait on every save', () => {
    const { result } = renderHook(() => useSavedFlash(1000))
    act(() => result.current[1]())
    act(() => { vi.advanceTimersByTime(800) })
    act(() => result.current[1]())
    act(() => { vi.advanceTimersByTime(800) })
    expect(result.current[0]).toBe(true)
    act(() => { vi.advanceTimersByTime(200) })
    expect(result.current[0]).toBe(false)
  })

  it('leaves no timer behind on unmount', () => {
    const { result, unmount } = renderHook(() => useSavedFlash())
    act(() => result.current[1]())
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
