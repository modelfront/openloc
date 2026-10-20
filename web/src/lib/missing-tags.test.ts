import { describe, it, expect } from 'vitest'
import { getMissingTags } from './missing-tags'

// Tags-from-source that the target is missing — the diff behind the "missing
// tags" indicator and what `F8` / `Alt+F8` insert. Pure function: input rich-text strings, output ordered
// list of tags-to-be-inserted (in source order).
//
// Identity key for matching: `tagType + tagId + ctype + open/close`. It
// deliberately excludes the chip's visible label, which depends on the
// editor's full-tags toggle — keying on it would let
// source and target rendered in different modes fail to match. `</g>` chips
// inherit their opening tag's id, so the pair is told apart by direction.

describe('getMissingTags', () => {
  it('returns empty when target already has all source tags', () => {
    const source = 'Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>.'
    const target = 'Pulsa <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept>.'
    expect(getMissingTags(source, target)).toEqual([])
  })

  it('returns empty when source has no tags', () => {
    expect(getMissingTags('Hello world', 'Hola mundo')).toEqual([])
  })

  it('returns empty when target rich-text contains the same tags even with different inner text', () => {
    // Inner content of bpt/ept is structural and not user-editable; identity
    // is tagType+tagId. Same id pair on both sides → not missing.
    const source = 'Foo <bpt id="2"><i></bpt>bar<ept id="2"></i></ept>.'
    const target = '<bpt id="2"><i></bpt>bar<ept id="2"></i></ept>'
    expect(getMissingTags(source, target)).toEqual([])
  })

  it('returns the bpt+ept pair when target deleted them and re-typed plain text', () => {
    // Reproduces the user-reported case: source has paired tags,
    // target deleted them and typed `<1>aquí</1>` as plain characters
    // (which doesn't form chips). Missing tags must appear in source order.
    const source = 'Haga clic <bpt id="1"><b></bpt>aquí<ept id="1"></b></ept> para continuar.'
    const target = 'Haga clic <1>aquí</1> para continuar. zibil'

    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(2)
    expect(missing[0]).toMatchObject({
      tagType: 'bpt',
      tagId: '1',
      closing: false,
    })
    expect(missing[0].rawXml).toContain('<bpt id="1">')
    expect(missing[1]).toMatchObject({
      tagType: 'ept',
      tagId: '1',
      closing: true,
    })
    expect(missing[1].rawXml).toContain('<ept id="1">')
  })

  it('returns missing self-closing ph tags', () => {
    const source = 'Order <ph id="4"/>: ready.'
    const target = 'Pedido : listo.'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(1)
    expect(missing[0]).toMatchObject({
      tagType: 'ph',
      tagId: '4',
    })
    expect(missing[0].rawXml).toBe('<ph id="4"/>')
  })

  it('counts duplicates as a multiset (two source `<ph id="1"/>`, one in target → one missing)', () => {
    const source = '<ph id="1"/> and <ph id="1"/>'
    const target = '<ph id="1"/> y'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(1)
    expect(missing[0]).toMatchObject({ tagType: 'ph', tagId: '1' })
  })

  it('preserves source order across mixed missing tags', () => {
    const source = '<ph id="9"/> middle <bpt id="1"><b></bpt>x<ept id="1"></b></ept>'
    const target = ''
    const missing = getMissingTags(source, target)
    expect(missing.map(t => `${t.tagType}:${t.tagId}${t.closing ? ':close' : ''}`))
      .toEqual(['ph:9', 'bpt:1', 'ept:1:close'])
  })

  it('returns missing tags when target has only some of the source tags', () => {
    // bpt is in target, ept is not — only ept is missing.
    const source = 'Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>.'
    const target = 'Pulsa <bpt id="1"><b></bpt>aquí.'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(1)
    expect(missing[0]).toMatchObject({ tagType: 'ept', tagId: '1' })
  })

  it('handles g-pair tags by inherited id + direction', () => {
    // The <g> opening carries the id; the closing tag has no attributes of
    // its own and inherits them, so the pair is distinguished by `closing`.
    const source = '<g id="1" ctype="bold">x</g>'
    const missing = getMissingTags(source, '')
    expect(missing).toHaveLength(2)
    expect(missing[0]).toMatchObject({ tagType: 'g', tagId: '1', ctype: 'bold', closing: false })
    expect(missing[1]).toMatchObject({ tagType: 'g', tagId: '1', ctype: 'bold', closing: true })
  })

  it('reports WHICH pair is missing when two <g> pairs share a ctype', () => {
    // Pins that the reported close carries its opening id. F8 / Alt+F8 insert
    // these attrs straight into the editor, so an id-less chip in the target
    // could never match its opening tag again.
    const source = '<g id="1" ctype="bold">a</g> and <g id="2" ctype="bold">b</g>'
    const target = '<g id="1" ctype="bold">a</g> und'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(2)
    expect(missing[0]).toMatchObject({ tagType: 'g', tagId: '2', closing: false })
    expect(missing[1]).toMatchObject({ tagType: 'g', tagId: '2', closing: true })
    // The close inherits its opening id rather than being blank.
    expect(missing[1].tagId).not.toBe('')
  })

  it('identifies the right pair when same-ctype pairs are nested', () => {
    // Pins that with nested same-ctype pairs, a target holding the INNER pair
    // does not satisfy the outer pair's close.
    const source = '<g id="1" ctype="bold">a<g id="2" ctype="bold">b</g>c</g>'
    const target = '<g id="2" ctype="bold">b</g>'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(2)
    expect(missing[0]).toMatchObject({ tagType: 'g', tagId: '1', closing: false })
    expect(missing[1]).toMatchObject({ tagType: 'g', tagId: '1', closing: true })
  })

  it('is independent of how chips are labelled on screen', () => {
    // Matching must not change when the user flips the full-tags toggle, so
    // the identity excludes the display label. Labels are derived at
    // render time from these same attrs, so asserting the diff is stable
    // against ctypes that render very differently pins the separation.
    const source = 'A <g id="1" ctype="x-html-sub">2</g> B <ph id="3"/>'
    const target = 'A <g id="1" ctype="x-html-sub">2</g> B'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(1)
    expect(missing[0]).toMatchObject({ tagType: 'ph', tagId: '3' })
    // No label text leaks into the returned structure at all.
    expect(missing[0]).not.toHaveProperty('displayText')
  })

  it('treats raw display shorthand `<1>` typed by hand as NOT counting toward target tag presence', () => {
    // The user typed `<1>` and `</1>` as plain text after
    // deleting the bpt/ept chips. Display shorthand is NOT real XLIFF
    // markup — it carries no metadata — so it must be reported missing
    // regardless of whether `<1>` characters appear in target.
    const source = 'Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>.'
    const target = 'Pulsa <1>aquí</1>.'
    const missing = getMissingTags(source, target)
    expect(missing).toHaveLength(2) // bpt + ept, NOT zero
  })
})
