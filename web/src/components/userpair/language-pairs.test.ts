import { describe, it, expect } from 'vitest'
import { pairsFromRows, rowsFromPairs } from './LanguagePairsEditor'

describe('pairsFromRows — cartesian product tagged with row index', () => {
  it('pairs every source with every target in a row (row_group 0)', () => {
    expect(pairsFromRows([{ sources: ['en', 'de'], targets: ['es', 'fr'] }])).toEqual([
      { source_lang: 'en', target_lang: 'es', row_group: 0 },
      { source_lang: 'en', target_lang: 'fr', row_group: 0 },
      { source_lang: 'de', target_lang: 'es', row_group: 0 },
      { source_lang: 'de', target_lang: 'fr', row_group: 0 },
    ])
  })

  it('tags each row with its index across multiple rows', () => {
    expect(
      pairsFromRows([
        { sources: ['en'], targets: ['es'] },
        { sources: ['de'], targets: ['en'] },
      ]),
    ).toEqual([
      { source_lang: 'en', target_lang: 'es', row_group: 0 },
      { source_lang: 'de', target_lang: 'en', row_group: 1 },
    ])
  })

  it('skips self-pairs (source === target)', () => {
    expect(pairsFromRows([{ sources: ['en', 'es'], targets: ['es'] }])).toEqual([
      { source_lang: 'en', target_lang: 'es', row_group: 0 },
    ])
  })

  it('dedupes identical pairs across rows — first row wins', () => {
    expect(
      pairsFromRows([
        { sources: ['en'], targets: ['es'] },
        { sources: ['en'], targets: ['es'] },
      ]),
    ).toEqual([{ source_lang: 'en', target_lang: 'es', row_group: 0 }])
  })

  it('trims codes and ignores empty selections', () => {
    expect(pairsFromRows([{ sources: [' en ', ''], targets: [' es ', ''] }])).toEqual([
      { source_lang: 'en', target_lang: 'es', row_group: 0 },
    ])
  })

  it('yields nothing for empty rows', () => {
    expect(pairsFromRows([{ sources: [], targets: [] }])).toEqual([])
    expect(pairsFromRows([{ sources: ['en'], targets: [] }])).toEqual([])
  })
})

describe('rowsFromPairs — reconstruct rows from row_group', () => {
  it('groups pairs by row_group, not by source', () => {
    expect(
      rowsFromPairs([
        { source_lang: 'en', target_lang: 'es', row_group: 0 },
        { source_lang: 'en', target_lang: 'fr', row_group: 0 },
        { source_lang: 'de', target_lang: 'en', row_group: 1 },
      ]),
    ).toEqual([
      { sources: ['en'], targets: ['es', 'fr'] },
      { sources: ['de'], targets: ['en'] },
    ])
  })

  it('keeps two rows that share a source distinct (the whole point)', () => {
    expect(
      rowsFromPairs([
        { source_lang: 'de', target_lang: 'en', row_group: 0 },
        { source_lang: 'en', target_lang: 'es', row_group: 1 },
        { source_lang: 'en', target_lang: 'fr', row_group: 1 },
        { source_lang: 'de', target_lang: 'fr', row_group: 2 },
      ]),
    ).toEqual([
      { sources: ['de'], targets: ['en'] },
      { sources: ['en'], targets: ['es', 'fr'] },
      { sources: ['de'], targets: ['fr'] },
    ])
  })

  it('reconstructs a multi-source row from a shared row_group', () => {
    expect(
      rowsFromPairs([
        { source_lang: 'en', target_lang: 'es', row_group: 0 },
        { source_lang: 'de', target_lang: 'es', row_group: 0 },
      ]),
    ).toEqual([{ sources: ['en', 'de'], targets: ['es'] }])
  })

  it('groups legacy pairs (no row_group) into a single row', () => {
    expect(
      rowsFromPairs([
        { source_lang: 'en', target_lang: 'es' },
        { source_lang: 'de', target_lang: 'en' },
      ]),
    ).toEqual([{ sources: ['en', 'de'], targets: ['es', 'en'] }])
  })

  it('returns a single empty row when there are no pairs', () => {
    expect(rowsFromPairs([])).toEqual([{ sources: [], targets: [] }])
  })

  it('round-trips: pairsFromRows(rowsFromPairs(pairs)) preserves the set', () => {
    const pairs = [
      { source_lang: 'de', target_lang: 'en', row_group: 0 },
      { source_lang: 'en', target_lang: 'es', row_group: 1 },
      { source_lang: 'en', target_lang: 'fr', row_group: 1 },
      { source_lang: 'de', target_lang: 'fr', row_group: 2 },
    ]
    expect(pairsFromRows(rowsFromPairs(pairs))).toEqual(pairs)
  })
})
