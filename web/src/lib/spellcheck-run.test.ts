// @vitest-environment node
//
// Pure orchestration logic — chunking, scope resolution, ignore filtering. No
// DOM involved, so this runs under `node` (see pm-offset.test.ts).
import { describe, it, expect, vi } from 'vitest'
import {
  chunk,
  runSpellcheck,
  isIgnored,
  SPELLCHECK_CHUNK_SIZE,
} from './spellcheck-run'
import { resolveRunScope } from './spellcheck-scope'
import type { SpellcheckJobResult } from '../api/spellcheck'

type Row = { id: number; segment_id: number; locked?: boolean }

const translations: Row[] = [
  { id: 101, segment_id: 1, locked: true },
  { id: 102, segment_id: 2 },
  { id: 103, segment_id: 3, locked: true },
  { id: 104, segment_id: 4 },
]

describe('resolveScope', () => {
  const map = new Map(translations.map(t => [t.segment_id, t]))

  it('all → every translation in segment order, locked included', () => {
    expect(resolveRunScope('all', [0, 1, 2, 3], [1, 2, 3, 4], map)).toEqual([101, 102, 103, 104])
  })

  it('nonlocked → skips locked segments', () => {
    expect(resolveRunScope('nonlocked', [0, 1, 2, 3], [1, 2, 3, 4], map)).toEqual([102, 104])
  })

  it('filtered → only the currently visible rows, locked ones skipped', () => {
    // filteredIndices points at segments 1, 2 and 4; 101 is locked.
    expect(resolveRunScope('filtered', [0, 1, 3], [1, 2, 3, 4], map)).toEqual([102, 104])
  })

  it('selected → the selected visible rows, locked ones skipped', () => {
    // 101 is locked; 104 is selected but hidden by the filter.
    expect(resolveRunScope('selected', [0, 1, 2], [1, 2, 3, 4], map, new Set([101, 102, 104]))).toEqual([102])
  })

  it('skips segments with no translation row', () => {
    const sparse = new Map([[1, translations[0]]])
    expect(resolveRunScope('all', [0, 1], [1, 2], sparse)).toEqual([101])
  })
})

describe('chunk', () => {
  it('splits into fixed-size groups', () => {
    expect(chunk([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]])
  })

  it('returns nothing for an empty list', () => {
    expect(chunk([], 10)).toEqual([])
  })

  it('keeps the chunk size within what the server accepts', () => {
    // Server rejects >200 per request (maxTranslationsPerRequest).
    expect(SPELLCHECK_CHUNK_SIZE).toBeLessThanOrEqual(200)
  })
})

describe('isIgnored', () => {
  it('matches the flagged word exactly, case-sensitively', () => {
    const ignored = new Set(['Adresse'])
    expect(isIgnored('die Adresse hier', { offset: 4, length: 7 }, ignored)).toBe(true)
    // `adresse` lowercase is a different word — in German that IS the error
    expect(isIgnored('die adresse hier', { offset: 4, length: 7 }, ignored)).toBe(false)
  })

  it('handles multi-byte text without shifting', () => {
    const ignored = new Set(['adresse'])
    const text = 'Die Prüfung enthält die adresse dort.'
    expect(isIgnored(text, { offset: text.indexOf('adresse'), length: 7 }, ignored)).toBe(true)
  })

  it('is false when nothing is ignored', () => {
    expect(isIgnored('die adresse', { offset: 4, length: 7 }, new Set())).toBe(false)
  })
})

function emptyResult(overrides: Partial<SpellcheckJobResult> = {}): SpellcheckJobResult {
  return { results: [], failed: 0, ...overrides }
}

describe('runSpellcheck', () => {
  it('chunks the id list and aggregates every chunk', async () => {
    const seen: number[][] = []
    const check = vi.fn(async (_job: number, ids: number[]) => {
      seen.push(ids)
      return emptyResult({
        results: ids.map(id => ({
          translation_id: id,
          matches: [{
            message: 'typo', offset: 4, length: 7,
            replacements: [{ value: 'Adresse' }],
            rule: { id: 'R', description: '', category: { id: 'TYPOS', name: 'Typo' } },
          }],
        })),
      })
    })

    const ids = Array.from({ length: 5 }, (_, i) => i + 1)
    const out = await runSpellcheck({
      jobId: 7, translationIds: ids, chunkSize: 2,
      textFor: () => 'die adresse hier',
      ignoredWords: new Set(),
      check,
    })

    expect(seen).toEqual([[1, 2], [3, 4], [5]])
    expect(out.errors).toHaveLength(5)
    expect(out.failed).toBe(0)
  })

  it('reports progress as chunks complete', async () => {
    const progress: number[] = []
    await runSpellcheck({
      jobId: 1, translationIds: [1, 2, 3, 4], chunkSize: 2,
      textFor: () => 'text',
      ignoredWords: new Set(),
      check: async () => emptyResult(),
      onProgress: done => progress.push(done),
    })
    expect(progress).toEqual([2, 4])
  })

  it('stops issuing requests once aborted', async () => {
    const controller = new AbortController()
    const check = vi.fn(async () => {
      controller.abort()
      return emptyResult()
    })

    const out = await runSpellcheck({
      jobId: 1, translationIds: [1, 2, 3, 4, 5, 6], chunkSize: 2,
      textFor: () => 'text',
      ignoredWords: new Set(),
      check,
      signal: controller.signal,
    })

    // First chunk runs and aborts; nothing after it should be requested.
    expect(check).toHaveBeenCalledTimes(1)
    expect(out.aborted).toBe(true)
  })

  it('drops matches whose word is already in the ignore set', async () => {
    const out = await runSpellcheck({
      jobId: 1, translationIds: [1], chunkSize: 10,
      textFor: () => 'die Adresse hier',
      ignoredWords: new Set(['Adresse']),
      check: async () => emptyResult({
        results: [{
          translation_id: 1,
          matches: [{
            message: 'typo', offset: 4, length: 7,
            replacements: [{ value: 'Adresse' }],
            rule: { id: 'R', description: '', category: { id: 'TYPOS', name: 'Typo' } },
          }],
        }],
      }),
    })
    expect(out.errors).toHaveLength(0)
  })

  it('carries the failed count through so the UI can report it', async () => {
    const out = await runSpellcheck({
      jobId: 1, translationIds: [1, 2, 3, 4], chunkSize: 2,
      textFor: () => 'text',
      ignoredWords: new Set(),
      check: async () => emptyResult({ failed: 2 }),
    })
    expect(out.failed).toBe(4) // two per chunk, two chunks
  })

  it('records the flagged word so Ignore all and Change all can group by it', async () => {
    const out = await runSpellcheck({
      jobId: 1, translationIds: [1], chunkSize: 10,
      textFor: () => 'die adresse hier',
      ignoredWords: new Set(),
      check: async () => emptyResult({
        results: [{
          translation_id: 1,
          matches: [{
            message: 'typo', offset: 4, length: 7,
            replacements: [{ value: 'Adresse' }],
            rule: { id: 'R', description: '', category: { id: 'TYPOS', name: 'Typo' } },
          }],
        }],
      }),
    })
    expect(out.errors[0].word).toBe('adresse')
    expect(out.errors[0].suggestions).toEqual(['Adresse'])
  })
})
