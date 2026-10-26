/**
 * Inline tag chip labels.
 *
 * Chips are numbered by default — `<1>` … `</1>` for paired tags, `{1}` for
 * standalone ones. The `¶` toggle in the CAT editor toolbar switches them to
 * element names (`<sub>`, `{br}`), and hovering any chip shows the element form
 * regardless of mode.
 *
 * Everything here derives from the chip's stable attrs. Labels are computed at
 * RENDER time and never stored on the ProseMirror node: a label that changes
 * with a toggle can't double as an identity (see chipKey in missing-tags.ts),
 * and writing it into the document on every toggle would dirty the doc and
 * pollute undo history — which can trigger a save.
 */

import { HTML_PAIRED_TAGS } from './xliff-tiptap'

/** How chip labels are rendered. Driven by the editor's `¶` toggle. */
export type TagLabelMode = 'numeric' | 'full'

/** The chip attrs a label can be derived from. Mirrors the xliffTag node. */
export interface TagLabelAttrs {
  /** XLIFF element: g, bpt, ept, ph, x, it. */
  tagType: string
  /** The `id` attribute. Closing chips inherit their opening tag's id. */
  tagId?: string
  /** The `ctype` attribute — what the element originally was. */
  ctype?: string
  /** Fallback ordinal for chips whose markup carries no id. */
  tagNum?: number
  /** The original XLIFF markup, verbatim. */
  rawXml?: string
  /**
   * The source element's start tag as written in the original document —
   * `<a href="example.com">`. Set by the parsers only when the element has
   * attributes worth preserving; absent on formatting tags and on older
   * imports.
   */
  originalMarkup?: string
  /** True for the closing half of a paired tag. */
  closing?: boolean
  /**
   * For a cross-reference placeholder: the key of the segment whose
   * translation it renders. ST4's `<meta-ref>` shows the value of another
   * node, so the chip displays that node's term rather than a bare number.
   */
  refKey?: string
}

/** Resolves a cross-reference's segment key to the term to display. */
export type RefResolver = (refKey: string) => string | undefined

/**
 * ctype values that don't encode their element name, so they need a table.
 * Inverse of the Go-side maps in internal/parsers: htmlCtypeMap (html/html_segment.go),
 * inlineCtypeToHTMLMap (common/inline_tag.go) and xmlInlineCtypes (xml/xml_inline.go).
 */
const CTYPE_ELEMENT: Record<string, string> = {
  bold: 'b',
  italic: 'i',
  underlined: 'u',
  lb: 'br',
  image: 'img',
}

/**
 * Recover the original element name from a ctype.
 *
 * Prefixed ctypes carry their element name directly — `x-html-sub` → `sub`,
 * `x-xml-meta-ref` → `meta-ref` — which is why this needs no backend support
 * and works on rows already in the database. OASIS reserves the `x-` prefix
 * for custom types, so the values stay standards-correct in the data while
 * only the display changes.
 *
 * Returns '' for an empty ctype so callers can fall back to the number.
 */
export function ctypeToElementName(ctype?: string): string {
  if (!ctype) return ''
  if (CTYPE_ELEMENT[ctype]) return CTYPE_ELEMENT[ctype]

  const prefixed = /^x-(?:html|xml)-(.+)$/.exec(ctype)
  if (prefixed) return prefixed[1]

  // Unknown ctype: show it verbatim rather than inventing a name.
  return ctype
}

/**
 * Paired tags read as markup (`<1>` … `</1>`); standalone ones read as
 * placeholders (`{1}`).
 *
 * The HTML half of the list comes from the tokenizer rather than being
 * retyped here — which names produce chip PAIRS is the tokenizer's decision,
 * and a second copy would drift.
 */
const PAIRED_TAG_TYPES = new Set(['g', 'bpt', 'ept', 'it', ...HTML_PAIRED_TAGS])

function isPaired(tagType: string): boolean {
  return PAIRED_TAG_TYPES.has(tagType)
}

/** The number shown for a chip: its XLIFF id, else its ordinal, else '?'. */
function tagNumber(attrs: TagLabelAttrs): string {
  if (attrs.tagId) return attrs.tagId
  if (attrs.tagNum !== undefined) return String(attrs.tagNum)
  return '?'
}

/**
 * The element name for a chip, or '' when it can't be determined.
 *
 * bpt/ept carry the original markup as their content rather than a ctype
 * (`<bpt id="1">&lt;b&gt;</bpt>`), so fall back to digging it out of rawXml.
 */
function elementName(attrs: TagLabelAttrs): string {
  const fromCtype = ctypeToElementName(attrs.ctype)
  if (fromCtype) return fromCtype
  return elementNameFromRawXml(attrs.rawXml)
}

/** Extract `b` from `<bpt id="1">&lt;b&gt;</bpt>`. Returns '' when absent. */
function elementNameFromRawXml(rawXml?: string): string {
  const decoded = innerContentFromRawXml(rawXml, /^<(?:bpt|ept|it)\b[^>]*>([\s\S]*)<\/(?:bpt|ept|it)>$/)
  if (!decoded) return ''
  const name = /^<\/?\s*([A-Za-z_][\w.-]*)/.exec(decoded)
  return name ? name[1] : ''
}

