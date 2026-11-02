import { describe, it, expect } from 'vitest'
import { DEFAULT_SORT, formatSort, nextSort, parseSort, sortKeysFor, type IssueSortLevel } from './issue-sort'

const s = (spec: string | null) => parseSort(spec, sortKeysFor('platform'))
const f = (levels: IssueSortLevel[]) => formatSort(levels) ?? ''

describe('parseSort / formatSort', () => {
  it('reads levels and writes them back, the default as no parameter', () => {
    expect(s('status,-priority,updated')).toEqual([
      { key: 'status', desc: false }, { key: 'priority', desc: true }, { key: 'updated', desc: false },
    ])
    expect(f(s('status,-priority,updated'))).toBe('status,-priority,updated')
    expect(s(null)).toEqual(DEFAULT_SORT)
    expect(formatSort(DEFAULT_SORT)).toBeNull()
    expect(formatSort(s('updated'))).toBe('updated')
  })

  it('drops unknown, repeated, extra and other-scope keys', () => {
    expect(f(s('title,status,-status,kind,area,updated'))).toBe('status,kind,area')
    expect(parseSort('priority,assignee', sortKeysFor('mine'))).toEqual(DEFAULT_SORT)
    expect(f(parseSort('priority,status', sortKeysFor('mine')))).toBe('status')
  })
})

describe('nextSort', () => {
  it('a click sorts by that column alone, at its first direction', () => {
    expect(f(nextSort(s('-updated'), 'status', false))).toBe('status')
    expect(f(nextSort(s('status,-priority'), 'priority', false))).toBe('priority')
    expect(f(nextSort(DEFAULT_SORT, 'kind', false))).toBe('kind')
    // Updated starts newest first.
    expect(f(nextSort(s('status'), 'updated', false))).toBe('-updated')
  })

  it('a click on the only level reverses it, then goes back to the default', () => {
    expect(f(nextSort(s('status'), 'status', false))).toBe('-status')
    expect(nextSort(s('-status'), 'status', false)).toEqual(DEFAULT_SORT)
    expect(f(nextSort(s('-updated'), 'updated', false))).toBe('updated')
    expect(nextSort(s('updated'), 'updated', false)).toEqual(DEFAULT_SORT)
  })

  it('a click on the first of several levels keeps it, alone and in its direction', () => {
    expect(f(nextSort(s('-status,priority'), 'status', false))).toBe('-status')
  })

  it('shift+click adds a level, up to three', () => {
    expect(f(nextSort(s('status'), 'priority', true))).toBe('status,priority')
    expect(f(nextSort(s('status,priority'), 'updated', true))).toBe('status,priority,-updated')
    expect(f(nextSort(s('status,priority,area'), 'kind', true))).toBe('status,priority,area')
  })

  it('shift+click flips a level at its first direction, then removes it', () => {
    expect(f(nextSort(s('status,priority'), 'priority', true))).toBe('status,-priority')
    expect(f(nextSort(s('status,-priority'), 'priority', true))).toBe('status')
    expect(f(nextSort(s('status,-updated'), 'updated', true))).toBe('status,updated')
    expect(f(nextSort(s('status,updated'), 'updated', true))).toBe('status')
    // Removing the first level promotes the next.
    expect(f(nextSort(s('-status,priority'), 'status', true))).toBe('priority')
  })

  it('shift+click on the only level takes the same steps as a click', () => {
    expect(f(nextSort(s('status'), 'status', true))).toBe('-status')
    expect(nextSort(s('-status'), 'status', true)).toEqual(DEFAULT_SORT)
    expect(f(nextSort(DEFAULT_SORT, 'status', true))).toBe('status')
  })
})
