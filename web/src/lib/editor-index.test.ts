import { describe, expect, it } from 'vitest'
import { rowForNumber, segmentNumber } from './editor-index'

const row = (position: number, subPosition = 0) => ({ position, subPosition })

describe('segmentNumber', () => {
  it('is the import number, with the piece of a split after a dot', () => {
    expect(segmentNumber(row(0))).toBe('1')
    expect(segmentNumber(row(2, 1))).toBe('3.1')
  })
})

describe('rowForNumber', () => {
  // 1, 1.1, 2, then 4: row 3 was joined into row 2.
  const rows = [row(0), row(0, 1), row(1), row(3)]

  it('finds the row showing the number', () => {
    expect(rowForNumber(rows, '1')).toBe(0)
    expect(rowForNumber(rows, ' 1.1 ')).toBe(1)
    expect(rowForNumber(rows, '4')).toBe(3)
  })

  it('finds the row a joined number went into', () => {
    expect(rowForNumber(rows, '3')).toBe(2)
  })

  it('finds nothing for a number the file does not have', () => {
    expect(rowForNumber(rows, '5')).toBeUndefined()
    expect(rowForNumber(rows, '1.2')).toBeUndefined()
    expect(rowForNumber(rows, '0')).toBeUndefined()
    expect(rowForNumber(rows, 'abc')).toBeUndefined()
    expect(rowForNumber([], '1')).toBeUndefined()
  })
})
