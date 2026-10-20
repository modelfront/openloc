import { describe, it, expect } from 'vitest'
import { trackChangeRanges, TRACK_DELETE_CLASS, TRACK_GAP_CLASS, TRACK_INSERT_CLASS } from './track-changes'
import { MAX_DIFF_LENGTH } from './text-diff'

/**
 * The ranges as the reader sees them: `[-x]` is a deletion drawn inline,
 * `[+x]` an insertion painted over `text`. A deletion occupies no offsets in
 * `text`, so it is spliced in at its position; removed spaces drawn without a
 * mark are spliced in bare.
 */
function track(text: string, previous: string | null | undefined): string {
  let out = ''
  let pos = 0
  for (const r of trackChangeRanges(text, previous)) {
    out += text.slice(pos, r.start)
    if (r.ghost !== undefined) {
      out += r.className ? `[-${r.ghost}]` : r.ghost
      pos = r.start
    } else {
      out += `[+${text.slice(r.start, r.end)}]`
      pos = r.end
    }
  }
  return out + text.slice(pos)
}

describe('trackChangeRanges', () => {
  // A segment with no earlier text has nothing to compare against: the first
  // version of a translation is not a change to anything.
  it.each([null, undefined, ''])('marks nothing when the previous version is %j', (previous) => {
    expect(trackChangeRanges('Denuncia enviada', previous)).toEqual([])
  })

  it('marks nothing when the text is unchanged or empty', () => {
    expect(trackChangeRanges('Denuncia enviada', 'Denuncia enviada')).toEqual([])
    expect(trackChangeRanges('', 'Denuncia enviada')).toEqual([])
  })

  it('shows a replaced word as the old one struck out, then the new one', () => {
    expect(track('Denuncia recibida', 'Denuncia enviada')).toBe('Denuncia [-enviada][+recibida]')
  })

  // Character-level diffs split words that share letters ("envia|da" vs
  // "recibi|da"), which is unreadable once the deleted half is drawn inline.
  it('never splits a word', () => {
    expect(track('Pulse Guardar para seguir.', 'Haga clic en Guardar para continuar.'))
      .toBe('[-Haga clic en][+Pulse] Guardar para [-continuar][+seguir].')
  })

  it('segments words in scripts written without spaces', () => {
    expect(track('パスワードを再設定してください', 'パスワードをリセットしてください'))
      .toBe('パスワードを[-リセット][+再設定]してください')
  })

  // The tint hugs the words: a space at the edge of a change stays outside it,
  // so the marks read as words, not as words plus a stray coloured gap.
  it('marks a pure insertion without a deletion, leaving its edge space unmarked', () => {
    expect(track('Reset your admin password', 'Reset your password')).toBe('Reset your [+admin] password')
  })

  it('marks a pure deletion without painting anything, leaving its edge space unmarked', () => {
    const ranges = trackChangeRanges('Reset your password', 'Reset your admin password')
    expect(ranges.every(r => r.ghost !== undefined && r.start === r.end)).toBe(true)
    expect(track('Reset your password', 'Reset your admin password')).toBe('Reset your [-admin] password')
  })

  it('places a deletion at the very end at the end of the text', () => {
    const ranges = trackChangeRanges('Hola', 'Hola mundo')
    expect(ranges.map(r => [r.start, r.ghost, r.className])).toEqual([
      [4, ' ', ''],
      [4, 'mundo', TRACK_DELETE_CLASS],
    ])
  })

  // A change that is only whitespace — a double space fixed — has no word to
  // hug, so the space itself is marked, or the change would be invisible.
  it('marks a change that is only whitespace', () => {
    expect(track('Hola mundo', 'Hola  mundo')).toBe('Hola[-  ][+ ]mundo')
  })

  it('styles insertions and deletions differently, each with a tooltip', () => {
    const ranges = trackChangeRanges('Denuncia recibida', 'Denuncia enviada')
    expect(ranges).toEqual([
      { start: 9, end: 9, ghost: 'enviada', className: `${TRACK_DELETE_CLASS} ${TRACK_GAP_CLASS}`, title: 'Removed' },
      { start: 9, end: 17, className: TRACK_INSERT_CLASS, title: 'Added' },
    ])
  })

  // The added run's highlight reaches 2px past its glyphs on both sides
  // (HIGHLIGHT_CLASS), so a removed run touching it would merge into it.
  it('spaces a removed run from the added run it touches, and only that one', () => {
    const touching = trackChangeRanges('Denuncia recibida', 'Denuncia enviada').find(r => r.ghost === 'enviada')
    expect(touching?.className.split(' ')).toContain(TRACK_GAP_CLASS)

    const alone = trackChangeRanges('Reset your password', 'Reset your admin password').find(r => r.ghost === 'admin')
    expect(alone?.className.split(' ')).not.toContain(TRACK_GAP_CLASS)

    // An added run that starts with a space is already parted from it.
    expect(track('Hola gente', 'Hola!')).toBe('Hola[-!] [+gente]')
    const spaced = trackChangeRanges('Hola gente', 'Hola!').find(r => r.ghost === '!')
    expect(spaced?.className.split(' ')).not.toContain(TRACK_GAP_CLASS)
  })

  it('gives up on texts past the diff length cap', () => {
    const long = 'palabra '.repeat(MAX_DIFF_LENGTH / 4)
    expect(trackChangeRanges(`${long}nueva`, long)).toEqual([])
  })
})
