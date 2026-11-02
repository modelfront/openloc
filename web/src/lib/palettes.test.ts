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

  // A disabled filled button: the label must stay legible but read dimmer than
  // an enabled one, whatever the accent — a light accent gets near-black
  // on-accent text, which at full strength would look like a working button.
  it.each([
    ['light, blue accent', 'paper', '#3b82f6'],
    ['light, yellow accent', 'paper', '#facc15'],
    ['light, lime accent', 'paper', '#84cc16'],
    ['dark, blue accent', 'black-true', '#3b82f6'],
    ['dark, yellow accent', 'black-true', '#facc15'],
  ])('fades the disabled-button label for a %s', (_label, base, accent) => {
    const palette = BUILTIN_PALETTES.find(p => p.id === base)!
    const t = buildTokens({ ...palette, anchors: { ...palette.anchors, accent } })
    const ratio = contrast(t['--primary-faded-foreground'], t['--primary-faded'])
    expect(ratio).toBeGreaterThanOrEqual(1.5)
    expect(ratio).toBeLessThanOrEqual(3.05)
  })

  // The default look: the on-accent white, already soft on the faded blue.
  it('keeps the white label on the faded blue of the default light palette', () => {
    const paper = BUILTIN_PALETTES.find(p => p.id === 'paper')!
    expect(buildTokens(paper)['--primary-faded-foreground']).toBe('#ffffff')
  })

  // A light accent's label is near-black like its enabled button, dimmed.
  it('starts a light accent\'s disabled label from its near-black on-accent colour', () => {
    const paper = BUILTIN_PALETTES.find(p => p.id === 'paper')!
    const t = buildTokens({ ...paper, anchors: { ...paper.anchors, accent: '#facc15' } })
    expect(red(t['--primary-faded-foreground'])).toBeLessThan(red(t['--primary-faded']))
  })
})

function contrast(a: string, b: string): number {
  const lum = (h: string) => {
    const [r, g, bl] = [1, 3, 5].map(i => {
      const s = parseInt(h.slice(i, i + 2), 16) / 255
      return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
    })
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl
  }
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

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
