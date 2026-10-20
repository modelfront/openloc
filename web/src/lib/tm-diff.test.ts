import { describe, it, expect } from 'vitest'
import { tmDiffRanges, TM_DIFF_CLASS } from './tm-diff'

/** The substrings the ranges actually cover — what the user sees highlighted. */
const highlighted = (text: string, reference: string) =>
  tmDiffRanges(text, reference).map(r => text.slice(r.start, r.end))

describe('tmDiffRanges', () => {
  it('returns [] for identical strings', () => {
    expect(tmDiffRanges('Reset your password.', 'Reset your password.')).toEqual([])
  })

  it('returns [] when either side is empty', () => {
    expect(tmDiffRanges('', 'Reset your password.')).toEqual([])
    expect(tmDiffRanges('Reset your password.', '')).toEqual([])
  })

  it('highlights only what `text` adds over `reference`', () => {
    expect(highlighted('Reset your admin password.', 'Reset your password.')).toEqual(['admin '])
  })

  it('returns [] when `text` only DROPS words — that side has nothing to add', () => {
    expect(tmDiffRanges('Reset your password.', 'Reset your admin password.')).toEqual([])
  })

  // This asymmetry is the whole design: each string is diffed against the other
  // and rendered, so a word only one of them has is visible on that one.
  it('shows each side its own additions when the arguments swap', () => {
    const segment = 'Click Save to store your changes.'
    const tmHit = 'Click Apply to store your settings.'

    // Asserted as the contract, not as the exact fragments the library emits:
    // a substituted word may come back split (see the next test), so the claim
    // is "the word this side owns is marked, the other side's is not".
    const onSegment = highlighted(segment, tmHit).join('')
    expect(onSegment).toContain('Save')
    expect(onSegment).not.toContain('Apply')

    const onHit = highlighted(tmHit, segment).join('')
    expect(onHit).toContain('Apply')
    expect(onHit).not.toContain('Save')

    // The shared middle is never marked on either side.
    expect(onSegment).not.toContain('store')
    expect(onHit).not.toContain('store')
  })

  // The diff is character-level, so two words that merely SHARE letters can
  // come back split around the shared ones. What must hold is containment: the
  // marks stay inside the substituted word and never leak into the text that
  // matches. The exact split is the algorithm's business — pinning it would go
  // red when diff_cleanupSemantic gets BETTER at merging the run.
  it('keeps a split substitution inside the substituted word', () => {
    const ranges = tmDiffRanges('your changes', 'your settings')
    expect(ranges.length).toBeGreaterThan(0)
    const wordStart = 'your '.length
    for (const r of ranges) {
      expect(r.start).toBeGreaterThanOrEqual(wordStart)
      expect(r.end).toBeLessThanOrEqual('your changes'.length)
    }
  })

  it('handles a substitution at the start and at the end', () => {
    expect(highlighted('A quick brown fox', 'The quick brown dog')).toEqual(['A', 'fox'])
  })

  it('tags every range with the shared highlight class', () => {
    const ranges = tmDiffRanges('Reset your admin password.', 'Reset your password.')
    expect(ranges.length).toBeGreaterThan(0)
    expect(ranges.every(r => r.className === TM_DIFF_CLASS)).toBe(true)
  })

  it('produces offsets that slice `text`, never `reference`', () => {
    const text = 'Der Benutzer muss das neue Passwort bestätigen.'
    const ranges = tmDiffRanges(text, 'Der Benutzer muss das Passwort bestätigen.')
    for (const r of ranges) {
      expect(r.start).toBeGreaterThanOrEqual(0)
      expect(r.end).toBeLessThanOrEqual(text.length)
      expect(r.end).toBeGreaterThan(r.start)
    }
    expect(highlighted(text, 'Der Benutzer muss das Passwort bestätigen.').join('')).toContain('neue')
  })

  // diff-match-patch indexes UTF-16 code units, the same units String.slice
  // uses — so an astral character must not shift the ranges that follow it.
  it('keeps offsets aligned across a surrogate pair', () => {
    expect(highlighted('Ship it 🚀 today please', 'Ship it 🚀 today')).toEqual([' please'])
  })

  it('works without word boundaries (CJK)', () => {
    const out = highlighted('請重設您的管理員密碼', '請重設您的密碼')
    expect(out.join('')).toBe('管理員')
  })

  it('declines a pair longer than the score cap', () => {
    const long = 'a'.repeat(2049)
    expect(tmDiffRanges(long + 'b', long)).toEqual([])
  })

  // Identity, not deep equality: two freshly computed results are deep-equal,
  // so `toEqual` here would pass with the cache deleted.
  it('returns the SAME array on a repeat call (memoised)', () => {
    const first = tmDiffRanges('Reset your admin password.', 'Reset your password.')
    const second = tmDiffRanges('Reset your admin password.', 'Reset your password.')
    expect(second).toBe(first)
  })

  // The cache key joins reference and text with a NUL, which segment text
  // cannot contain. Asserted on the offsets: a key that let ("ab","c") and
  // ("a","bc") collide would serve the first call's range {0,2} for the
  // second, and `highlighted` would hide that by slicing it out of a shorter
  // string.
  it('does not confuse pairs that concatenate to the same string', () => {
    tmDiffRanges('bc', 'a') // seeds the cache with a 2-char range
    expect(tmDiffRanges('c', 'ab')).toEqual([
      { start: 0, end: 1, className: TM_DIFF_CLASS },
    ])
  })
})
