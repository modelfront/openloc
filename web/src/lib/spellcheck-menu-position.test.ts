// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { positionFixMenu, estimateFixMenuHeight, FIX_MENU_WIDTH } from './spellcheck-menu-position'

const viewport = { width: 1280, height: 800 }
const anchor = (over: Partial<{ top: number; left: number; bottom: number }> = {}) => ({
  top: 100, left: 200, bottom: 118, ...over,
})

describe('positionFixMenu', () => {
  it('sits just below the word when there is room', () => {
    const p = positionFixMenu(anchor(), 3, viewport)
    expect(p).toEqual({ top: 122, left: 200, placement: 'below' })
  })

  it('flips above the word when it would overflow the bottom', () => {
    // Word near the bottom edge; a 3-row menu needs ~154px.
    const p = positionFixMenu(anchor({ top: 740, bottom: 758 }), 3, viewport)
    expect(p.placement).toBe('above')
    expect(p.top).toBeLessThan(740)
    expect(p.top).toBeGreaterThanOrEqual(8)
  })

  it('clamps horizontally so a word near the right edge stays visible', () => {
    const p = positionFixMenu(anchor({ left: 1270 }), 2, viewport)
    expect(p.left).toBe(viewport.width - FIX_MENU_WIDTH - 8)
    expect(p.left + FIX_MENU_WIDTH).toBeLessThanOrEqual(viewport.width)
  })

  it('never positions off the left edge', () => {
    const p = positionFixMenu(anchor({ left: -50 }), 2, viewport)
    expect(p.left).toBe(8)
  })

  // A viewport narrower than the menu makes the clamp bound go negative; the
  // menu must still land on screen rather than at a negative offset.
  it('pins to the left edge when the viewport is narrower than the menu', () => {
    const p = positionFixMenu(anchor({ left: 40 }), 2, { width: 200, height: 800 })
    expect(p.left).toBe(8)
  })

  it('clamps to the top edge when the menu fits neither below nor above', () => {
    // Short viewport, many suggestions — no room either side.
    const p = positionFixMenu(anchor({ top: 60, bottom: 78 }), 6, { width: 1280, height: 200 })
    expect(p.top).toBeGreaterThanOrEqual(8)
    expect(p.top).toBeLessThan(200)
  })

  it('always produces coordinates inside the viewport', () => {
    const viewports = [{ width: 1280, height: 800 }, { width: 768, height: 500 }, { width: 360, height: 640 }]
    for (const vp of viewports) {
      for (const top of [0, 50, vp.height - 40, vp.height - 5]) {
        for (const left of [0, 100, vp.width - 30, vp.width + 200]) {
          for (const count of [0, 1, 5]) {
            const p = positionFixMenu({ top, left, bottom: top + 18 }, count, vp)
            expect(p.left).toBeGreaterThanOrEqual(0)
            expect(p.left).toBeLessThanOrEqual(Math.max(0, vp.width - 1))
            expect(p.top).toBeGreaterThanOrEqual(0)
            expect(p.top).toBeLessThan(vp.height)
          }
        }
      }
    }
  })
})

describe('estimateFixMenuHeight', () => {
  it('reserves a row even with no suggestions', () => {
    expect(estimateFixMenuHeight(0)).toBe(estimateFixMenuHeight(1))
  })

  it('grows with the suggestion count', () => {
    expect(estimateFixMenuHeight(5)).toBeGreaterThan(estimateFixMenuHeight(2))
  })
})
