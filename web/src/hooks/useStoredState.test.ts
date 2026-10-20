import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStoredState } from './useStoredState'

const KEY = 'test-stored-state'
const asBool = (stored: unknown) => stored === true
const asMode = (stored: string | null) => (stored === 'write' ? 'write' : 'read')

beforeEach(() => localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('useStoredState (JSON)', () => {
  it('reads the stored value on mount', () => {
    localStorage.setItem(KEY, 'true')
    const { result } = renderHook(() => useStoredState(KEY, asBool))
    expect(result.current[0]).toBe(true)
  })

  it('writes every change back as JSON', () => {
    const { result } = renderHook(() => useStoredState(KEY, asBool))
    expect(result.current[0]).toBe(false)
    act(() => result.current[1](v => !v))
    expect(result.current[0]).toBe(true)
    expect(localStorage.getItem(KEY)).toBe('true')
  })

  it('hands parse null for an entry that is not JSON', () => {
    localStorage.setItem(KEY, 'write')
    const parse = vi.fn(asBool)
    renderHook(() => useStoredState(KEY, parse))
    expect(parse).toHaveBeenCalledWith(null)
  })
})

describe('useStoredState (raw)', () => {
  it('reads a bare stored string as-is', () => {
    localStorage.setItem(KEY, 'write')
    const { result } = renderHook(() => useStoredState(KEY, asMode, { raw: true }))
    expect(result.current[0]).toBe('write')
  })

  it('writes the string without JSON quotes', () => {
    const { result } = renderHook(() => useStoredState(KEY, asMode, { raw: true }))
    act(() => result.current[1]('write'))
    expect(localStorage.getItem(KEY)).toBe('write')
  })

  it('hands parse null when nothing is stored', () => {
    const { result } = renderHook(() => useStoredState(KEY, stored => stored ?? 'none', { raw: true }))
    expect(result.current[0]).toBe('none')
  })
})

describe('useStoredState with blocked storage', () => {
  it('falls back to the parsed default and keeps working in memory', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    const { result } = renderHook(() => useStoredState(KEY, asBool))
    expect(result.current[0]).toBe(false)
    act(() => result.current[1](true))
    expect(result.current[0]).toBe(true)
  })
})
