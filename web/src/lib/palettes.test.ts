import { describe, it, expect } from 'vitest'
import { buildTokens, resolvePalette, palettesForMode, BUILTIN_PALETTES, DEFAULT_DARK_ID } from './palettes'

const trueBlack = BUILTIN_PALETTES.find(p => p.id === 'black-true')!
const red = (h: string) => parseInt(h.slice(1, 3), 16)

describe('buildTokens', () => {
  it('maps anchors onto the app-critical gray steps + semantic tokens (dark)', () => {
    const t = buildTokens(trueBlack)
    expect(t['--background']).toBe('#000000')
    expect(t['--color-gray-950']).toBe('#000000') // page bg
    expect(t['--color-gray-900']).toBe('#0a0a0a') // surface
    expect(t['--color-gray-800']).toBe('#171717') // elevated
    expect(t['--color-gray-700']).toBe('#262626') // border
    expect(t['--color-gray-500']).toBe('#a3a3a3') // muted
    expect(t['--color-gray-50']).toBe('#fafafa')  // text
    expect(t['--primary']).toBe('#3b82f6')
    expect(t['--border']).toBe('#262626')
    // Blue accent is dark → readable text on it is white.
    expect(t['--primary-foreground']).toBe('#ffffff')
  })

  it('interpolates a monotonic ramp between anchor stops', () => {
    const t = buildTokens(trueBlack)
    // gray-600 sits between border (700=#262626) and muted (500=#a3a3a3).
    expect(red(t['--color-gray-600'])).toBeGreaterThan(red('#262626'))
    expect(red(t['--color-gray-600'])).toBeLessThan(red('#a3a3a3'))
  })

  it('picks dark on-accent text for a bright accent', () => {
    const bright = { ...trueBlack, anchors: { ...trueBlack.anchors, accent: '#fde047' } } // yellow
    expect(buildTokens(bright)['--primary-foreground']).toBe('#0a0a0a')
  })
})

describe('resolvePalette', () => {
  it('falls back to the mode default for an unknown id', () => {
    expect(resolvePalette('nope', 'dark', []).id).toBe(DEFAULT_DARK_ID)
  })

  it('never returns a light palette when dark is requested', () => {
    expect(resolvePalette('light-default', 'dark', []).id).toBe(DEFAULT_DARK_ID)
  })

  it('resolves a matching custom palette', () => {
    const custom = { id: 'custom-x', name: 'X', mode: 'dark' as const, anchors: trueBlack.anchors }
    expect(resolvePalette('custom-x', 'dark', [custom]).name).toBe('X')
  })
})

describe('palettesForMode', () => {
  it('returns built-ins + customs for that mode only', () => {
    const custom = { id: 'custom-x', name: 'X', mode: 'light' as const, anchors: trueBlack.anchors }
    const light = palettesForMode('light', [custom])
    expect(light.map(p => p.id)).toContain('light-default')
    expect(light.map(p => p.id)).toContain('custom-x')
    expect(light.some(p => p.mode === 'dark')).toBe(false)
  })
})
