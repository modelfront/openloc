import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useAutosaveDraft } from './useAutosaveDraft'
import { DRAFT_SAVE_DELAY_MS } from './useProjectDraft'

// A 0–100 integer, null otherwise.
const parsePct = (raw: string) => {
  if (raw.trim() === '') return null
  const n = Number(raw)
  return Number.isInteger(n) && n >= 0 && n <= 100 ? n : null
}

function setup(stored = '10', instant = false) {
  const onSave = vi.fn()
  const hook = renderHook(
    ({ stored }) => useAutosaveDraft({ stored, parse: parsePct, format: String, onSave, instant }),
    { initialProps: { stored } },
  )
  return { onSave, hook }
}

describe('useAutosaveDraft', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('saves after the pause while the field keeps focus', () => {
    const { onSave, hook } = setup()
    act(() => hook.result.current.onChange('25'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS - 1) })
    expect(onSave).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(1) })
    expect(onSave).toHaveBeenCalledExactlyOnceWith(25)
  })

  it('typing "25" never saves the 2 on the way', () => {
    const { onSave, hook } = setup()
    act(() => hook.result.current.onChange('2'))
    act(() => { vi.advanceTimersByTime(200) })
    act(() => hook.result.current.onChange('25'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    expect(onSave.mock.calls).toEqual([[25]])
  })

  it('blur saves at once, without waiting for the pause', () => {
    const { onSave, hook } = setup()
    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    expect(onSave).toHaveBeenCalledExactlyOnceWith(30)
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS * 2) })
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('blur right after the pause does not save the same value again', () => {
    const { onSave, hook } = setup()
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onBlur())
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('does not save the stored value back', () => {
    const { onSave, hook } = setup('10')
    act(() => hook.result.current.onChange('10'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onBlur())
    expect(onSave).not.toHaveBeenCalled()
  })

  it('an empty or invalid value never saves, and blur puts the stored value back', () => {
    const { onSave, hook } = setup('10')
    act(() => hook.result.current.onChange(''))
    expect(hook.result.current.value).toBe('')
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('150'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onBlur())
    expect(onSave).not.toHaveBeenCalled()
    expect(hook.result.current.value).toBe('10')
  })

  it('follows the stored value when it changes', () => {
    const { hook } = setup('10')
    hook.rerender({ stored: '40' })
    expect(hook.result.current.value).toBe('40')
  })

  it('the server answering an earlier save does not wipe what was typed since', () => {
    const { hook } = setup('10')
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('305'))
    hook.rerender({ stored: '30' })
    expect(hook.result.current.value).toBe('305')
  })

  it('a save still waiting at unmount goes through', () => {
    const { onSave, hook } = setup()
    act(() => hook.result.current.onChange('45'))
    hook.unmount()
    expect(onSave).toHaveBeenCalledExactlyOnceWith(45)
  })

  it('a save the server refuses can be tried again, and the field shows the stored value', async () => {
    const onSave = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    const hook = renderHook(() => useAutosaveDraft({ stored: '10', parse: parsePct, format: String, onSave }))
    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    await act(async () => { await Promise.resolve() })
    expect(hook.result.current.value).toBe('10')

    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    expect(onSave).toHaveBeenCalledTimes(2)
  })

  it('a rejected save counts as refused too', async () => {
    const onSave = vi.fn().mockRejectedValueOnce(new Error('500')).mockResolvedValueOnce(true)
    const hook = renderHook(() => useAutosaveDraft({ stored: '10', parse: parsePct, format: String, onSave }))
    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    await act(async () => { await Promise.resolve() })
    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    expect(onSave).toHaveBeenCalledTimes(2)
  })

  it('a refused save does not wipe what was typed since', async () => {
    let refuse: (ok: boolean) => void = () => {}
    const onSave = vi.fn(() => new Promise<boolean>(r => { refuse = r }))
    const hook = renderHook(() => useAutosaveDraft({ stored: '10', parse: parsePct, format: String, onSave }))
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('35'))
    await act(async () => { refuse(false); await Promise.resolve() })
    expect(hook.result.current.value).toBe('35')
  })

  // A save that starts while another is still on its way waits for it: two
  // requests in flight could land in either order and leave the older value.
  it('a save waits for the one still in flight, then sends the latest value', async () => {
    const answers: Array<(ok: boolean) => void> = []
    const onSave = vi.fn(() => new Promise<boolean>(r => { answers.push(r) }))
    const hook = renderHook(() => useAutosaveDraft({ stored: '10', parse: parsePct, format: String, onSave }))
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    expect(onSave.mock.calls).toEqual([[30]])

    act(() => hook.result.current.onChange('35'))
    act(() => hook.result.current.onBlur())
    act(() => hook.result.current.onChange('40'))
    act(() => hook.result.current.onBlur())
    expect(onSave.mock.calls, 'nothing new is sent while 30 is in flight').toEqual([[30]])

    await act(async () => { answers[0](true); await Promise.resolve(); await Promise.resolve() })
    expect(onSave.mock.calls, 'then only the latest value goes').toEqual([[30], [40]])
  })

  it('a queued save still goes when the one before it was refused', async () => {
    const answers: Array<(ok: boolean) => void> = []
    const onSave = vi.fn(() => new Promise<boolean>(r => { answers.push(r) }))
    const hook = renderHook(() => useAutosaveDraft({ stored: '10', parse: parsePct, format: String, onSave }))
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('35'))
    act(() => hook.result.current.onBlur())
    await act(async () => { answers[0](false); await Promise.resolve(); await Promise.resolve() })
    expect(onSave.mock.calls).toEqual([[30], [35]])
    expect(hook.result.current.value, 'the newer value stays in the field').toBe('35')
  })

  function setupPending(stored = '10') {
    const answers: Array<{ ok: (v: boolean) => void; fail: (e: Error) => void }> = []
    const onSave = vi.fn(() => new Promise<boolean>((ok, fail) => { answers.push({ ok, fail }) }))
    const hook = renderHook(
      ({ stored }) => useAutosaveDraft({ stored, parse: parsePct, format: String, onSave }),
      { initialProps: { stored } },
    )
    return { onSave, hook, answers }
  }
  const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })

  it('the answer to an earlier save does not pull the field back while a newer one waits', async () => {
    const { hook, answers } = setupPending('10')
    act(() => hook.result.current.onChange('30'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('40'))
    act(() => hook.result.current.onBlur())
    answers[0].ok(true)
    await settle()
    // The refetch after the 30 lands while 40 is on its way.
    hook.rerender({ stored: '30' })
    expect(hook.result.current.value).toBe('40')
  })

  it('a refused save behind others shows the latest stored value, not an older one', async () => {
    const { onSave, hook, answers } = setupPending('10')
    act(() => hook.result.current.onChange('20'))
    act(() => { vi.advanceTimersByTime(DRAFT_SAVE_DELAY_MS) })
    act(() => hook.result.current.onChange('30'))
    act(() => hook.result.current.onBlur())
    answers[0].ok(true)
    await settle()
    hook.rerender({ stored: '20' })
    act(() => hook.result.current.onChange('40'))
    act(() => hook.result.current.onBlur())
    answers[1].fail(new Error('500'))
    await settle()
    answers[2].fail(new Error('500'))
    await settle()
    expect(onSave.mock.calls).toEqual([[20], [30], [40]])
    expect(hook.result.current.value, 'the server holds 20').toBe('20')
    // Typing the old 10 is a real change against what the server holds.
    act(() => hook.result.current.onChange('10'))
    act(() => hook.result.current.onBlur())
    expect(onSave).toHaveBeenLastCalledWith(10)
  })

  it('instant mode saves on every valid keystroke', () => {
    const { onSave, hook } = setup('10', true)
    act(() => hook.result.current.onChange('2'))
    act(() => hook.result.current.onChange('25'))
    act(() => hook.result.current.onChange(''))
    expect(onSave.mock.calls).toEqual([[2], [25]])
  })
})
