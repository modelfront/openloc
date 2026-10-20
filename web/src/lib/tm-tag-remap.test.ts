import { describe, it, expect } from 'vitest'
import { tmInsertText, remapTagsToSource, tagUnits } from './tm-tag-remap'

// An ST4 XML source (the parser emits <g id ctype>) matched against a TMX entry
// (an SDLX export pairs with `i`). Inserting the match verbatim would put
// bpt/ept into the target, trip the missing-tags bar and drop the tag on export.
const sourceRich =
  'Keep the valve closed (except for H<g id="1" ctype="x-html-sub">2</g>O) while the pump is running.'
const tmTargetRich =
  'Halten Sie das Ventil geschlossen (außer für H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O), solange die Pumpe läuft.'
const tmTargetPlain =
  'Halten Sie das Ventil geschlossen (außer für H2O), solange die Pumpe läuft.'

describe('tmInsertText', () => {
  it('gives a plain-text file the plain target, never the tags', () => {
    expect(tmInsertText({ target_text: tmTargetPlain, target_rich_text: tmTargetRich }, sourceRich, true))
      .toBe(tmTargetPlain)
  })

  it('re-anchors a TMX match onto the source segment’s tags', () => {
    const got = tmInsertText(
      { target_text: tmTargetPlain, target_rich_text: tmTargetRich },
      sourceRich,
    )
    expect(got).toBe(
      'Halten Sie das Ventil geschlossen (außer für H<g id="1" ctype="x-html-sub">2</g>O), solange die Pumpe läuft.',
    )
    // The entry's own markup must not survive — it breaks export.
    expect(got).not.toContain('bpt')
    expect(got).not.toContain('ept')
  })

  it('falls back to plain text when the tag counts differ', () => {
    // Two tags in the source, one in the match: we can't know which is which,
    // so insert plain text and let the missing-tags bar guide placement.
    const twoTagSource =
      'Set <g id="1" ctype="bold">Wet</g> or <g id="2" ctype="bold">Dry</g>.'
    const got = tmInsertText(
      { target_text: tmTargetPlain, target_rich_text: tmTargetRich },
      twoTagSource,
    )
    expect(got).toBe(tmTargetPlain)
  })

  it('never imports tags into an untagged source segment', () => {
    const got = tmInsertText(
      { target_text: tmTargetPlain, target_rich_text: tmTargetRich },
      '',
    )
    expect(got).toBe(tmTargetPlain)
  })

  it('passes plain matches straight through', () => {
    expect(tmInsertText({ target_text: 'Bonjour', target_rich_text: null }, sourceRich))
      .toBe('Bonjour')
    // MT suggestions arrive as plain strings with no rich form.
    expect(tmInsertText({ target_text: 'Bonjour' }, sourceRich)).toBe('Bonjour')
  })

  it('leaves a match that already uses the source’s markup untouched', () => {
    const alreadyRight =
      'Halten Sie das Ventil geschlossen (außer für H<g id="1" ctype="x-html-sub">2</g>O), während die Pumpe läuft.'
    expect(tmInsertText({ target_text: tmTargetPlain, target_rich_text: alreadyRight }, sourceRich))
      .toBe(alreadyRight)
  })

  it('handles a multi-tag segment when the structures line up', () => {
    const src = 'Set <g id="1" ctype="bold">Wet</g> or <g id="2" ctype="italic">Dry</g>.'
    const tgt =
      'Régler <bpt i="1">&lt;b&gt;</bpt>Humide<ept i="1">&lt;/b&gt;</ept> ou <bpt i="2">&lt;i&gt;</bpt>Sec<ept i="2">&lt;/i&gt;</ept>.'
    expect(tmInsertText({ target_text: 'Régler Humide ou Sec.', target_rich_text: tgt }, src))
      .toBe('Régler <g id="1" ctype="bold">Humide</g> ou <g id="2" ctype="italic">Sec</g>.')
  })
})

describe('remapTagsToSource', () => {
  it('returns empty on a unit-count mismatch so callers can fall back', () => {
    expect(remapTagsToSource(
      'a<bpt i="1">x</bpt>b<ept i="1">y</ept>c<ph x="2">z</ph>',
      [{ open: '<g id="1">', close: '</g>' }],
    )).toBe('')
  })

  it('preserves the text around the tags verbatim', () => {
    const got = remapTagsToSource(
      'vor<bpt i="1">x</bpt>mitte<ept i="1">y</ept>nach',
      [{ open: '<g id="9" ctype="bold">', close: '</g>' }],
    )
    expect(got).toBe('vor<g id="9" ctype="bold">mitte</g>nach')
  })
})

// Our parser makes each ST4 <meta-ref> a paired <g>; a TMX export carries the
// same construct as a standalone <ph> placeholder. Counting raw tags sees 6 vs 4 and gives up;
// counting logical units sees 3 vs 3 and lines them up.
describe('pair-vs-placeholder modelling differences', () => {
  const source =
    'For H<g id="1" ctype="x-html-sub">2</g>O, the flow rate can be set to ' +
    '<g id="2" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref linkid=&quot;a&quot;&gt;">Low</g> or ' +
    '<g id="3" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref linkid=&quot;b&quot;&gt;">High</g>.'
  const tmTargetRich =
    'Für H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O kann die Durchflussrate auf ' +
    '<ph x="2">&lt;2/&gt;</ph> oder <ph x="3">&lt;3/&gt;</ph> eingestellt werden.'
  const tmTargetPlain = 'Für H2O kann die Durchflussrate auf  oder  eingestellt werden.'

  it('counts three logical units on each side despite 6 vs 4 raw tags', () => {
    expect(tagUnits(source)).toHaveLength(3)
    expect(tagUnits(tmTargetRich)).toHaveLength(3)
  })

  it('inserts the source tags, keeping the German text', () => {
    const got = tmInsertText(
      { target_text: tmTargetPlain, target_rich_text: tmTargetRich },
      source,
    )
    // Pair → pair keeps the TM's inner text ("2").
    expect(got).toContain('H<g id="1" ctype="x-html-sub">2</g>O kann')
    // Placeholder → pair emits the source's tags with no text: the meta-ref
    // cross-reference (and its linkid) survives export, and ST4 fills the
    // content from the referenced node, which is what the TM encoded.
    expect(got).toContain('auf <g id="2" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref linkid=&quot;a&quot;&gt;"></g> oder')
    expect(got).toContain('linkid=&quot;b&quot;')
    // No foreign markup survives.
    expect(got).not.toContain('bpt')
    expect(got).not.toContain('<ph')
  })

  it('still bails when the unit counts genuinely differ', () => {
    const twoUnitSource = 'Set <g id="1">Wet</g> or <g id="2">Dry</g>.'
    expect(tmInsertText({ target_text: tmTargetPlain, target_rich_text: tmTargetRich }, twoUnitSource))
      .toBe(tmTargetPlain)
  })
})
