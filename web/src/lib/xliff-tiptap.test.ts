import { describe, it, expect } from 'vitest'
import { chipLabel, toLabelAttrs } from './xliff-tag-label'
import { richTextToTipTapContent, tipTapToRichText, hasXliffTags } from './xliff-tiptap'

describe('hasXliffTags', () => {
  it('detects bpt tags', () => {
    expect(hasXliffTags('Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>')).toBe(true)
  })
  it('detects ph tags', () => {
    expect(hasXliffTags('Press <ph id="2">{key}</ph> to submit')).toBe(true)
  })
  it('returns false for plain text', () => {
    expect(hasXliffTags('plain text')).toBe(false)
  })
  // TipTap-mark tags are NOT chips. <strong>/<em>/<sub>/<sup> are
  // applied via Bold/Italic/Subscript/Superscript extensions on Mod+B etc.,
  // and must round-trip as inline marks via the HTML-string path in
  // textToContent — NOT through the chip parser.
  it('returns false for TipTap-mark HTML (strong / em / sub / sup)', () => {
    expect(hasXliffTags('This is <strong>bold</strong>')).toBe(false)
    expect(hasXliffTags('This is <em>italic</em>')).toBe(false)
    expect(hasXliffTags('H<sub>2</sub>O')).toBe(false)
    expect(hasXliffTags('x<sup>2</sup>')).toBe(false)
  })
  it('detects g tags', () => {
    expect(hasXliffTags('Click <g id="1" ctype="bold">here</g> to continue')).toBe(true)
  })
  it('detects x self-closing tags', () => {
    expect(hasXliffTags('Line one<x id="1" ctype="lb"/>Line two')).toBe(true)
  })
  // hasXliffTags only matches XLIFF rich-text markers,
  // NOT raw HTML in plain text. The chip pipeline is fed via richText
  // populated at IMPORT time by the Go-side parsers (inline_tag.go), which
  // convert HTML inline tags into XLIFF <g>/<x> wrappers before storage.
  it('returns false for raw HTML in plain text (chip path is richText-only)', () => {
    expect(hasXliffTags('Say <i>"Alexa"</i>')).toBe(false)
    expect(hasXliffTags('Press <b>Enter</b>')).toBe(false)
    expect(hasXliffTags('Click <a href="/foo">here</a>')).toBe(false)
    expect(hasXliffTags('one<br>two')).toBe(false)
  })
  it('returns true for XLIFF <g> wrappers (chip pipeline input)', () => {
    expect(hasXliffTags('<g id="1" ctype="italic">Alexa</g>')).toBe(true)
    expect(hasXliffTags('one<x id="1" ctype="lb" original_tag="br"/>two')).toBe(true)
  })
})

