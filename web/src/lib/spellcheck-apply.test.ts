// @vitest-environment node
import { describe, it, expect } from 'vitest'
import {
  buildSpellFixes, replaceOccurrenceInRich, countOccurrences, richOccurrences,
  type TranslationTextRow,
} from './spellcheck-apply'
import type { SpellError } from './spellcheck-run'

function err(over: Partial<SpellError> & { translationId: number; offset: number }): SpellError {
  return {
    translationId: over.translationId,
    segmentId: 1,
    segmentIndex: 0,
    offset: over.offset,
    length: over.length ?? 7,
    word: over.word ?? 'adresse',
    message: 'typo',
    suggestions: ['Adresse'],
    ruleCategory: 'TYPOS',
  }
}

function rows(map: Record<number, TranslationTextRow>) {
  return (id: number) => map[id]
}

describe('buildSpellFixes', () => {
  it('produces one fix with the replacement applied', () => {
    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset: 21 })],
      'Adresse',
      rows({ 1: { active_text: 'Senden Sie es an die adresse unten.', updated_at: '2026-08-10T00:00:00Z' } }),
    )
    expect(unfixable).toHaveLength(0)
    expect(fixes).toEqual([{
      translation_id: 1,
      human_text: 'Senden Sie es an die Adresse unten.',
      human_rich_text: undefined,
      expected_updated_at: '2026-08-10T00:00:00Z',
    }])
  })

  // Two findings in one segment is the case that a naive implementation gets
  // wrong: fixing the earlier one shifts the later offset.
  it('applies several findings in one segment back-to-front', () => {
    const text = 'die adresse und die adresse'
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 }), err({ translationId: 1, offset: 20 })],
      'Adresse',
      rows({ 1: { active_text: text } }),
    )
    expect(fixes).toHaveLength(1)
    expect(fixes[0].human_text).toBe('die Adresse und die Adresse')
  })

  it('emits exactly one fix per translation so optimistic locking holds', () => {
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 }), err({ translationId: 1, offset: 20 })],
      'Adresse',
      rows({ 1: { active_text: 'die adresse und die adresse', updated_at: 'T0' } }),
    )
    expect(fixes).toHaveLength(1)
    expect(fixes.filter(f => f.translation_id === 1)).toHaveLength(1)
  })

  it('groups findings across several translations', () => {
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 }), err({ translationId: 2, offset: 4 })],
      'Adresse',
      rows({
        1: { active_text: 'die adresse eins' },
        2: { active_text: 'die adresse zwei' },
      }),
    )
    expect(fixes.map(f => f.translation_id).sort()).toEqual([1, 2])
    expect(fixes.every(f => f.human_text.includes('Adresse'))).toBe(true)
  })

  it('rewrites the rich form too when the segment carries tags', () => {
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 })],
      'Adresse',
      rows({
        1: {
          active_text: 'die adresse unten',
          active_rich_text: '<ph id="1"/>die adresse unten',
        },
      }),
    )
    expect(fixes[0].human_text).toBe('die Adresse unten')
    expect(fixes[0].human_rich_text).toBe('<ph id="1"/>die Adresse unten')
  })

  // Demo file segment 32. `active_text` keeps the placeholder's content
  // (`{orderId}`) while the rich form wraps it in `<ph>` markup, so the two
  // strings disagree on plain-text length. An offset carried from one into the
  // other lands 9 characters off; anchoring on the word occurrence doesn't.
  it('handles a placeholder whose content is part of the plain text', () => {
    const plain = 'Senden Sie die {orderId} Bestätigung an die adresse.'
    const rich = 'Senden Sie die <ph id="4">{orderId}</ph> Bestätigung an die adresse.'
    const offset = plain.indexOf('adresse')

    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset })],
      'Adresse',
      rows({ 1: { active_text: plain, active_rich_text: rich } }),
    )

    expect(unfixable).toHaveLength(0)
    expect(fixes[0].human_text).toBe('Senden Sie die {orderId} Bestätigung an die Adresse.')
    expect(fixes[0].human_rich_text)
      .toBe('Senden Sie die <ph id="4">{orderId}</ph> Bestätigung an die Adresse.')
  })

  // Paired markup is dropped from the plain form — the opposite drift direction.
  it('handles bpt/ept markup that is absent from the plain text', () => {
    const plain = 'Klicken Sie hier fur die adresse.'
    const rich = 'Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> fur die adresse.'
    const offset = plain.indexOf('adresse')

    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset })],
      'Adresse',
      rows({ 1: { active_text: plain, active_rich_text: rich } }),
    )

    expect(unfixable).toHaveLength(0)
    expect(fixes[0].human_rich_text).toBe(
      'Klicken Sie <bpt id="1">&lt;b&gt;</bpt>hier<ept id="1">&lt;/b&gt;</ept> fur die Adresse.',
    )
  })

  it('replaces the right occurrence when the word repeats around a tag', () => {
    const plain = 'die adresse und {x} die adresse'
    const rich = 'die adresse und <ph id="9">{x}</ph> die adresse'
    const second = plain.indexOf('adresse', plain.indexOf('adresse') + 1)

    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: second })],
      'Adresse',
      rows({ 1: { active_text: plain, active_rich_text: rich } }),
    )
    expect(fixes[0].human_rich_text).toBe('die adresse und <ph id="9">{x}</ph> die Adresse')
  })

  it('leaves rich text alone for tag-free segments', () => {
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 })],
      'Adresse',
      rows({ 1: { active_text: 'die adresse', active_rich_text: 'die adresse' } }),
    )
    expect(fixes[0].human_rich_text).toBeUndefined()
  })

  // Refusing is the whole point: a splice across a chip would delete the tag,
  // and the loss surfaces much later, at export.
  it('reports a finding that straddles a tag as unfixable', () => {
    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset: 1, length: 2, word: 'bc' })],
      'XY',
      rows({ 1: { active_text: 'abcd', active_rich_text: 'ab<ph id="1"/>cd' } }),
    )
    expect(fixes).toHaveLength(0)
    expect(unfixable).toHaveLength(1)
  })

  it('reports an offset past the end of the text as unfixable', () => {
    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset: 40 })],
      'Adresse',
      rows({ 1: { active_text: 'short' } }),
    )
    expect(fixes).toHaveLength(0)
    expect(unfixable).toHaveLength(1)
  })

  it('reports findings for an unknown translation as unfixable', () => {
    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 99, offset: 0 })],
      'Adresse',
      rows({}),
    )
    expect(fixes).toHaveLength(0)
    expect(unfixable).toHaveLength(1)
  })

  // One bad finding must not discard the other segments' corrections.
  it('keeps other translations when one is unfixable', () => {
    const { fixes, unfixable } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 }), err({ translationId: 2, offset: 99 })],
      'Adresse',
      rows({
        1: { active_text: 'die adresse gut' },
        2: { active_text: 'kurz' },
      }),
    )
    expect(fixes.map(f => f.translation_id)).toEqual([1])
    expect(unfixable.map(e => e.translationId)).toEqual([2])
  })

  it('carries updated_at through for optimistic locking, null when absent', () => {
    const { fixes } = buildSpellFixes(
      [err({ translationId: 1, offset: 4 })],
      'Adresse',
      rows({ 1: { active_text: 'die adresse' } }),
    )
    expect(fixes[0].expected_updated_at).toBeNull()
  })
})

