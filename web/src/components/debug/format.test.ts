import { describe, expect, it } from 'vitest'
import { formatDuration, kindLabel, kindColor, matchesKindFilter, tryPretty } from './format'

describe('formatDuration', () => {
  it('shows milliseconds under a second', () => {
    expect(formatDuration(0)).toBe('0ms')
    expect(formatDuration(618)).toBe('618ms')
    expect(formatDuration(999)).toBe('999ms')
  })

  it('switches to seconds from 1000ms', () => {
    expect(formatDuration(1000)).toBe('1.0s')
    expect(formatDuration(2569)).toBe('2.6s')
    expect(formatDuration(61500)).toBe('61.5s')
  })
})

describe('tryPretty', () => {
  it('pretty-prints valid JSON', () => {
    expect(tryPretty('{"a":1}')).toBe('{\n  "a": 1\n}')
  })

  it('returns non-JSON input untouched', () => {
    expect(tryPretty('not json {')).toBe('not json {')
  })

  it('returns truncated JSON untouched (backend marker breaks parsing)', () => {
    const truncated = '{"a": "xxx…[truncated]'
    expect(tryPretty(truncated)).toBe(truncated)
  })

  it('passes empty string through', () => {
    expect(tryPretty('')).toBe('')
  })

  it('shows a line break inside a string as a real line break', () => {
    expect(tryPretty(JSON.stringify({ content: '[\n  {"id":"1"}\n]' }))).toBe(
      '{\n  "content": "[\n  {\\"id\\":\\"1\\"}\n]"\n}',
    )
  })

  it('keeps an escaped backslash before an n as text', () => {
    expect(tryPretty(JSON.stringify({ path: 'C:\\new' }))).toBe('{\n  "path": "C:\\\\new"\n}')
  })
})

describe('kindLabel / kindColor', () => {
  it('maps every known kind to a label', () => {
    expect(kindLabel('mt')).toBe('MT')
    expect(kindLabel('qe')).toBe('QE')
    expect(kindLabel('ape')).toBe('APE')
    expect(kindLabel('extension')).toBe('Extension')
  })

  // The colors have to be pinned per kind, not just for the fallback: unknown
  // kinds also resolve to 'grey', so a dropped or wrong entry would render a
  // grey chip and still pass a fallback-only assertion.
  it('maps every known kind to a color', () => {
    expect(kindColor('mt')).toBe('blue')
    expect(kindColor('qe')).toBe('yellow')
    expect(kindColor('ape')).toBe('green')
    expect(kindColor('extension')).toBe('grey')
  })

  it('gives the LLM kinds distinct colors, so the feed can be scanned by hue', () => {
    const llmKinds = ['mt', 'qe', 'ape'].map(kindColor)
    expect(new Set(llmKinds).size).toBe(llmKinds.length)
  })

  it('falls back to the raw kind and neutral color for unknown kinds', () => {
    expect(kindLabel('mystery')).toBe('mystery')
    expect(kindColor('mystery')).toBe('grey')
  })
})

describe('matchesKindFilter', () => {
  it('all matches everything', () => {
    expect(matchesKindFilter('mt', 'all')).toBe(true)
    expect(matchesKindFilter('extension', 'all')).toBe(true)
  })

  it('extension matches extension calls only', () => {
    expect(matchesKindFilter('extension', 'extension')).toBe(true)
    expect(matchesKindFilter('mt', 'extension')).toBe(false)
  })

  it('exact kinds match themselves only', () => {
    expect(matchesKindFilter('qe', 'qe')).toBe(true)
    expect(matchesKindFilter('ape', 'qe')).toBe(false)
  })
})
