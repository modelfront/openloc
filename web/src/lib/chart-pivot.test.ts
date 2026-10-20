import { describe, it, expect } from 'vitest'
import { pivotChart, MAX_SERIES, OTHER_SERIES, labelCategories, capSlices } from './chart-pivot'
import type { ChartSpec } from '../api'

const base = { type: 'area', title: 't' } as const

describe('pivotChart', () => {
  // The reported bug: two rows per week, one series named `words`, so the
  // renderer drew a single band and both origins shared one colour.
  it('splits a dimension value into one series per value', () => {
    const spec: ChartSpec = {
      ...base,
      x_key: 'week',
      series: ['words'],
      series_key: 'kind',
      data: [
        { week: '2026-08-10', kind: 'human', words: 29 },
        { week: '2026-08-10', kind: 'mt', words: 29 },
        { week: '2026-08-17', kind: 'human', words: 40 },
        { week: '2026-08-17', kind: 'mt', words: 35 },
      ],
    }
    const out = pivotChart(spec)
    expect(out.series).toEqual(['human', 'mt'])
    expect(out.stacked).toBe(true)
    expect(out.data).toEqual([
      { week: '2026-08-10', human: 29, mt: 29 },
      { week: '2026-08-17', human: 40, mt: 35 },
    ])
  })

  it('passes a spec without series_key through untouched', () => {
    const spec: ChartSpec = {
      ...base,
      x_key: 'week',
      series: ['words', 'segments'],
      data: [{ week: '2026-08-10', words: 29, segments: 3 }],
    }
    const out = pivotChart(spec)
    expect(out.data).toBe(spec.data)
    expect(out.series).toEqual(['words', 'segments'])
    // Independent value columns are not parts of one whole — words stacked on
    // segments would be a meaningless total.
    expect(out.stacked).toBe(false)
  })

  // A gap must read as zero, not as a dip in the total.
  it('fills missing combinations with 0', () => {
    const out = pivotChart({
      ...base,
      x_key: 'week',
      series: ['words'],
      series_key: 'kind',
      data: [
        { week: 'w1', kind: 'mt', words: 10 },
        { week: 'w2', kind: 'mt', words: 12 },
        { week: 'w2', kind: 'human', words: 4 },
      ],
    })
    expect(out.data).toEqual([
      { week: 'w1', mt: 10, human: 0 },
      { week: 'w2', mt: 12, human: 4 },
    ])
  })

  it('preserves the order the server sent', () => {
    const out = pivotChart({
      ...base,
      x_key: 'engine',
      series: ['words'],
      series_key: 'kind',
      data: [
        { engine: 'DeepL', kind: 'mt', words: 100 },
        { engine: 'Claude', kind: 'mt', words: 80 },
      ],
    })
    expect(out.data.map(r => r.engine)).toEqual(['DeepL', 'Claude'])
  })

  // NULL dimensions normalize to "" upstream; an empty legend entry is
  // unreadable, so it gets a name.
  it('labels an empty dimension value', () => {
    const out = pivotChart({
      ...base,
      x_key: 'week',
      series: ['words'],
      series_key: 'customer',
      data: [{ week: 'w1', customer: '', words: 5 }],
    })
    expect(out.series).toEqual(['(none)'])
    expect(out.data[0]['(none)']).toBe(5)
  })

  // Nothing in the pivot knows about `kind` — the split works for any
  // dimension the measure declares. If this ever needs a per-dimension branch,
  // the design is wrong.
  it.each(['kind', 'customer', 'mt_engine', 'target_language', 'user', 'project'])(
    'splits on any dimension: %s',
    dim => {
      const out = pivotChart({
        ...base,
        x_key: 'week',
        series: ['words'],
        series_key: dim,
        data: [
          { week: 'w1', [dim]: 'A', words: 10 },
          { week: 'w1', [dim]: 'B', words: 4 },
        ],
      })
      expect(out.series).toEqual(['A', 'B'])
      expect(out.data).toEqual([{ week: 'w1', A: 10, B: 4 }])
      expect(out.stacked).toBe(true)
    },
  )

  // A dimension's cardinality is unbounded (hundreds of projects), and past the
  // palette size colours repeat — the very confusion series_key exists to fix.
  it('caps the bands and folds the rest into Other', () => {
    const data = Array.from({ length: 20 }, (_, i) => ({
      week: 'w1',
      project: `P${i}`,
      // P0 biggest, P19 smallest, so the ranking is unambiguous.
      words: 100 - i,
    }))
    const out = pivotChart({ ...base, x_key: 'week', series: ['words'], series_key: 'project', data })

    expect(out.series).toHaveLength(MAX_SERIES)
    expect(out.series.at(-1)).toBe(OTHER_SERIES)
    // The seven biggest survive by name; everything else is one bucket.
    expect(out.series.slice(0, 7)).toEqual(['P0', 'P1', 'P2', 'P3', 'P4', 'P5', 'P6'])

    // Nothing is lost — the folded bands still add up.
    const row = out.data[0]
    const total = out.series.reduce((sum, s) => sum + (row[s] as number), 0)
    expect(total).toBe(data.reduce((sum, d) => sum + d.words, 0))
    // And the dropped keys are gone rather than lingering as dead data.
    expect(row.P19).toBeUndefined()
  })

  it('leaves the bands alone when they fit', () => {
    const data = Array.from({ length: MAX_SERIES }, (_, i) => ({ week: 'w1', project: `P${i}`, words: 1 }))
    const out = pivotChart({ ...base, x_key: 'week', series: ['words'], series_key: 'project', data })
    expect(out.series).toHaveLength(MAX_SERIES)
    expect(out.series).not.toContain(OTHER_SERIES)
  })

  it('sums repeated pairs rather than overwriting', () => {
    const out = pivotChart({
      ...base,
      x_key: 'week',
      series: ['words'],
      series_key: 'kind',
      data: [
        { week: 'w1', kind: 'mt', words: 10 },
        { week: 'w1', kind: 'mt', words: 5 },
      ],
    })
    expect(out.data[0].mt).toBe(15)
  })
})

