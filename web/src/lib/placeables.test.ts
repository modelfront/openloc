import { describe, it, expect } from 'vitest'
import { getMissingPlaceables, placeableLabel, placeableRanges, type Placeable } from './placeables'
import { decorationRanges } from './segment-decorations'

const p = (text: string, kind = 'variable'): Placeable => ({ kind, text })

describe('placeableRanges', () => {
  it('finds each placeable in order, a repeated one at its next occurrence', () => {
    const flat = 'Hi {0}, {0} owes $5'
    const ranges = placeableRanges(flat, [p('{0}'), p('{0}'), p('$5', 'currency')])
    expect(ranges.map(r => flat.slice(r.start, r.end))).toEqual(['{0}', '{0}', '$5'])
    expect(ranges.map(r => r.start)).toEqual([3, 8, 17])
    expect(ranges[2].insertText).toBe('$5')
  })

  it('counts UTF-16 offsets, so an emoji before a placeable does not shift it', () => {
    const flat = '👋 see {name}'
    const [r] = placeableRanges(flat, [p('{name}')])
    expect(flat.slice(r.start, r.end)).toBe('{name}')
  })

  it('skips a placeable the flattened text does not have and keeps going', () => {
    const flat = 'Order XJ-500B'
    const ranges = placeableRanges(flat, [p('{gone}'), p('XJ-500B', 'code')])
    expect(ranges).toHaveLength(1)
    expect(flat.slice(ranges[0].start, ranges[0].end)).toBe('XJ-500B')
  })

  it('keeps the placeable insert text when a term underline covers part of it', () => {
    const flat = 'Use XJ-500B now'
    const ranges = decorationRanges(flat, { placeables: [p('XJ-500B', 'code')], search: /XJ/g })
    const covering = ranges.filter(r => r.start < 11 && r.end > 4)
    expect(covering.length).toBeGreaterThan(1)
    expect(covering.every(r => r.insertText === 'XJ-500B')).toBe(true)
  })
})

describe('getMissingPlaceables', () => {
  it('returns nothing when every placeable is in the target', () => {
    expect(getMissingPlaceables([p('{name}'), p('https://example.com', 'url')], 'Hallo {name}, https://example.com')).toEqual([])
  })

  it('returns the missing ones in source order', () => {
    const missing = getMissingPlaceables([p('{a}'), p('{b}'), p('{c}')], 'nur {b}')
    expect(missing.map(m => m.text)).toEqual(['{a}', '{c}'])
  })

  it('counts repeats: a variable the source has twice must be there twice', () => {
    expect(getMissingPlaceables([p('{0}'), p('{0}')], 'x {0}').map(m => m.text)).toEqual(['{0}'])
  })

  it('counts a variable wherever it is, and a code only where it does not run on', () => {
    expect(getMissingPlaceables([p('%d'), p('%d')], 'Gr %dx%d')).toEqual([])
    expect(getMissingPlaceables([p('MP3', 'code')], 'den MP3-Player')).toEqual([])
    expect(getMissingPlaceables([p('XJ-500B', 'code')], 'XJ-500B-2')).toEqual([p('XJ-500B', 'code')])
    expect(getMissingPlaceables([p('SKU_1234', 'code')], 'SKU_1234_B')).toEqual([p('SKU_1234', 'code')])
    expect(getMissingPlaceables([p('XJ-500B', 'code')], 'รุ่นXJ-500Bพร้อม')).toEqual([])
  })

  it('lets only a letter or digit edge run on, and counts only decimal digits as digits', () => {
    expect(getMissingPlaceables([p('$5', 'currency')], 'PLUS$5 Angebot')).toEqual([])
    expect(getMissingPlaceables([p('5', 'number')], 'x\u00b25')).toEqual([])
  })

  it('counts a code written against Arabic text', () => {
    expect(getMissingPlaceables([p('XJ500', 'code')], 'رقمXJ500 جاهز')).toEqual([])
  })

  it('counts a number written against Chinese or Japanese text', () => {
    expect(getMissingPlaceables([p('500', 'number')], '価格は500円です')).toEqual([])
    expect(getMissingPlaceables([p('5', 'number')], 'Pack 5er')).toEqual([p('5', 'number')])
  })

  it('does not count a number inside a longer number or a code inside a longer word', () => {
    expect(getMissingPlaceables([p('5', 'number')], 'Es sind 15').map(m => m.text)).toEqual(['5'])
    expect(getMissingPlaceables([p('A4', 'code')], 'Format A45').map(m => m.text)).toEqual(['A4'])
    expect(getMissingPlaceables([p('5', 'number')], 'Es sind 5.')).toEqual([])
  })
})

describe('placeableLabel', () => {
  it('names the kind', () => {
    expect(placeableLabel(p('x', 'url'))).toBe('Web address')
    expect(placeableLabel(p('XJ-500B', 'code'))).toBe('Code')
  })
})
