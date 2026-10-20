import { describe, it, expect } from 'vitest'
import { mergeRanges, withGhosts, type DecoratedRange } from './text-ranges'

const r = (start: number, end: number, className: string, title?: string): DecoratedRange =>
  title ? { start, end, className, title } : { start, end, className }

describe('mergeRanges', () => {
  it('returns [] for no input and for empty lists', () => {
    expect(mergeRanges()).toEqual([])
    expect(mergeRanges([], [])).toEqual([])
  })

  it('passes a single disjoint list through, sorted', () => {
    expect(mergeRanges([r(5, 8, 'b'), r(0, 3, 'a')])).toEqual([
      { start: 0, end: 3, className: 'a' },
      { start: 5, end: 8, className: 'b' },
    ])
  })

  it('leaves a gap between ranges — the renderer fills it with plain text', () => {
    const out = mergeRanges([r(0, 2, 'a')], [r(5, 7, 'b')])
    expect(out).toEqual([
      { start: 0, end: 2, className: 'a' },
      { start: 5, end: 7, className: 'b' },
    ])
  })

  it('splits a partial overlap into three spans, the middle carrying both classes', () => {
    const out = mergeRanges([r(0, 6, 'underline')], [r(4, 10, 'bg')])
    expect(out).toEqual([
      { start: 0, end: 4, className: 'underline' },
      { start: 4, end: 6, className: 'underline bg' },
      { start: 6, end: 10, className: 'bg' },
    ])
  })

  it('splits a nested range into three spans', () => {
    const out = mergeRanges([r(0, 10, 'outer')], [r(3, 6, 'inner')])
    expect(out).toEqual([
      { start: 0, end: 3, className: 'outer' },
      { start: 3, end: 6, className: 'outer inner' },
      { start: 6, end: 10, className: 'outer' },
    ])
  })

  it('combines identical ranges into one span with both classes', () => {
    expect(mergeRanges([r(2, 5, 'a')], [r(2, 5, 'b')])).toEqual([
      { start: 2, end: 5, className: 'a b' },
    ])
  })

  it('keeps a title from whichever input carries one', () => {
    const out = mergeRanges([r(0, 4, 'underline', 'Passwort (Approved)')], [r(0, 4, 'bg')])
    expect(out).toEqual([
      { start: 0, end: 4, className: 'underline bg', title: 'Passwort (Approved)' },
    ])
  })

  // A '' className is "covered but not worth marking" (an unused forbidden
  // term). It must survive the merge so the renderer still emits its leaf, and
  // it must not contribute a stray space to a co-located class.
  it('preserves an unmarked range and does not pad the joined class', () => {
    expect(mergeRanges([r(0, 3, '')])).toEqual([{ start: 0, end: 3, className: '' }])
    expect(mergeRanges([r(0, 3, '')], [r(0, 3, 'bg')])).toEqual([
      { start: 0, end: 3, className: 'bg' },
    ])
  })

  it('coalesces touching spans that end up identical', () => {
    // Two adjacent term occurrences with the same class and no gap would
    // otherwise render as two spans saying exactly the same thing.
    expect(mergeRanges([r(0, 3, 'a'), r(3, 6, 'a')])).toEqual([
      { start: 0, end: 6, className: 'a' },
    ])
  })

  it('drops zero-width ranges', () => {
    expect(mergeRanges([r(4, 4, 'a')], [r(0, 2, 'b')])).toEqual([
      { start: 0, end: 2, className: 'b' },
    ])
  })
})

describe('withGhosts', () => {
  const ghost = (at: number, text: string): DecoratedRange =>
    ({ start: at, end: at, className: 'del', ghost: text })

  it('returns the painted ranges untouched when there are no ghosts', () => {
    const painted = [r(0, 3, 'a')]
    expect(withGhosts(painted, [])).toBe(painted)
  })

  it('puts a ghost before a painted range that starts where it sits', () => {
    expect(withGhosts([r(4, 8, 'ins')], [ghost(4, 'old')])).toEqual([ghost(4, 'old'), r(4, 8, 'ins')])
  })

  // A term underline can span the spot where a word was deleted. The renderer
  // only draws a ghost between ranges, so the range is cut around it.
  it('splits a painted range that strictly contains a ghost', () => {
    expect(withGhosts([r(0, 10, 'underline', 't')], [ghost(4, 'old')])).toEqual([
      r(0, 4, 'underline', 't'),
      ghost(4, 'old'),
      r(4, 10, 'underline', 't'),
    ])
  })

  it('keeps ghosts that touch no painted range, in order', () => {
    expect(withGhosts([r(2, 3, 'a')], [ghost(9, 'z'), ghost(0, 'y')])).toEqual([
      ghost(0, 'y'), r(2, 3, 'a'), ghost(9, 'z'),
    ])
  })
})
