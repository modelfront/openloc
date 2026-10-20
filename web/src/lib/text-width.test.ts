// The canvas measurer is called for thousands of rows that share a few labels:
// it must measure each distinct text once and only reset the font on change.
import { describe, it, expect, vi, afterEach } from 'vitest'

afterEach(() => { vi.restoreAllMocks(); vi.resetModules() })

async function withFakeCanvas() {
  const fonts: string[] = []
  const measureText = vi.fn((t: string) => ({ width: t.length * 10 }) as TextMetrics)
  const ctx = {
    measureText,
    get font() { return fonts.at(-1) ?? '' },
    set font(f: string) { fonts.push(f) },
  } as unknown as CanvasRenderingContext2D
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as never)
  const { canvasTextMeasure } = await import('./text-width')
  return { canvasTextMeasure, measureText, fonts }
}

describe('canvasTextMeasure', () => {
  it('measures each distinct text once', async () => {
    const { canvasTextMeasure, measureText } = await withFakeCanvas()
    const m = canvasTextMeasure(500, 12, 7)
    expect([m('QE 0.9'), m('QE 0.9'), m('3'), m('QE 0.9')]).toEqual([60, 60, 10, 60])
    expect(measureText).toHaveBeenCalledTimes(2)
  })

  it('sets the canvas font only when it changes', async () => {
    const { canvasTextMeasure, fonts } = await withFakeCanvas()
    const chip = canvasTextMeasure(500, 12, 7)
    const text = canvasTextMeasure(400, 12, 7)
    chip('a'); chip('b'); text('c'); text('d'); chip('e')
    expect(fonts.map(f => f.split(' ')[0])).toEqual(['500', '400', '500'])
  })
})