describe('pivotChart with an average column', () => {
  // An average is not part of a whole: stacked bands, summed pairs, an
  // "Other" sum and a 0 for a missing week would each draw a wrong number.
  const spec = (data: Record<string, unknown>[]): ChartSpec => ({
    type: 'bar', x_key: 'week', series: ['avg'], series_key: 'user', averages: ['avg'], data,
  })

  it('does not stack the bands', () => {
    expect(pivotChart(spec([{ week: 'w1', user: 'a', avg: 3 }])).stacked).toBe(false)
  })

  it('leaves a missing week empty instead of 0', () => {
    const out = pivotChart(spec([{ week: 'w1', user: 'a', avg: 3 }, { week: 'w2', user: 'b', avg: 4 }]))
    expect(out.data[0].b).toBeUndefined()
    expect(out.data[1].a).toBeUndefined()
  })

  it('drops the smallest bands instead of summing them into Other', () => {
    const rows = Array.from({ length: MAX_SERIES + 2 }, (_, i) => ({ week: 'w1', user: `u${i}`, avg: i + 1 }))
    const out = pivotChart(spec(rows))
    expect(out.series).not.toContain(OTHER_SERIES)
    expect(out.series).toHaveLength(MAX_SERIES)
    expect(out.data[0]).not.toHaveProperty('u0')
    expect(out.data[0].u9).toBe(10)
  })
})

describe('labelCategories', () => {
  it('names an empty category "(none)", as a breakdown does', () => {
    const rows = [{ origin: 'mt', words: 5 }, { origin: '', words: 3 }, { origin: null, words: 1 }]
    expect(labelCategories(rows, 'origin').map(r => r.origin)).toEqual(['mt', '(none)', '(none)'])
  })

  it('leaves the rows it was given alone', () => {
    const rows = [{ origin: '', words: 3 }]
    labelCategories(rows, 'origin')
    expect(rows[0].origin).toBe('')
  })
})

describe('capSlices', () => {
  const slices = (n: number) => Array.from({ length: n }, (_, i) => ({ lang: `l${i}`, words: 100 - i }))

  it('leaves a pie that fits alone', () => {
    expect(capSlices(slices(MAX_SERIES), 'lang', 'words', true)).toHaveLength(MAX_SERIES)
  })

  it('keeps the biggest and sums the rest into Other', () => {
    const out = capSlices(slices(10), 'lang', 'words', true)
    expect(out.map(r => r.lang)).toEqual(['l0', 'l1', 'l2', 'l3', 'l4', 'l5', 'l6', OTHER_SERIES])
    expect(out[MAX_SERIES - 1].words).toBe(93 + 92 + 91)
  })

  it('folds an Other the server already made into the same slice', () => {
    const rows = [...slices(8), { lang: OTHER_SERIES, words: 50 }]
    const out = capSlices(rows, 'lang', 'words', true)
    expect(out.filter(r => r.lang === OTHER_SERIES)).toHaveLength(1)
    expect(out[MAX_SERIES - 1].words).toBe(93 + 50)
  })

  it('drops the rest of an average instead of summing it', () => {
    const out = capSlices(slices(10), 'lang', 'words', false)
    expect(out).toHaveLength(MAX_SERIES - 1)
    expect(out.some(r => r.lang === OTHER_SERIES)).toBe(false)
  })
})
