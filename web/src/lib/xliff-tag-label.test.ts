import { describe, it, expect } from 'vitest'
import {
  ctypeToElementName,
  chipLabel,
  chipTitle,
  labelModeFor,
  toLabelAttrs,
  type TagLabelAttrs,
} from './xliff-tag-label'
import { richTextToTipTapContent } from './xliff-tiptap'

describe('ctypeToElementName', () => {
  it('maps ctypes that do not encode their element name', () => {
    expect(ctypeToElementName('bold')).toBe('b')
    expect(ctypeToElementName('italic')).toBe('i')
    expect(ctypeToElementName('underlined')).toBe('u')
    expect(ctypeToElementName('lb')).toBe('br')
    expect(ctypeToElementName('image')).toBe('img')
  })

  it('strips the x-html- prefix', () => {
    expect(ctypeToElementName('x-html-sub')).toBe('sub')
    expect(ctypeToElementName('x-html-sup')).toBe('sup')
    expect(ctypeToElementName('x-html-span')).toBe('span')
    expect(ctypeToElementName('x-html-a')).toBe('a')
    expect(ctypeToElementName('x-html-em')).toBe('em')
    expect(ctypeToElementName('x-html-strong')).toBe('strong')
    expect(ctypeToElementName('x-html-code')).toBe('code')
  })

  it('strips the x-xml- prefix, including hyphenated element names', () => {
    // Unknown XML elements are encoded as x-xml-<name>, which is what
    // makes the element recoverable without a backend change.
    expect(ctypeToElementName('x-xml-meta-ref')).toBe('meta-ref')
    expect(ctypeToElementName('x-xml-subheading')).toBe('subheading')
  })

  it('passes an unrecognized ctype through rather than inventing a name', () => {
    expect(ctypeToElementName('protected')).toBe('protected')
  })

  it('returns empty for a missing ctype so callers fall back to the number', () => {
    expect(ctypeToElementName(undefined)).toBe('')
    expect(ctypeToElementName('')).toBe('')
  })
})

describe('chipLabel', () => {
  const gOpen: TagLabelAttrs = { tagType: 'g', tagId: '1', ctype: 'x-html-sup' }
  const gClose: TagLabelAttrs = { tagType: 'g', tagId: '1', ctype: 'x-html-sup', closing: true }
  const x: TagLabelAttrs = { tagType: 'x', tagId: '4', ctype: 'lb' }

  it('numbers paired tags with angle brackets by default', () => {
    expect(chipLabel(gOpen)).toBe('<1>')
    expect(chipLabel(gClose)).toBe('</1>')
  })

  it('numbers standalone tags with braces by default', () => {
    expect(chipLabel(x)).toBe('{4}')
  })

  it('shows element names in full mode', () => {
    expect(chipLabel(gOpen, 'full')).toBe('<sup>')
    expect(chipLabel(gClose, 'full')).toBe('</sup>')
    expect(chipLabel(x, 'full')).toBe('{br}')
  })

  it('renders the user-facing examples from the ST4 and HTML cases', () => {
    expect(chipLabel({ tagType: 'g', tagId: '1', ctype: 'x-html-sub' }, 'full')).toBe('<sub>')
    expect(chipLabel({ tagType: 'g', tagId: '2', ctype: 'x-html-span' }, 'full')).toBe('<span>')
    expect(chipLabel({ tagType: 'g', tagId: '3', ctype: 'x-html-a' }, 'full')).toBe('<a>')
    expect(chipLabel({ tagType: 'g', tagId: '4', ctype: 'x-xml-meta-ref' }, 'full')).toBe('<meta-ref>')
  })

  it('falls back to the ordinal when the markup carries no id', () => {
    // HTML paired tags in rich text (<i>, <b>) have no id attribute.
    const noId: TagLabelAttrs = { tagType: 'g', ctype: 'italic', tagNum: 7 }
    expect(chipLabel(noId)).toBe('<7>')
    expect(chipLabel(noId, 'full')).toBe('<i>')
  })

  it('falls back to the number in full mode when the element is unknown', () => {
    expect(chipLabel({ tagType: 'g', tagId: '2' }, 'full')).toBe('<2>')
  })

  it('shows ? when neither an id nor an ordinal is available', () => {
    expect(chipLabel({ tagType: 'x' })).toBe('{?}')
  })

  it('shows a placeholder by its content, not its number, in both modes', () => {
    // A translator has to tell %s from {count} to place it correctly; a bare
    // {2} hides exactly the information they need.
    // Verbatim: the content brings its own delimiters, so re-wrapping would
    // render `{{key}}`.
    const ph: TagLabelAttrs = { tagType: 'ph', tagId: '2', rawXml: '<ph id="2">{key}</ph>' }
    expect(chipLabel(ph)).toBe('{key}')
    expect(chipLabel(ph, 'full')).toBe('{key}')

    const printf: TagLabelAttrs = { tagType: 'ph', tagId: '3', rawXml: '<ph id="3">%1$s</ph>' }
    expect(chipLabel(printf)).toBe('%1$s')
  })

  it('falls back to the number for a placeholder with no content', () => {
    expect(chipLabel({ tagType: 'ph', tagId: '2', rawXml: '<ph id="2"/>' })).toBe('{2}')
  })

  it('recovers the element for bpt/ept from their escaped content', () => {
    // bpt/ept carry the original markup as content instead of a ctype.
    const bpt: TagLabelAttrs = {
      tagType: 'bpt',
      tagId: '1',
      rawXml: '<bpt id="1">&lt;b&gt;</bpt>',
    }
    const ept: TagLabelAttrs = {
      tagType: 'ept',
      tagId: '1',
      rawXml: '<ept id="1">&lt;/b&gt;</ept>',
      closing: true,
    }
    expect(chipLabel(bpt)).toBe('<1>')
    expect(chipLabel(bpt, 'full')).toBe('<b>')
    expect(chipLabel(ept, 'full')).toBe('</b>')
  })

  it('leaves bpt numeric in full mode when the content is not markup', () => {
    const bpt: TagLabelAttrs = { tagType: 'bpt', tagId: '3', rawXml: '<bpt id="3">plain</bpt>' }
    expect(chipLabel(bpt, 'full')).toBe('<3>')
  })
})

