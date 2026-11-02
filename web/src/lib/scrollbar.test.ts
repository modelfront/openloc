// hasOverlayScrollbars: a scrollbar that takes no width from its box is an
// overlay one (macOS's default), drawn over the content's right edge.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { hasOverlayScrollbars } from './scrollbar'

// jsdom has no layout: give every element a 100px box whose scrollbar takes
// `scrollbar` px of it.
function fakeScrollbar(scrollbar: number) {
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(100)
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(100 - scrollbar)
}

afterEach(() => vi.restoreAllMocks())

describe('hasOverlayScrollbars', () => {
  it('is true when the scrollbar takes no width', () => {
    fakeScrollbar(0)
    expect(hasOverlayScrollbars()).toBe(true)
  })
  it('is false for a classic scrollbar that takes its own width', () => {
    fakeScrollbar(15)
    expect(hasOverlayScrollbars()).toBe(false)
  })
  it('leaves no probe element behind', () => {
    fakeScrollbar(0)
    const before = document.body.childElementCount
    hasOverlayScrollbars()
    expect(document.body.childElementCount).toBe(before)
  })
})
