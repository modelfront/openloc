import { describe, expect, it } from 'vitest'
import type { XLSXInspection } from '@/api'
import {
  choicesToLayout,
  columnLabel,
  columnNumber,
  isXLSXFile,
  layoutProblem,
  sheetChoices,
  sheetColumns,
  sheetSummary,
  type SheetChoice,
} from './xlsx-columns'

const inspection: XLSXInspection = {
  bilingual: false,
  sheets: [
    { name: 'Products', columns: [{ letter: 'A', title: 'entry id' }, { letter: 'B', title: 'A very long product description heading' }, { letter: 'D' }] },
    { name: 'Notes', columns: [{ letter: 'A', title: 'Thanks' }] },
  ],
  suggested_layout: {
    sheets: [
      { name: 'Products', header_row: true, columns: ['B'] },
      { name: 'Notes', columns: ['A'] },
    ],
  },
}

const choice = (patch: Partial<SheetChoice>): SheetChoice => ({
  name: 'Products', header_row: true, columns: ['B'], included: true, ...patch,
})

describe('columnNumber', () => {
  it('reads one to three letters', () => {
    for (const [letter, n] of [['A', 1], ['Z', 26], ['AA', 27], ['AZ', 52], ['BA', 53], ['ZZ', 702], ['AAA', 703]] as const) {
      expect(columnNumber(letter)).toBe(n)
    }
    expect(columnNumber('a')).toBe(0)
    expect(columnNumber('')).toBe(0)
  })
})

describe('columnLabel', () => {
  it('puts the row 1 text in parentheses after the letter', () => {
    const [a, b, d] = inspection.sheets[0].columns
    expect(columnLabel(a)).toBe('A (entry id)')
    expect(columnLabel(b)).toBe('B (A very long product descriptio…)')
    expect(columnLabel(d)).toBe('D')
  })
})

describe('sheetColumns', () => {
  it('offers the columns that hold text, plus any already chosen, left to right', () => {
    const sheet = inspection.sheets[0]
    expect(sheetColumns(sheet).map(c => c.letter)).toEqual(['A', 'B', 'D'])
    expect(sheetColumns(sheet, ['AA', 'C', 'B']).map(c => c.letter)).toEqual(['A', 'B', 'C', 'D', 'AA'])
    expect(sheetColumns(undefined, ['B'])).toEqual([{ letter: 'B' }])
  })
})

describe('sheetChoices', () => {
  it('offers every suggested sheet in workbook order, the chosen ones included', () => {
    const stored = { sheets: [{ name: 'Notes', header_row: true }] }
    expect(sheetChoices(inspection, stored)).toEqual([
      { name: 'Products', header_row: true, columns: ['B'], included: false },
      { name: 'Notes', header_row: true, columns: [], included: true },
    ])
  })

  it('reads an absent header_row as false', () => {
    expect(sheetChoices(inspection, inspection.suggested_layout!)[1].header_row).toBe(false)
  })
})

describe('sheetSummary', () => {
  it('says what a collapsed sheet translates', () => {
    expect(sheetSummary(choice({ columns: ['D', 'B'] }), 4)).toBe('Columns B, D')
    expect(sheetSummary(choice({ columns: ['A', 'B', 'C', 'D'] }), 4)).toBe('All columns')
    expect(sheetSummary(choice({ columns: ['A'] }), 1)).toBe('Column A')
    expect(sheetSummary(choice({ columns: ['B'] }), 4)).toBe('Column B')
    expect(sheetSummary(choice({ columns: [] }), 4)).toBe('Whole sheet')
    expect(sheetSummary(choice({ included: false }), 4)).toBe('Not translated')
  })
})

describe('layoutProblem', () => {
  it('needs at least one sheet', () => {
    expect(layoutProblem([choice({})])).toBeNull()
    expect(layoutProblem([choice({ columns: [] })])).toBeNull()
    expect(layoutProblem([choice({ included: false })])).toBe('Choose at least one sheet to translate.')
  })
})

describe('choicesToLayout', () => {
  it('keeps the included sheets, sorts their columns, and drops them for a whole sheet', () => {
    expect(choicesToLayout([
      choice({ columns: ['D', 'AA', 'B'] }),
      choice({ name: 'Notes', columns: [], header_row: false }),
      choice({ name: 'Codes', included: false }),
    ])).toEqual({
      sheets: [
        { name: 'Products', header_row: true, columns: ['B', 'D', 'AA'] },
        { name: 'Notes', header_row: false },
      ],
    })
  })
})

describe('isXLSXFile', () => {
  it('goes by the extension', () => {
    expect(isXLSXFile(new File([''], 'Catalog.XLSX'))).toBe(true)
    expect(isXLSXFile(new File([''], 'catalog.xls'))).toBe(false)
  })
})
