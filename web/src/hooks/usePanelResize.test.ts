import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { usePanelResize, clampWidth, loadWidth, KEY_STEP } from './usePanelResize'

const KEY = 'test-panel-width'
const OPTS = { storageKey: KEY, min: 200, max: 600, fallback: 320, label: 'Resize panel' }

// jsdom has no PointerEvent constructor; MouseEvent carries clientX and is
// dispatched under the pointer* type names the hook listens for.
function pointer(type: string, clientX: number) {
  window.dispatchEvent(new MouseEvent(type, { clientX }))
}

describe('clampWidth', () => {
  it('keeps a width inside the bounds', () => {
    expect(clampWidth(300, 200, 600)).toBe(300)
    expect(clampWidth(100, 200, 600)).toBe(200)
    expect(clampWidth(900, 200, 600)).toBe(600)
  })

  it('rounds fractional widths and rejects non-numbers', () => {
    expect(clampWidth(300.6, 200, 600)).toBe(301)
    expect(clampWidth(Number.NaN, 200, 600)).toBe(200)
  })
})

describe('loadWidth', () => {
  beforeEach(() => localStorage.clear())

  it('falls back when nothing is stored', () => {
    expect(loadWidth(KEY, 320, 200, 600)).toBe(320)
  })

  it('falls back on a non-numeric value', () => {
    localStorage.setItem(KEY, 'wide please')
    expect(loadWidth(KEY, 320, 200, 600)).toBe(320)
  })

  it('clamps a stored width that is out of range', () => {
    localStorage.setItem(KEY, '5000')
    expect(loadWidth(KEY, 320, 200, 600)).toBe(600)
  })
})