describe('countOccurrences / richOccurrences', () => {
  it('counts non-overlapping matches', () => {
    expect(countOccurrences('aa bb aa', 'aa')).toBe(2)
    expect(countOccurrences('aaaa', 'aa')).toBe(2)
    expect(countOccurrences('none', 'zz')).toBe(0)
  })

  it('ignores text inside tag markup', () => {
    expect(richOccurrences('<ph id="1">x</ph>hello', 'id')).toBe(0)
    expect(richOccurrences('id <ph id="1">x</ph> id', 'id')).toBe(2)
  })
})

describe('replaceOccurrenceInRich', () => {
  it('replaces the first occurrence', () => {
    expect(replaceOccurrenceInRich('die adresse hier', 'adresse', 0, 'Adresse'))
      .toBe('die Adresse hier')
  })

  it('replaces a later occurrence, leaving earlier ones alone', () => {
    expect(replaceOccurrenceInRich('a adresse b adresse', 'adresse', 1, 'X'))
      .toBe('a adresse b X')
  })

  it('counts occurrences across text runs split by a tag', () => {
    expect(replaceOccurrenceInRich('adresse <ph id="1"/> adresse', 'adresse', 1, 'X'))
      .toBe('adresse <ph id="1"/> X')
  })

  it('never rewrites tag markup', () => {
    expect(replaceOccurrenceInRich('<ph id="1">adresse</ph> adresse', 'adresse', 0, 'X'))
      .toBe('<ph id="1">adresse</ph> X')
  })

  it('returns null when the occurrence does not exist', () => {
    expect(replaceOccurrenceInRich('die adresse', 'adresse', 5, 'X')).toBeNull()
    expect(replaceOccurrenceInRich('nothing here', 'adresse', 0, 'X')).toBeNull()
  })

  it('returns null when the word straddles a tag', () => {
    expect(replaceOccurrenceInRich('adr<ph id="1"/>esse', 'adresse', 0, 'X')).toBeNull()
  })
})