/**
 * The visible content of a `<ph>` placeholder — `{key}` in
 * `<ph id="2">{key}</ph>`.
 *
 * Placeholders are shown by their content in BOTH modes rather than by a
 * number: a translator has to be able to tell `%s` from `{count}` to place it
 * correctly, and a bare `{2}` hides exactly the information they need.
 */
function placeholderContent(rawXml?: string): string {
  return innerContentFromRawXml(rawXml, /^<(?:ph|it)\b[^>]*>([\s\S]+)<\/(?:ph|it)>$/)
}

/** Inner content of a paired raw tag, entity-decoded. '' when it doesn't match. */
function innerContentFromRawXml(rawXml: string | undefined, re: RegExp): string {
  if (!rawXml) return ''
  const inner = re.exec(rawXml)
  if (!inner) return ''
  return decodeEntities(inner[1])
}

/**
 * Decode the XML entities the Go side escapes with `xmlEscapeAttr`.
 * `&amp;` goes LAST so an escaped `&amp;lt;` doesn't collapse into a `<`.
 */
export function decodeEntities(s: string): string {
  return s
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

/**
 * The visible text of a chip.
 *
 *   paired      <1> … </1>      full: <sub> … </sub>
 *   standalone  {1}             full: {br}
 *
 * Angle brackets for paired and braces for standalone is the convention
 * bpt/ept chips already followed; applying it to `<g>` makes the set uniform
 * and lets full-tags mode read like the real markup.
 */
export function chipLabel(
  attrs: TagLabelAttrs,
  mode: TagLabelMode = 'numeric',
  resolveRef?: RefResolver,
): string {
  const paired = isPaired(attrs.tagType)

  // A cross-reference shows the term it resolves to. `{2}` would hide the one
  // thing the translator needs in order to read the sentence, and the ladder
  // (translated term → source term → number) means it never goes blank.
  if (attrs.refKey && resolveRef) {
    const term = resolveRef(attrs.refKey)
    if (term) return `{${term}}`
  }

  // A placeholder's content outranks both the number and the element name —
  // it's the only thing that identifies which variable this is.
  if (!paired) {
    // Shown verbatim — the content usually carries its own delimiters
    // (`{key}`, `%s`, `%1$s`), so wrapping it again would read as `{{key}}`.
    const content = placeholderContent(attrs.rawXml)
    if (content) return content
  }

  const element = mode === 'full' ? elementName(attrs) : ''
  const body = element || tagNumber(attrs)

  if (!paired) return `{${body}}`
  return attrs.closing ? `</${body}>` : `<${body}>`
}

/**
 * The hover tooltip: the fullest description of the tag we can produce, in
 * both label modes, so the numeric view stays inspectable without switching.
 *
 * Ordered fallback — each rung degrades to the next:
 *
 *   1. the source element with its attributes, `<a href="example.com">`
 *   2. a placeholder's full markup, `<ph id="2">{orderNumber}</ph>`
 *   3. the element form recovered from ctype or bpt content, `<sup>`
 *   4. the numeric label — an empty tooltip would read as a rendering bug
 *
 * Rung 1 needs `original_markup`; rows without it (older imports)
 * fall to rung 3.
 */
export function chipTitle(attrs: TagLabelAttrs): string {
  if (attrs.originalMarkup) {
    const markup = decodeEntities(attrs.originalMarkup)
    if (!attrs.closing) return markup
    const name = startTagName(markup)
    return name ? `</${name}>` : markup
  }

  // Placeholders come from XLIFF sources, where rawXml already IS the original
  // markup — and it says strictly more than the label does.
  if ((attrs.tagType === 'ph' || attrs.tagType === 'it') && placeholderContent(attrs.rawXml)) {
    return attrs.rawXml ?? ''
  }

  const element = elementName(attrs)
  if (!element) return chipLabel(attrs, 'numeric')

  if (!isPaired(attrs.tagType)) return `<${element}>`
  return attrs.closing ? `</${element}>` : `<${element}>`
}

/** Pull `a` out of `<a href="…">`. '' when it isn't a start tag. */
function startTagName(startTag: string): string {
  const m = /^<\s*([A-Za-z_][\w.:-]*)/.exec(startTag)
  return m ? m[1] : ''
}

/** Convenience for the components: map the `¶` toggle to a label mode. */
export function labelModeFor(showWhitespace?: boolean): TagLabelMode {
  return showWhitespace ? 'full' : 'numeric'
}

/**
 * Narrow a loosely-typed attrs bag (ProseMirror node attrs, TipTap JSON) into
 * the shape the label helpers expect.
 *
 * Shared because all three chip renderers — the React source column, the
 * editor's node view, and the tests — read attrs from differently-typed
 * structures and would otherwise each hand-roll the same coercions.
 */
export function toLabelAttrs(attrs: Record<string, unknown> | undefined | null): TagLabelAttrs {
  const a = attrs ?? {}
  return {
    tagType: String(a.tagType ?? 'ph'),
    tagId: a.tagId ? String(a.tagId) : '',
    ctype: a.ctype ? String(a.ctype) : '',
    tagNum: typeof a.tagNum === 'number' ? a.tagNum : undefined,
    rawXml: a.rawXml ? String(a.rawXml) : '',
    originalMarkup: a.originalMarkup ? String(a.originalMarkup) : '',
    refKey: a.refKey ? String(a.refKey) : '',
    closing: Boolean(a.closing),
  }
}
