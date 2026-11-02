import { describe, it, expect } from 'vitest'
import { segmentRefKeys, refResolver, type RefTerms } from './ref-terms'

// Shapes taken from the real ST4 parse: the reference chip carries the segment
// key of the <meta> node whose translation it renders.
const REF_KEY = '/tms/tmsnode[14]/metalist/meta'

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
  const terms = (target: string): RefTerms => new Map([[REF_KEY, { source: 'Wet', target }]])

  it('resolves to the translated term once the defining node is translated', () => {
    expect(refResolver(terms('Feucht'), 'target')(REF_KEY)).toBe('Feucht')
    // The source column keeps showing the source term — it mirrors the source
    // document, not the deliverable.
    expect(refResolver(terms('Feucht'), 'source')(REF_KEY)).toBe('Wet')
  })

  it('falls back to the source term so a chip never goes blank', () => {
    expect(refResolver(terms(''), 'target')(REF_KEY)).toBe('Wet')
  })

  it('returns undefined for an unknown key, so the chip shows its number', () => {
    expect(refResolver(terms('Feucht'), 'target')('/no/such/segment')).toBeUndefined()
  })
})
