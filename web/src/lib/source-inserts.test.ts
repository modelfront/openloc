import { describe, it, expect } from 'vitest'
import { sourceInserts, type SourceInsert } from './source-inserts'
import { getMissingTags } from './missing-tags'
import { getMissingPlaceables, type Placeable } from './placeables'
import { flattenRichText } from './xliff-tiptap'
import { getMissingTerms } from './missing-terms'
import type { TermLookupResult } from '../api/terminology'

const p = (text: string, kind = 'variable'): Placeable => ({ kind, text })

const term = (source_term: string, target_term: string, id = 1): TermLookupResult => ({
  id, concept_id: id, source_term, target_term, mt_translation: '', status: 'approved',
  notes: '', domain: '', term_base_id: 1, term_base_name: 'tb', priority: 0,
})

const label = (i: SourceInsert) =>
  'tag' in i ? `tag:${i.tag.tagType}${i.tag.tagId}` : 'placeable' in i ? `p:${i.placeable.text}` : `t:${i.term.target_term}`

/** What F8 would insert, in order, for this source and target. */
function order(sourceRich: string, placeables: Placeable[], targetRich = '', targetText = '', terms: TermLookupResult[] = []): string[] {
  const flat = flattenRichText(sourceRich)
  const items = sourceInserts(
    sourceRich, flat, placeables, getMissingTags(sourceRich, targetRich), getMissingPlaceables(placeables, targetText),
    getMissingTerms(terms, flat, targetText),
  )
  return items.map(label)
}

describe('sourceInserts', () => {
  it('walks the source left to right, tags and placeables mixed', () => {
    const src = 'Order <bpt id="1">&lt;b&gt;</bpt>XJ-500B<ept id="1">&lt;/b&gt;</ept> for {name}<ph id="2"/>.'
    expect(order(src, [p('XJ-500B', 'code'), p('{name}')])).toEqual(['tag:bpt1', 'p:XJ-500B', 'tag:ept1', 'p:{name}', 'tag:ph2'])
  })

  it('skips what the target already has', () => {
    const src = 'A <ph id="1"/> {x} and {y}'
    expect(order(src, [p('{x}'), p('{y}')], 'A <ph id="1"/>', 'A {x}')).toEqual(['p:{y}'])
  })

  it('a repeated placeable the target has once points at its later occurrence', () => {
    const src = '{0} <ph id="1"/> {0}'
    expect(order(src, [p('{0}'), p('{0}')], '', 'x {0}')).toEqual(['tag:ph1', 'p:{0}'])
  })

  it('works with no tags in the source', () => {
    const src = 'See https://example.com for {name}'
    expect(order(src, [p('https://example.com', 'url'), p('{name}')])).toEqual(['p:https://example.com', 'p:{name}'])
  })

  it('puts missing term targets among the tags and placeables, by where the source term sits', () => {
    const src = 'Reset your <bpt id="1">&lt;b&gt;</bpt>password<ept id="1">&lt;/b&gt;</ept> for {name} in the dashboard'
    const terms = [term('dashboard', 'Dashboard', 2), term('password', 'Passwort')]
    expect(order(src, [p('{name}')], '', '', terms)).toEqual(['tag:bpt1', 't:Passwort', 'tag:ept1', 'p:{name}', 't:Dashboard'])
  })

  it('skips a term the target has, keeping the rest in order', () => {
    const src = 'Reset your password in the dashboard'
    const terms = [term('password', 'Passwort'), term('dashboard', 'Dashboard', 2)]
    expect(order(src, [], '', 'Passwort', terms)).toEqual(['t:Dashboard'])
  })

  it('a term whose target is the missing placeable at the same place is inserted once, as the placeable', () => {
    const src = 'Use the XJ-500B adapter'
    expect(order(src, [p('XJ-500B', 'code')], '', '', [term('XJ-500B', 'XJ-500B')])).toEqual(['p:XJ-500B'])
  })

  it('returns nothing when nothing is missing', () => {
    expect(order('Plain text', [])).toEqual([])
  })
})
