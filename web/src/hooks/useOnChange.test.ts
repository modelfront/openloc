import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useOnChange } from './useOnChange'

function setup(initialDeps: readonly unknown[], options?: { onMount?: boolean }) {
  const onChange = vi.fn()
  const hook = renderHook(({ deps }) => useOnChange(deps, onChange, options), {
    initialProps: { deps: initialDeps },
  })
  return { onChange, ...hook }
}

describe('useOnChange', () => {
  it('does not run on mount by default', () => {
    const { onChange } = setup([1])
    expect(onChange).not.toHaveBeenCalled()
  })

  it('runs once on mount with onMount: true', () => {
    const { onChange, rerender } = setup([1], { onMount: true })
    expect(onChange).toHaveBeenCalledTimes(1)
    rerender({ deps: [1] })
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('runs once per changed dep, not on re-renders with the same deps', () => {
    const { onChange, rerender } = setup(['a', 1])
    rerender({ deps: ['b', 1] })
    expect(onChange).toHaveBeenCalledTimes(1)
    rerender({ deps: ['b', 1] })
    expect(onChange).toHaveBeenCalledTimes(1)
    rerender({ deps: ['b', 2] })
    expect(onChange).toHaveBeenCalledTimes(2)
  })

  it('compares with Object.is: the same values in a new array do not run it, NaN equals NaN', () => {
    const obj = { x: 1 }
    const { onChange, rerender } = setup([obj, NaN])
    rerender({ deps: [obj, NaN] })
    expect(onChange).not.toHaveBeenCalled()
    // Same shape, different object: a change.
    rerender({ deps: [{ x: 1 }, NaN] })
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('runs when the deps length changes', () => {
    const { onChange, rerender } = setup([1])
    rerender({ deps: [1, undefined] })
    expect(onChange).toHaveBeenCalledTimes(1)
    rerender({ deps: [1] })
    expect(onChange).toHaveBeenCalledTimes(2)
  })
})
