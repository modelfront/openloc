import { describe, it, expect } from 'vitest'
import { formatShare, orderBySize, seriesAt, seriesMean, seriesSum, sliceShares, toggleSelected, withUnit } from './chart-legend'

describe('toggleSelected', () => {
  const all = ['de', 'fr', 'es']
  const hidden = (...names: string[]) => new Set(names)

  it('keeps only the first entry picked, without touching the original', () => {
    const none = hidden()
    expect([...toggleSelected(none, 'de', all)].sort()).toEqual(['es', 'fr'])
    expect(none.size).toBe(0)
  })

  it('adds an entry that was left out', () => {
    expect([...toggleSelected(hidden('fr', 'es'), 'fr', all)]).toEqual(['es'])
  })

  it('takes out a picked entry while another stays picked', () => {
    expect([...toggleSelected(hidden('es'), 'de', all)].sort()).toEqual(['de', 'es'])
  })

  it('shows everything again when the only picked entry is clicked', () => {
    expect([...toggleSelected(hidden('fr', 'es'), 'de', all)]).toEqual([])
  })

  it('shows everything once every entry is picked', () => {
    expect([...toggleSelected(hidden('es'), 'es', all)]).toEqual([])
  })
})

describe('sliceShares', () => {
  const rows = [{ lang: 'de', words: 600 }, { lang: 'fr', words: 300 }, { lang: 'es', words: 100 }]

  it('is each slice\'s share of the whole', () => {
    const s = sliceShares(rows, 'lang', 'words', new Set())
    expect([s.get('de'), s.get('fr'), s.get('es')]).toEqual([0.6, 0.3, 0.1])
  })

  it('is a share of what is still shown once a slice is hidden', () => {
    const s = sliceShares(rows, 'lang', 'words', new Set(['de']))
    expect([s.get('de'), s.get('fr'), s.get('es')]).toEqual([null, 0.75, 0.25])
  })

  it('has no share when the shown slices add up to nothing', () => {
    const s = sliceShares([{ lang: 'de', words: 0 }], 'lang', 'words', new Set())
    expect(s.get('de')).toBeNull()
  })
})

describe('formatShare', () => {
  it('rounds to a whole percent', () => {
    expect(formatShare(0.316)).toBe('32%')
    expect(formatShare(1)).toBe('100%')
  })

  it('never calls a real slice 0%', () => {
    expect(formatShare(0.001)).toBe('<1%')
    expect(formatShare(0)).toBe('0%')
  })
})

describe('seriesSum and seriesMean', () => {
  const data = [{ de: 40_000 }, { de: 15_000 }, {}]

  it('adds a series over every x', () => {
    expect(seriesSum(data, 'de')).toBe(55_000)
  })

  it('averages over the x values the series has, skipping the gaps', () => {
    expect(seriesMean(data, 'de')).toBe(27_500)
    expect(seriesMean(data, 'fr')).toBe(0)
  })
})

describe('orderBySize', () => {
  it('puts the biggest first, keeps ties in order, and pins the "last" item to the end', () => {
    const sizes: Record<string, number> = { a: 1, b: 5, c: 5, Other: 99 }
    expect(orderBySize(['a', 'Other', 'b', 'c'], n => sizes[n], n => n === 'Other')).toEqual(['b', 'c', 'a', 'Other'])
  })
})

describe('withUnit', () => {
  it('adds the unit, in the singular for one', () => {
    expect(withUnit('55', 55, 'words')).toBe('55 words')
    expect(withUnit('1', 1, 'segments')).toBe('1 segment')
    expect(withUnit('3.4', 3.4, 'edit distance')).toBe('3.4 edit distance')
  })

  it('leaves the number bare when the unit is unknown', () => {
    expect(withUnit('55', 55, undefined)).toBe('55')
  })
})

describe('seriesAt', () => {
  const row = { week: 'w1', mt: 600, tm: 300, human: 100 }

  it('finds the band of a stack that holds the value, bottom first', () => {
    expect(seriesAt(row, ['mt', 'tm', 'human'], 450, true)).toBe('mt')
    expect(seriesAt(row, ['mt', 'tm', 'human'], 601, true)).toBe('tm')
    expect(seriesAt(row, ['mt', 'tm', 'human'], 950, true)).toBe('human')
  })

  it('is nothing above the stack or below zero', () => {
    expect(seriesAt(row, ['mt', 'tm', 'human'], 1001, true)).toBeNull()
    expect(seriesAt(row, ['mt', 'tm', 'human'], -1, true)).toBeNull()
  })

  it('skips hidden bands, which the caller leaves out', () => {
    expect(seriesAt(row, ['tm', 'human'], 350, true)).toBe('human')
  })

  it('picks the closest line when the series are not stacked', () => {
    expect(seriesAt(row, ['mt', 'tm', 'human'], 250, false)).toBe('tm')
    expect(seriesAt({ a: null, b: 5 }, ['a', 'b'], 0, false)).toBe('b')
  })
})
