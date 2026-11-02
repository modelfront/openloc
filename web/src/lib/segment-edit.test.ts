import { describe, expect, it } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { joinRefusal, joinsWithNext, loadedVersion } from './segment-edit'
import { queryKeys } from './query-keys'

// 1, 1.1, 1.2, 2: pieces of one split, then the next paragraph
const rows = [{ joinsNext: true }, { joinsNext: true }, { joinsNext: false }, { joinsNext: false }]

describe('joinsWithNext', () => {
  it('follows the index hint, and is false at the end', () => {
    expect(joinsWithNext(rows, 0)).toBe(true)
    expect(joinsWithNext(rows, 2)).toBe(false)
    expect(joinsWithNext(rows, 3)).toBe(false)
  })
})

describe('joinRefusal', () => {
  it('allows pieces that follow each other, in any order', () => {
    expect(joinRefusal(rows, [1, 0])).toBeNull()
    expect(joinRefusal(rows, [0, 1, 2])).toBeNull()
  })

  it('says why it refuses', () => {
    expect(joinRefusal(rows, [0])).toBe('Select at least two rows to join.')
    expect(joinRefusal(rows, [0, 2])).toBe('Only rows that follow each other can be joined.')
    expect(joinRefusal(rows, [2, 3])).toBe('Only sentences of one paragraph can be joined.')
  })
})

describe('loadedVersion', () => {
  it('reads the version from whichever loaded page holds the translation', () => {
    const qc = new QueryClient()
    qc.setQueryData([...queryKeys.job.editorRows(7), 0, 1], [{ segment: { id: 1 }, translation: { id: 10, updated_at: 'v1' } }])
    qc.setQueryData([...queryKeys.job.editorRows(7), 1, 2], [{ segment: { id: 2 }, translation: { id: 11, updated_at: 'v2' } }])
    expect(loadedVersion(qc, 7, 11)).toBe('v2')
    expect(loadedVersion(qc, 7, 12)).toBeUndefined()
    expect(loadedVersion(qc, 8, 11)).toBeUndefined()
  })
})