describe('chipTitle', () => {
  it('shows the element form regardless of mode', () => {
    expect(chipTitle({ tagType: 'g', tagId: '1', ctype: 'x-html-sup' })).toBe('<sup>')
    expect(chipTitle({ tagType: 'g', tagId: '1', ctype: 'x-html-sup', closing: true })).toBe('</sup>')
    expect(chipTitle({ tagType: 'x', tagId: '4', ctype: 'lb' })).toBe('<br>')
    expect(chipTitle({ tagType: 'g', tagId: '2', ctype: 'x-xml-meta-ref' })).toBe('<meta-ref>')
  })

  it('falls back to the numeric label rather than an empty tooltip', () => {
    expect(chipTitle({ tagType: 'g', tagId: '5' })).toBe('<5>')
    expect(chipTitle({ tagType: 'ph', tagId: '6' })).toBe('{6}')
  })
})

describe('labelModeFor', () => {
  it('maps the whitespace toggle onto the label mode', () => {
    expect(labelModeFor(true)).toBe('full')
    expect(labelModeFor(false)).toBe('numeric')
    expect(labelModeFor(undefined)).toBe('numeric')
  })
})

describe('chipTitle with original_markup', () => {
  // Rung 1: the source element with its attributes. This is the whole point —
  // `<a>` tells you it's a link, `<a href="example.com">` tells you which.
  it('shows the source element with its attributes', () => {
    const link: TagLabelAttrs = {
      tagType: 'g',
      tagId: '1',
      ctype: 'x-html-a',
      originalMarkup: '&lt;a href=&quot;example.com&quot;&gt;',
    }
    expect(chipTitle(link)).toBe('<a href="example.com">')
  })

  it('shows the matching close tag for the closing chip', () => {
    const close: TagLabelAttrs = {
      tagType: 'g',
      tagId: '1',
      ctype: 'x-html-a',
      originalMarkup: '&lt;a href=&quot;example.com&quot;&gt;',
      closing: true,
    }
    expect(chipTitle(close)).toBe('</a>')
  })

  it('decodes an ST4 cross-reference in full', () => {
    const metaRef: TagLabelAttrs = {
      tagType: 'g',
      tagId: '2',
      ctype: 'x-xml-meta-ref',
      originalMarkup:
        '&lt;meta-ref type=&quot;static&quot; linkid=&quot;datanode_1001_2002&quot;&gt;',
    }
    expect(chipTitle(metaRef)).toBe(
      '<meta-ref type="static" linkid="datanode_1001_2002">'
    )
  })

  it('decodes ampersands last so a query string survives', () => {
    const link: TagLabelAttrs = {
      tagType: 'g',
      tagId: '1',
      ctype: 'x-html-a',
      originalMarkup: '&lt;a href=&quot;https://x.test/?a=1&amp;b=2&quot;&gt;',
    }
    expect(chipTitle(link)).toBe('<a href="https://x.test/?a=1&b=2">')
  })

  // Rung 2: placeholders come from XLIFF, where rawXml IS the original.
  it('shows a placeholder’s full markup, which says more than its label', () => {
    const ph: TagLabelAttrs = { tagType: 'ph', tagId: '2', rawXml: '<ph id="2">{orderNumber}</ph>' }
    expect(chipLabel(ph)).toBe('{orderNumber}')
    expect(chipTitle(ph)).toBe('<ph id="2">{orderNumber}</ph>')
  })

  // Rung 3: files imported before the parsers carried the markup forward.
  it('falls back to the element form when original_markup is absent', () => {
    expect(chipTitle({ tagType: 'g', tagId: '1', ctype: 'x-html-a' })).toBe('<a>')
    expect(chipTitle({ tagType: 'g', tagId: '1', ctype: 'x-html-sup' })).toBe('<sup>')
  })

  it('leaves the visible label untouched — only the tooltip grows', () => {
    const link: TagLabelAttrs = {
      tagType: 'g',
      tagId: '1',
      ctype: 'x-html-a',
      originalMarkup: '&lt;a href=&quot;example.com&quot;&gt;',
    }
    expect(chipLabel(link)).toBe('<1>')
    expect(chipLabel(link, 'full')).toBe('<a>')
  })
})

