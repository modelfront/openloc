import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useSegmentEditingTimer } from './useSegmentEditingTimer'

// Editing time is reported once, on the confirm that ends it. Time from a
// visit that ends without a confirm, or from a confirm the server refused,
// belongs to the next confirm, not to nobody. The time is kept per
// translation, outside the row, so a row that unmounts between visits keeps
// it. Each test uses its own translation id.
describe('useSegmentEditingTimer', () => {
  let now = 1_000_000
  beforeEach(() => {
    now = 1_000_000
    vi.spyOn(Date, 'now').mockImplementation(() => now)
  })
  afterEach(() => vi.restoreAllMocks())

  it('a visit without a confirm counts toward the next confirm', () => {
    const { result } = renderHook(() => useSegmentEditingTimer(1))
    result.current.start()
    now += 4_000
    result.current.stop()
    now += 60_000 // on another segment
    result.current.start()
    now += 2_000
    expect(result.current.consume()).toBe(6_000)
  })

  it('a visit survives the row unmounting before the next visit', () => {
    const first = renderHook(() => useSegmentEditingTimer(2))
    first.result.current.start()
    now += 30_000
    first.result.current.stop()
    first.unmount()
    now += 120_000 // scrolled away; the row is not mounted
    const second = renderHook(() => useSegmentEditingTimer(2))
    second.result.current.start()
    now += 60_000
    expect(second.result.current.consume()).toBe(90_000)
  })

  it('an unmount during a visit closes the window at the unmount', () => {
    const first = renderHook(() => useSegmentEditingTimer(3))
    first.result.current.start()
    now += 10_000
    first.unmount()
    now += 50_000
    const second = renderHook(() => useSegmentEditingTimer(3))
    second.result.current.start()
    now += 5_000
    expect(second.result.current.consume()).toBe(15_000)
  })

  it('one segment never reports another segment’s time', () => {
    const a = renderHook(() => useSegmentEditingTimer(4))
    const b = renderHook(() => useSegmentEditingTimer(5))
    a.result.current.start()
    now += 7_000
    a.result.current.stop()
    b.result.current.start()
    now += 1_000
    expect(b.result.current.consume()).toBe(1_000)
    expect(a.result.current.consume()).toBe(7_000)
  })

  it('restore hands a refused confirm its time back', () => {
    const { result } = renderHook(() => useSegmentEditingTimer(6))
    result.current.start()
    now += 5_000
    const refused = result.current.consume()
    expect(refused).toBe(5_000)
    result.current.restore(refused)
    now += 3_000
    expect(result.current.consume()).toBe(8_000)
  })

  it('start() while already running keeps the open window', () => {
    const { result } = renderHook(() => useSegmentEditingTimer(7))
    result.current.start()
    now += 3_000
    result.current.start()
    now += 2_000
    expect(result.current.consume()).toBe(5_000)
  })

  it('a confirm starts the next window from zero', () => {
    const { result } = renderHook(() => useSegmentEditingTimer(8))
    result.current.start()
    now += 5_000
    result.current.consume()
    now += 1_000
    expect(result.current.consume()).toBe(1_000)
  })

  it('consume() while stopped does not start counting', () => {
    const { result } = renderHook(() => useSegmentEditingTimer(9))
    result.current.start()
    now += 2_000
    result.current.stop()
    expect(result.current.consume()).toBe(2_000)
    now += 30_000 // not active
    expect(result.current.consume()).toBe(0)
  })
})