describe('usePanelResize', () => {
  beforeEach(() => localStorage.clear())

  it('starts from the stored width', () => {
    localStorage.setItem(KEY, '420')
    const { result } = renderHook(() => usePanelResize(OPTS))
    expect(result.current.width).toBe(420)
  })

  it('widens the panel when the handle is dragged left, and persists on release', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))

    act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
    expect(result.current.isResizing).toBe(true)

    act(() => pointer('pointermove', 700))
    expect(result.current.width).toBe(420)
    // Nothing is written mid-drag — only the released width is the user's choice.
    expect(localStorage.getItem(KEY)).toBeNull()

    act(() => pointer('pointerup', 700))
    expect(result.current.isResizing).toBe(false)
    expect(localStorage.getItem(KEY)).toBe('420')
  })

  it('narrows the panel when the handle is dragged right', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))
    act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
    act(() => pointer('pointermove', 860))
    expect(result.current.width).toBe(260)
  })

  it('clamps a drag past either bound', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))
    act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
    act(() => pointer('pointermove', 100))
    expect(result.current.width).toBe(600)
    act(() => pointer('pointermove', 1500))
    expect(result.current.width).toBe(200)
  })

  describe('with onCollapse', () => {
    it('closes the panel when a drag reaches the minimum, keeping the width it had', () => {
      const onCollapse = vi.fn()
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onCollapse }))

      act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
      act(() => pointer('pointermove', 1200))

      expect(onCollapse).toHaveBeenCalledTimes(1)
      // The drag is over, and the panel keeps the width it had — reopening it
      // must not hand back a 30px sliver.
      expect(result.current.isResizing).toBe(false)
      expect(result.current.width).toBe(320)
      expect(localStorage.getItem(KEY)).toBe('320')
    })

    it('does not collapse on a drag that stops short of the minimum', () => {
      const onCollapse = vi.fn()
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onCollapse }))
      act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
      act(() => pointer('pointermove', 1000))
      expect(onCollapse).not.toHaveBeenCalled()
      expect(result.current.width).toBe(120)
    })

    it('reopens the panel when the closed affordance is dragged out, at the dragged width', () => {
      const onExpand = vi.fn()
      const onCollapse = vi.fn()
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onExpand, onCollapse }))

      // A press with no movement leaves it closed — that press is a click, and
      // the affordance's own onClick handles it.
      act(() => result.current.reopenDragProps.onPointerDown({ clientX: 1400 } as React.PointerEvent))
      expect(onExpand).not.toHaveBeenCalled()

      act(() => pointer('pointermove', 1100))
      expect(onExpand).toHaveBeenCalledTimes(1)
      expect(result.current.width).toBe(300)

      act(() => pointer('pointermove', 1000))
      // Still one open — the panel opens on the crossing, not on every move.
      expect(onExpand).toHaveBeenCalledTimes(1)
      expect(result.current.width).toBe(400)

      act(() => pointer('pointerup', 1000))
      expect(localStorage.getItem(KEY)).toBe('400')
    })

    it('closes again if a reopen drag comes back in, and can pull out once more', () => {
      const onExpand = vi.fn()
      const onCollapse = vi.fn()
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onExpand, onCollapse }))

      act(() => result.current.reopenDragProps.onPointerDown({ clientX: 1400 } as React.PointerEvent))
      act(() => pointer('pointermove', 1100))
      act(() => pointer('pointermove', 1395))
      expect(onCollapse).toHaveBeenCalledTimes(1)
      // Still dragging — the affordance the drag started on is the one under
      // the pointer, so pulling out again reopens.
      expect(result.current.isResizing).toBe(true)

      act(() => pointer('pointermove', 1200))
      expect(onExpand).toHaveBeenCalledTimes(2)
      expect(result.current.width).toBe(200)
    })

    it('tells a click on the closed affordance apart from the tail of a drag', () => {
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onExpand: vi.fn(), onCollapse: vi.fn() }))

      // Press and release without moving: the caller is free to pick a width.
      act(() => result.current.reopenDragProps.onPointerDown({ clientX: 1400 } as React.PointerEvent))
      act(() => pointer('pointerup', 1400))
      expect(result.current.wasDragged()).toBe(false)

      act(() => result.current.setWidth(240))
      expect(result.current.width).toBe(240)
      expect(localStorage.getItem(KEY)).toBe('240')

      // A press that moved has already chosen a width; the caller leaves it be.
      act(() => result.current.reopenDragProps.onPointerDown({ clientX: 1400 } as React.PointerEvent))
      act(() => pointer('pointermove', 1100))
      act(() => pointer('pointerup', 1100))
      expect(result.current.wasDragged()).toBe(true)
      expect(result.current.width).toBe(300)
    })

    it('closes the panel when the arrow keys reach the minimum', () => {
      localStorage.setItem(KEY, '30')
      const onCollapse = vi.fn()
      const { result } = renderHook(() => usePanelResize({ ...OPTS, min: 30, onCollapse }))
      act(() => result.current.separatorProps.onKeyDown({ key: 'ArrowRight', preventDefault: () => {} } as React.KeyboardEvent))
      expect(onCollapse).toHaveBeenCalledTimes(1)
      expect(result.current.width).toBe(30)
    })
  })

  it('ignores pointer moves after release', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))
    act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
    act(() => pointer('pointerup', 800))
    act(() => pointer('pointermove', 400))
    expect(result.current.width).toBe(320)
  })

  it('nudges the width with the arrow keys and persists each step', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))
    const press = (key: string) => act(() =>
      result.current.separatorProps.onKeyDown({ key, preventDefault: () => {} } as React.KeyboardEvent))

    press('ArrowLeft')
    expect(result.current.width).toBe(320 + KEY_STEP)
    expect(localStorage.getItem(KEY)).toBe(String(320 + KEY_STEP))

    press('ArrowRight')
    press('ArrowRight')
    expect(result.current.width).toBe(320 - KEY_STEP)

    press('Enter')
    expect(result.current.width).toBe(320 - KEY_STEP)
  })

  it('restores the default width on double-click', () => {
    localStorage.setItem(KEY, '560')
    const { result } = renderHook(() => usePanelResize(OPTS))
    act(() => result.current.separatorProps.onDoubleClick())
    expect(result.current.width).toBe(320)
    expect(localStorage.getItem(KEY)).toBe('320')
  })

  describe("with edge: 'right' (a handle on the right edge, e.g. a column)", () => {
    const RIGHT = { ...OPTS, edge: 'right' as const }

    it('widens when dragged right and narrows when dragged left', () => {
      const { result } = renderHook(() => usePanelResize(RIGHT))
      act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
      act(() => pointer('pointermove', 900))
      expect(result.current.width).toBe(420)
      act(() => pointer('pointermove', 740))
      expect(result.current.width).toBe(260)
      act(() => pointer('pointerup', 740))
      expect(localStorage.getItem(KEY)).toBe('260')
    })

    it('ArrowRight widens and ArrowLeft narrows', () => {
      const { result } = renderHook(() => usePanelResize(RIGHT))
      const press = (key: string) => act(() =>
        result.current.separatorProps.onKeyDown({ key, preventDefault: () => {} } as React.KeyboardEvent))
      press('ArrowRight')
      expect(result.current.width).toBe(320 + KEY_STEP)
      press('ArrowLeft')
      press('ArrowLeft')
      expect(result.current.width).toBe(320 - KEY_STEP)
    })
  })

  it('releases the body cursor lock when a drag ends', () => {
    const { result } = renderHook(() => usePanelResize(OPTS))
    act(() => result.current.separatorProps.onPointerDown({ clientX: 800, preventDefault: () => {} } as React.PointerEvent))
    expect(document.body.style.cursor).toBe('col-resize')
    act(() => pointer('pointerup', 800))
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')
  })
})
