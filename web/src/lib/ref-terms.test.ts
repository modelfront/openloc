import { describe, it, expect } from 'vitest'
import {
  collectRefKeys,
  segmentRefKeys,
  refTermsSignature,
  buildRefTerms,
  indexSegments,
  indexTargets,
  refResolver,
} from './ref-terms'

// Shapes taken from the real ST4 parse: the reference chip carries the segment
// key of the <meta> node whose translation it renders.
const REF_KEY = '/tms/tmsnode[14]/metalist/meta'

const segments = [
  {
    id: 1,
    segment_key: '/tms/tmsnode[13]/content/p[9]',
    source_text: 'For CO2, the humidity correction can be set to  or .',
    source_rich_text:
      'For CO<g id="1" ctype="x-html-sub">2</g>, the humidity correction can be set to ' +
      `<x id="2" ctype="x-xml-meta-ref" ref_key="${REF_KEY}"/> or .`,
  },
  { id: 2, segment_key: REF_KEY, source_text: 'Wet', source_rich_text: null },
  { id: 3, segment_key: '/tms/tmsnode[13]/content/p[1]', source_text: 'Unrelated', source_rich_text: null },
]

describe('collectRefKeys', () => {
  it('finds the segment keys chips point at', () => {
    expect(collectRefKeys(segments)).toEqual([REF_KEY])
  })

  it('returns nothing for a file with no cross-references', () => {
    expect(collectRefKeys([segments[1], segments[2]])).toEqual([])
  })
})

describe('segmentRefKeys', () => {
  it('returns one segment\'s referenced keys in document order, deduplicated', () => {
    const rich =
      `A <x id="1" ctype="x-xml-meta-ref" ref_key="/k/b"/> then ` +
      `<x id="2" ctype="x-xml-meta-ref" ref_key="/k/a"/> and again ` +
      `<x id="3" ctype="x-xml-meta-ref" ref_key="/k/b"/>.`
    // Document order (b before a), not sorted — the sidebar lists references
    // in the order the translator reads them.
    expect(segmentRefKeys(rich)).toEqual(['/k/b', '/k/a'])
  })

  it('decodes the XML-escaped attribute back to the original key', () => {
    const rich = `<x id="1" ctype="x-xml-meta-ref" ref_key="/k[@n=&quot;x&quot;]"/>`
    expect(segmentRefKeys(rich)).toEqual(['/k[@n="x"]'])
  })

  it('is empty for plain or tag-only rich text and for null', () => {
    expect(segmentRefKeys(null)).toEqual([])
    expect(segmentRefKeys('no tags here')).toEqual([])
    expect(segmentRefKeys('a <g id="1" ctype="x-html-b">b</g>')).toEqual([])
  })
})

describe('resolution', () => {
  const { idByKey, sourceByKey } = indexSegments(segments)

  it('resolves to the translated term once the defining node is translated', () => {
    const targets = indexTargets([{ segment_id: 2, active_text: 'Feucht' }])
    const terms = buildRefTerms([REF_KEY], targets, idByKey, sourceByKey)

    expect(refResolver(terms, 'target')(REF_KEY)).toBe('Feucht')
    // The source column keeps showing the source term — it mirrors the source
    // document, not the deliverable.
    expect(refResolver(terms, 'source')(REF_KEY)).toBe('Wet')
  })

  it('falls back to the source term so a chip never goes blank', () => {
    const terms = buildRefTerms([REF_KEY], indexTargets([]), idByKey, sourceByKey)
    expect(refResolver(terms, 'target')(REF_KEY)).toBe('Wet')
  })

  it('returns undefined for an unknown key, so the chip shows its number', () => {
    const terms = buildRefTerms([REF_KEY], indexTargets([]), idByKey, sourceByKey)
    expect(refResolver(terms, 'target')('/no/such/segment')).toBeUndefined()
  })
})

// The grid is virtualized and SegmentRow is memo'd on an explicit field list.
// If the map's identity churned on every keystroke, every visible row would
// re-render on every edit — a regression that stays invisible until the editor
// feels slow, which is exactly the kind that ships.
describe('memo stability', () => {
  const { idByKey } = indexSegments(segments)

  it('signature is unchanged when an UNRELATED segment is edited', () => {
    const before = refTermsSignature([REF_KEY], indexTargets([
      { segment_id: 2, active_text: 'Feucht' },
      { segment_id: 3, active_text: 'Etwas' },
    ]), idByKey)

    const after = refTermsSignature([REF_KEY], indexTargets([
      { segment_id: 2, active_text: 'Feucht' },
      { segment_id: 3, active_text: 'Etwas anderes — still typing…' },
    ]), idByKey)

    expect(after).toBe(before)
  })

  it('signature changes when a REFERENCED term is edited', () => {
    const before = refTermsSignature([REF_KEY], indexTargets([
      { segment_id: 2, active_text: 'Feucht' },
    ]), idByKey)

    const after = refTermsSignature([REF_KEY], indexTargets([
      { segment_id: 2, active_text: 'Nass' },
    ]), idByKey)

    expect(after).not.toBe(before)
  })

  it('is empty work when the file has no references at all', () => {
    expect(refTermsSignature([], new Map(), idByKey)).toBe('')
  })
})