describe('richTextToTipTapContent', () => {
  it('converts plain text to doc with paragraph', () => {
    const result = richTextToTipTapContent('Hello world')
    expect(result.type).toBe('doc')
    expect(result.content).toHaveLength(1)
    expect(result.content![0].type).toBe('paragraph')
    expect(result.content![0].content).toHaveLength(1)
    expect(result.content![0].content![0].text).toBe('Hello world')
  })

  it('converts bpt/ept tags to xliffTag nodes', () => {
    const result = richTextToTipTapContent('Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue')
    const content = result.content![0].content!
    expect(content).toHaveLength(5) // "Click " + bpt + "here" + ept + " to continue"
    expect(content[0]).toEqual({ type: 'text', text: 'Click ' })
    expect(content[1].type).toBe('xliffTag')
    expect(content[1].attrs!.tagType).toBe('bpt')
    expect(content[1].attrs!.tagId).toBe('1')
    expect(content[2]).toEqual({ type: 'text', text: 'here' })
    expect(content[3].type).toBe('xliffTag')
    expect(content[3].attrs!.tagType).toBe('ept')
    expect(content[4]).toEqual({ type: 'text', text: ' to continue' })
  })

  it('converts bpt/ept with HTML entity content (DB format)', () => {
    // DB stores HTML entities inside bpt/ept: &lt;b&gt; instead of <b>
    const input = 'Haga clic <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept> para continuar.'
    const result = richTextToTipTapContent(input)
    const content = result.content![0].content!
    expect(content).toHaveLength(5) // text + bpt + text + ept + text
    expect(content[0]).toEqual({ type: 'text', text: 'Haga clic ' })
    expect(content[1].type).toBe('xliffTag')
    expect(content[1].attrs!.tagType).toBe('bpt')
    expect(content[1].attrs!.tagId).toBe('1')
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('<1>')
    expect(content[2]).toEqual({ type: 'text', text: 'aquí' })
    expect(content[3].type).toBe('xliffTag')
    expect(content[3].attrs!.tagType).toBe('ept')
    expect(content[3].attrs!.tagId).toBe('1')
    expect(chipLabel(toLabelAttrs(content[3].attrs))).toBe('</1>')
    expect(content[4]).toEqual({ type: 'text', text: ' para continuar.' })
  })

  it('converts ph tags with visible content', () => {
    const result = richTextToTipTapContent('Press <ph id="2">{key}</ph> to submit')
    const content = result.content![0].content!
    expect(content).toHaveLength(3) // "Press " + ph + " to submit"
    expect(content[1].type).toBe('xliffTag')
    expect(content[1].attrs!.tagType).toBe('ph')
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('{key}')
  })

  it('converts g tags to opening chip + text + closing chip', () => {
    const result = richTextToTipTapContent('Click <g id="1" ctype="bold">here</g> to continue')
    const content = result.content![0].content!
    // "Click " + <1> chip + "here" text + </1> chip + " to continue"
    expect(content).toHaveLength(5)
    expect(content[0]).toEqual({ type: 'text', text: 'Click ' })
    expect(content[1].type).toBe('xliffTag')
    expect(content[1].attrs!.tagType).toBe('g')
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('<1>')
    expect(content[1].attrs!.rawXml).toBe('<g id="1" ctype="bold">')
    expect(content[2]).toEqual({ type: 'text', text: 'here' })
    expect(content[3].type).toBe('xliffTag')
    expect(content[3].attrs!.tagType).toBe('g')
    expect(chipLabel(toLabelAttrs(content[3].attrs))).toBe('</1>')
    expect(content[3].attrs!.rawXml).toBe('</g>')
    expect(content[4]).toEqual({ type: 'text', text: ' to continue' })
  })

  it('converts nested g tags (bold + italic)', () => {
    const input = '<g id="1" ctype="bold"><g id="2" ctype="italic">(some text)</g></g>'
    const result = richTextToTipTapContent(input)
    const content = result.content![0].content!
    // <1> chip + <2> chip + "(some text)" + </2> chip + </1> chip
    expect(content).toHaveLength(5)
    expect(content[0].type).toBe('xliffTag')
    expect(chipLabel(toLabelAttrs(content[0].attrs))).toBe('<1>')
    expect(content[1].type).toBe('xliffTag')
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('<2>')
    expect(content[2]).toEqual({ type: 'text', text: '(some text)' })
    expect(content[3].type).toBe('xliffTag')
    expect(chipLabel(toLabelAttrs(content[3].attrs))).toBe('</2>')
    expect(content[4].type).toBe('xliffTag')
    expect(chipLabel(toLabelAttrs(content[4].attrs))).toBe('</1>')
  })

  it('converts nested g tags with text between', () => {
    const input = 'Start <g id="1" ctype="bold">bold <g id="2" ctype="italic">both</g> bold</g> end'
    const result = richTextToTipTapContent(input)
    const content = result.content![0].content!
    // "Start " + <1> + "bold " + <2> + "both" + </2> + " bold" + </1> + " end"
    expect(content).toHaveLength(9)
    expect(content[0]).toEqual({ type: 'text', text: 'Start ' })
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('<1>')
    expect(content[2]).toEqual({ type: 'text', text: 'bold ' })
    expect(chipLabel(toLabelAttrs(content[3].attrs))).toBe('<2>')
    expect(content[4]).toEqual({ type: 'text', text: 'both' })
    expect(chipLabel(toLabelAttrs(content[5].attrs))).toBe('</2>')
    expect(content[6]).toEqual({ type: 'text', text: ' bold' })
    expect(chipLabel(toLabelAttrs(content[7].attrs))).toBe('</1>')
    expect(content[8]).toEqual({ type: 'text', text: ' end' })
  })

  it('converts self-closing x ctype="lb" tags to hardBreak nodes', () => {
    // Line-break self-closing tags emit hardBreak nodes
    // (visual line break + ¶ marker via WhitespaceMarkers), not inline chips.
    const result = richTextToTipTapContent('Line one<x id="1" ctype="lb"/> Line two')
    const content = result.content![0].content!
    expect(content).toHaveLength(3)
    expect(content[0]).toEqual({ type: 'text', text: 'Line one' })
    expect(content[1]).toEqual({ type: 'hardBreak' })
    expect(content[2]).toEqual({ type: 'text', text: ' Line two' })
  })

  it('converts non-lb self-closing x tags to inline chips', () => {
    // Self-closing tags with other ctypes (or no ctype) stay as inline chips.
    const result = richTextToTipTapContent('Press <x id="3" ctype="image"/> button')
    const content = result.content![0].content!
    expect(content).toHaveLength(3)
    expect(content[1].type).toBe('xliffTag')
    expect(content[1].attrs!.tagType).toBe('x')
    expect(chipLabel(toLabelAttrs(content[1].attrs))).toBe('{3}')
  })

  it('decodes ref_key at ingestion so node attrs carry the original segment key', () => {
    // The parser XML-escapes ref_key on the way into source_rich_text. RefTerms
    // maps and segment indexes are keyed on the DECODED form (collectRefKeys),
    // so the node attr must be decoded here — otherwise every consumer
    // (resolver, data-ref-key, click-to-jump) would need its own decode and one
    // of them would forget.
    const rich = '<x id="1" ctype="x-xml-meta-ref" ref_key="/tms/a[@name=&quot;b&amp;c&quot;]/meta"/> rest'
    const result = richTextToTipTapContent(rich)
    const chip = result.content![0].content![0]
    expect(chip.type).toBe('xliffTag')
    expect(chip.attrs!.refKey).toBe('/tms/a[@name="b&c"]/meta')
  })
})

