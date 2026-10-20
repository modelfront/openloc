// Text widths from the page's own font, via one canvas made on first use — for
// layouts worked out from data instead of read from rendered DOM. Where there
// is no canvas (jsdom) it falls back to an average character width.
//
// Each measurer caches by text: callers measure the same few labels across
// thousands of rows. The canvas font is set only when it changes.

export type MeasureText = (text: string) => number

let ctx: CanvasRenderingContext2D | null | undefined
let family = 'sans-serif'
let currentFont = ''

export function canvasTextMeasure(weight: number, px: number, fallbackCharPx: number): MeasureText {
  const cache = new Map<string, number>()
  return (text: string) => {
    const hit = cache.get(text)
    if (hit !== undefined) return hit
    if (ctx === undefined) {
      ctx = document.createElement('canvas').getContext('2d')
      family = getComputedStyle(document.body).fontFamily || family
    }
    let width: number
    if (!ctx) {
      width = text.length * fallbackCharPx
    } else {
      const font = `${weight} ${px}px ${family}`
      if (font !== currentFont) {
        ctx.font = font
        currentFont = font
      }
      width = ctx.measureText(text).width
    }
    cache.set(text, width)
    return width
  }
}