// TMX 1.4 pairs inline tags with `i`; XLIFF 1.2 pairs with `rid` and gives each
// half a DIFFERENT `id`. Pins that both halves of a pair share one number,
// never `<1>…</2>`.
describe('paired tag numbering across TMX and XLIFF', () => {
  // The tag shape an SDLX TMX export produces.
  const tmxRich =
    'Fill the tank with H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O first.'

  function labelsOf(rich: string): string[] {
    const content = richTextToTipTapContent(rich).content![0].content ?? []
    return content
      .filter(n => n.type === 'xliffTag')
      .map(n => chipLabel(toLabelAttrs(n.attrs)))
  }

  it('gives both halves of a TMX pair the same number', () => {
    expect(labelsOf(tmxRich)).toEqual(['<1>', '</1>'])
  })

  it('pairs XLIFF bpt/ept by rid even when their ids differ', () => {
    const xliff =
      'Click <bpt id="1" rid="1">&lt;b&gt;</bpt>here<ept id="2" rid="1">&lt;/b&gt;</ept>.'
    expect(labelsOf(xliff)).toEqual(['<1>', '</1>'])
  })

  it('numbers two distinct TMX pairs independently', () => {
    const two =
      '<bpt i="1">&lt;b&gt;</bpt>a<ept i="1">&lt;/b&gt;</ept> and <bpt i="2">&lt;i&gt;</bpt>b<ept i="2">&lt;/i&gt;</ept>'
    expect(labelsOf(two)).toEqual(['<1>', '</1>', '<2>', '</2>'])
  })

  it('still pairs <g> tags that use a plain id', () => {
    expect(labelsOf('Click <g id="1" ctype="bold">here</g> now')).toEqual(['<1>', '</1>'])
  })
})

// A cross-reference chip renders the term it resolves to. A bare
// `{2}` would hide the one thing the translator needs to read the sentence —
// "…can be set to  or ." is unreadable.
describe('cross-reference chips', () => {
  const chip: TagLabelAttrs = {
    tagType: 'x',
    tagId: '2',
    ctype: 'x-xml-meta-ref',
    refKey: '/tms/tmsnode[14]/metalist/meta',
  }

  it('shows the resolved term', () => {
    expect(chipLabel(chip, 'numeric', () => 'Feucht')).toBe('{Feucht}')
    // Full-tags mode shows the term too — the element name says less here.
    expect(chipLabel(chip, 'full', () => 'Feucht')).toBe('{Feucht}')
  })

  it('falls back to the number when nothing resolves', () => {
    expect(chipLabel(chip, 'numeric', () => undefined)).toBe('{2}')
    // …and when no resolver is wired at all (TM browser, concordance).
    expect(chipLabel(chip, 'numeric')).toBe('{2}')
  })

  it('leaves ordinary chips alone', () => {
    const sup: TagLabelAttrs = { tagType: 'g', tagId: '1', ctype: 'x-html-sup' }
    expect(chipLabel(sup, 'numeric', () => 'Feucht')).toBe('<1>')
  })
})
