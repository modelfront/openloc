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

describe('toIndexData files', () => {
  it('gives each row the file whose rows it falls in', async () => {
    const { toIndexData } = await import('./editor-index')
    const n = 5
    const col = <T,>(v: T) => Array.from({ length: n }, () => v)
    const data = toIndexData({
      segment_ids: [1, 2, 3, 4, 5], translation_ids: [1, 2, 3, 4, 5], positions: [0, 1, 0, 1, 2], sub_positions: col(0),
      word_counts: col(1), statuses: col('needs_review'), has_target: col(false), step_edited: col(false), predicted: col(false),
      origins: col(''), tm_match: col(undefined), locked: col(false), in_flight: col(''), confirmed_via: col(''), qe_note_heads: col(''),
      joins_next: col(false), repetition: col(0), repetition_stats: { segments: 0, words: 0, characters: 0 },
      files: [
        { job_id: 7, file_id: 70, file_name: 'a', file_format: 'txt', inline_html: true, first_row: 0, row_count: 2, step_name: '', has_previous_step: false },
        { job_id: 8, file_id: 80, file_name: 'b', file_format: 'txt', inline_html: true, first_row: 2, row_count: 3, step_name: '', has_previous_step: false },
      ],
    })
    expect(data.rows.map(r => r.fileIdx)).toEqual([0, 0, 1, 1, 1])
    expect(data.files.map(f => f.job_id)).toEqual([7, 8])
  })
})
