// The spreadsheet export must be the chart on screen — same series, same
// split, same dates — so these pin the table against the shapes SpecChart
// draws, and the one date bug this code base has already paid for.
process.env.TZ = 'America/Los_Angeles'

import { describe, it, expect } from 'vitest'
import { unzipSync, strFromU8 } from 'fflate'
import { chartTable, chartWorkbook, exportFileName } from './chart-export'
import type { ChartSpec } from '@/api'

describe('chartTable', () => {
  it('keeps only the plotted columns, labelled in words', () => {
    const table = chartTable({
      type: 'line', x_key: 'week', series: ['words'],
      data: [{ week: '2026-09-14', words: 120, segments: 9, characters: 700 }],
    })
    // segments and characters are in the row but not on the chart.
    expect(table.headers).toEqual(['Week starting', 'Words'])
    expect(table.rows[0]!.slice(1)).toEqual([120])
  })

  // `new Date('2026-09-14')` is UTC midnight — 13 Sep in Los Angeles. The
  // TZ pin above is what makes a naive parse fail here.
  it('writes time axes as LOCAL dates, not shifted a day west of Greenwich', () => {
    const table = chartTable({ type: 'line', x_key: 'week', series: ['words'], data: [{ week: '2026-09-14', words: 1 }] })
    const d = table.rows[0]![0] as Date
    expect(d).toBeInstanceOf(Date)
    expect([d.getFullYear(), d.getMonth(), d.getDate()]).toEqual([2026, 8, 14])
    expect(table.grain).toBe('week')
  })

  // The split widens long rows into one column per category value — the
  // legend's names are the headers.
  it('turns a split into one column per series, as the chart draws it', () => {
    const table = chartTable({
      type: 'bar', x_key: 'week', series: ['words'], series_key: 'mt_engine',
      data: [
        { week: '2026-09-14', mt_engine: 'DeepL', words: 10 },
        { week: '2026-09-14', mt_engine: 'Google', words: 4 },
        { week: '2026-09-21', mt_engine: 'DeepL', words: 7 },
      ],
    })
    expect(table.headers).toEqual(['Week starting', 'DeepL', 'Google'])
    expect(table.rows.map(r => r.slice(1))).toEqual([[10, 4], [7, 0]])
  })

  it('leaves a category axis as text, headed by the dimension', () => {
    const table = chartTable({
      type: 'pie', x_key: 'mt_engine', series: ['segments'],
      data: [{ mt_engine: 'DeepL', segments: 5 }],
    })
    expect(table.headers).toEqual(['MT engine', 'Segments'])
    expect(table.rows).toEqual([['DeepL', 5]])
    expect(table.grain).toBeNull()
  })

  // An average over no events has no value. Writing 0 would claim a perfect
  // score for a week nobody worked.
  it('keeps an empty average empty rather than zero', () => {
    const table = chartTable({
      type: 'line', x_key: 'week', series: ['avg_edit_distance'],
      data: [{ week: '2026-09-14', avg_edit_distance: null }],
    })
    expect(table.rows[0]![1]).toBeNull()
  })
})

describe('chartWorkbook', () => {
  // The library is the part that could break silently on an upgrade, so the
  // file is opened and read back rather than trusted.
  it('writes a real xlsx: the table on one sheet, its context on another', async () => {
    const spec: ChartSpec = { type: 'line', x_key: 'week', series: ['words'], data: [{ week: '2026-09-14', words: 120 }] }
    const blob = await chartWorkbook(chartTable(spec), {
      title: 'Words per week', definition: 'Source words a human confirmed.',
      filters: [['Customer', 'Acme']], exportedAt: new Date(2026, 8, 24, 10, 0),
    })
    const files = unzipSync(new Uint8Array(await blob.arrayBuffer()))
    const workbook = strFromU8(files['xl/workbook.xml']!)
    expect(workbook).toContain('name="Data"')
    expect(workbook).toContain('name="About"')
    // Text cells live in the shared-strings table.
    const strings = strFromU8(files['xl/sharedStrings.xml']!)
    for (const s of ['Week starting', 'Words', 'Words per week', 'Customer', 'Acme']) expect(strings).toContain(s)
  })
})

describe('exportFileName', () => {
  it('dates the file and swaps characters no file system accepts', () => {
    expect(exportFileName('QA: errors / warnings?', new Date(2026, 8, 4))).toBe('QA- errors - warnings- 2026-09-04.xlsx')
  })

  it('never produces a nameless file', () => {
    expect(exportFileName('///', new Date(2026, 0, 1))).toBe('- 2026-01-01.xlsx')
    expect(exportFileName('   ', new Date(2026, 0, 1))).toBe('chart 2026-01-01.xlsx')
  })
})