describe('tipTapToRichText', () => {
  it('round-trips bpt/ept tags', () => {
    const original = 'Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  it('round-trips ph tags', () => {
    const original = 'Press <ph id="2">{key}</ph> to submit'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  it('handles plain text (no tags)', () => {
    const tiptap = richTextToTipTapContent('Hello world')
    const result = tipTapToRichText(tiptap)
    expect(result).toBe('Hello world')
  })

  it('round-trips g tags', () => {
    const original = 'Click <g id="1" ctype="bold">here</g> to continue'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  it('round-trips ctype="lb" via hardBreak (with original_tag attribute on emit)', () => {
    // Ctype="lb" round-trips through a hardBreak node,
    // so the serialized form has `original_tag="br"` added on emit. The
    // semantic (XLIFF line break) is preserved; the export-side
    // `XLIFFInlineToHTML` reads `original_tag` to restore <br>.
    const tiptap = richTextToTipTapContent('Line one<x id="1" ctype="lb"/> Line two')
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(`Line one<x id="1" ctype="lb" original_tag="br"/> Line two`)
  })

  it('round-trips non-lb self-closing x tags verbatim', () => {
    const original = 'Press <x id="3" ctype="image"/> button'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  it('round-trips nested g tags', () => {
    const original = '<g id="1" ctype="bold"><g id="2" ctype="italic">(some text)</g></g>'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  it('round-trips nested g tags with text between', () => {
    const original = 'Start <g id="1" ctype="bold">bold <g id="2" ctype="italic">both</g> bold</g> end'
    const tiptap = richTextToTipTapContent(original)
    const result = tipTapToRichText(tiptap)
    expect(result).toBe(original)
  })

  // HTML inline tag round-trip.
  it('round-trips paired HTML tags as chip pairs with editable text between', () => {
    const original = 'Say <i>"Alexa"</i> to start'
    const tiptap = richTextToTipTapContent(original)
    const para = tiptap.content![0].content!
    // text, openChip(<1>), text, closeChip(</1>), text
    expect(para.length).toBe(5)
    expect(para[0]).toEqual({ type: 'text', text: 'Say ' })
    expect(para[1].type).toBe('xliffTag')
    expect(para[1].attrs?.tagType).toBe('i')
    expect(chipLabel(toLabelAttrs(para[1].attrs))).toBe('<1>')
    // Full-tags mode recovers the element name from the synthesized ctype.
    expect(chipLabel(toLabelAttrs(para[1].attrs), 'full')).toBe('<i>')
    expect(para[2]).toEqual({ type: 'text', text: '"Alexa"' })
    expect(para[3].type).toBe('xliffTag')
    expect(para[3].attrs?.tagType).toBe('i')
    expect(chipLabel(toLabelAttrs(para[3].attrs))).toBe('</1>')
    expect(chipLabel(toLabelAttrs(para[3].attrs), 'full')).toBe('</i>')
    expect(para[4]).toEqual({ type: 'text', text: ' to start' })

    // Round-trip preserves the raw HTML.
    expect(tipTapToRichText(tiptap)).toBe(original)
  })

  it('round-trips HTML void elements (<br>, <br/>) as self-closing chips', () => {
    const tiptap = richTextToTipTapContent('one<br>two<br/>three')
    const chips = tiptap.content![0].content!.filter(n => n.type === 'xliffTag')
    expect(chips.length).toBe(2)
    expect(chips.every(c => c.attrs?.tagType === 'br')).toBe(true)
  })
})
