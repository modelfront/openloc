/**
 * Converts XLIFF rich text (with inline tags) to TipTap JSON content.
 *
 * Supports: bpt, ept, ph, it, x, g, hi, sub tags.
 * Uses a token-based parser (not regex) to handle nested <g> tags correctly.
 *
 * Input: "Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue"
 * Output: TipTap-compatible content with xliffTag nodes + text nodes
 */

interface TipTapNode {
  type: string
  text?: string
  // xliffTag chips carry a numeric ordinal (tagNum) and a boolean direction
  // flag (closing) alongside the string attrs, so this can't be
  // Record<string, string>.
  attrs?: Record<string, string | number | boolean | undefined>
  content?: TipTapNode[]
}

// Tokenizer: splits rich text into text segments and tag tokens.
// Handles nesting correctly by matching opening/closing tags individually.
//
// Token types:
//   - { type: 'text', value: '...' }
//   - { type: 'open', name: 'g', attrs: '...', raw: '<g ...>' }
//   - { type: 'close', name: 'g', raw: '</g>' }
//   - { type: 'pair', name: 'bpt', attrs: '...', content: '...', raw: '<bpt ...>...</bpt>' }
//   - { type: 'selfclose', name: 'x', attrs: '...', raw: '<x .../>' }
export interface Token {
  type: 'text' | 'open' | 'close' | 'pair' | 'selfclose'
  name?: string
  attrs?: string
  content?: string
  value?: string
  raw: string
}

// HTML inline tags treated as atomic chips alongside XLIFF tags.
// Paired tags emit open/close chip pairs with editable text between (just
// like XLIFF `<g>`); self-closing render as a single chip.
//
// EXCLUDED from chip set on purpose: `<strong>`, `<em>`, `<sub>`, `<sup>`.
// Those are inline FORMATTING (TipTap's Bold/Italic/Subscript/Superscript
// marks via StarterKit + extensions). Translators apply them with Mod+B /
// Mod+I / Mod+. / Mod+, and expect them to round-trip as formatting marks,
// not as atomic tags. `<b>` / `<i>` / `<u>` (presentational HTML — same
// visual as bold/italic/underline but without the semantic-emphasis
// meaning) ARE chips because in a CAT context they typically come from
// upstream source markup and the translator shouldn't be retyping them.
//
// Block-level (`<p>`, `<div>`, etc.) excluded — they'd produce false
// positives in tutorial / documentation content.
const HTML_PAIRED = 'i|b|u|a|span'
/** The HTML tag names that tokenize into open/close chip pairs. Exported so
 *  the label layer can tell paired chips from standalone ones without keeping
 *  a second copy of the list. */
export const HTML_PAIRED_TAGS = HTML_PAIRED.split('|')
const HTML_SELFCLOSE = 'br|hr|img'
// XLIFF tag set — `sub` intentionally removed because it conflicts with HTML
// subscript (TipTap's Subscript mark). XLIFF `<sub>` (sub-flow inline) is rare
// and was deprecated in XLIFF 2.0; we accept losing chip rendering for it in
// exchange for not over-chipping HTML subscripts.
const XLIFF_TAGS = 'bpt|ept|ph|it|x|g|hi'
// ALL_OPEN must include HTML self-closing names too — HTML5 lets void elements
// be written without `/>` (e.g. `<br>` not just `<br/>`). The tokenizer then
// re-classifies them as `selfclose` tokens via the HTML_SELFCLOSE check below.
const ALL_OPEN = `${XLIFF_TAGS}|${HTML_PAIRED}|${HTML_SELFCLOSE}`
const ALL_CLOSE = `${XLIFF_TAGS}|${HTML_PAIRED}`
const ALL_SELFCLOSE = `${XLIFF_TAGS}|${HTML_SELFCLOSE}`
// Tags that emit open/close chip pairs (XLIFF `<g>` + all HTML paired).
// Anything else listed under ALL_OPEN that isn't here is treated as a `pair`
// token (capture the whole `<name>...</name>` block as one chip — XLIFF
// `bpt`/`ept`/`ph`/`it` semantics).
const PAIRED_AS_CHIPS_RE = new RegExp(`^(g|${HTML_PAIRED})$`)

const closeRe = new RegExp(`^</(${ALL_CLOSE})>`)
const selfCloseRe = new RegExp(`^<(${ALL_SELFCLOSE})(\\s[^>]*)?/>`)
const openRe = new RegExp(`^<(${ALL_OPEN})(\\s[^>]*)?>`)

// Exported for `spellcheck-apply.ts` and `tm-tag-remap.ts`, which need the same
// tokenization to map plain-text offsets onto a rich string and to line up tags.
// Reimplementing the tag grammar there would be a second source of truth for
// which byte ranges are markup vs. content — exactly the kind of drift that
// turns a spell-check "Change" into corruption.
export function tokenize(input: string): Token[] {
  const tokens: Token[] = []
  let i = 0

  while (i < input.length) {
    if (input[i] !== '<') {
      // Text until next '<'
      const next = input.indexOf('<', i)
      const end = next === -1 ? input.length : next
      tokens.push({ type: 'text', value: input.slice(i, end), raw: input.slice(i, end) })
      i = end
      continue
    }

    // Closing tag: </tagname>
    const closeMatch = input.slice(i).match(closeRe)
    if (closeMatch) {
      tokens.push({ type: 'close', name: closeMatch[1], raw: closeMatch[0] })
      i += closeMatch[0].length
      continue
    }

    // Self-closing tag: <x id="1" .../> OR HTML <br/>, <br />, <br>
    const selfCloseMatch = input.slice(i).match(selfCloseRe)
    if (selfCloseMatch) {
      tokens.push({ type: 'selfclose', name: selfCloseMatch[1], attrs: selfCloseMatch[2] || '', raw: selfCloseMatch[0] })
      i += selfCloseMatch[0].length
      continue
    }

    // Opening tag: <tagname ...>
    const openMatch = input.slice(i).match(openRe)
    if (openMatch) {
      const tagName = openMatch[1]

      // HTML void elements without explicit /> (e.g. `<br>`) — treat as
      // self-closing chip. The HTML5 spec doesn't require `/>` for void
      // elements, and translators copy-pasting HTML often omit it.
      if (HTML_SELFCLOSE.split('|').includes(tagName)) {
        tokens.push({ type: 'selfclose', name: tagName, attrs: openMatch[2] || '', raw: openMatch[0] })
        i += openMatch[0].length
        continue
      }

      // For <g> + HTML paired tags: emit as 'open' token, the matching
      // 'close' token comes later. Text between the two stays editable.
      if (PAIRED_AS_CHIPS_RE.test(tagName)) {
        tokens.push({ type: 'open', name: tagName, attrs: openMatch[2] || '', raw: openMatch[0] })
        i += openMatch[0].length
        continue
      }

      // For bpt, ept, ph, it: find the matching </tagname> and capture
      // as a single 'pair' chip (whole block, XLIFF-placeholder semantics).
      const closeTag = `</${tagName}>`
      const closeIdx = input.indexOf(closeTag, i + openMatch[0].length)
      if (closeIdx !== -1) {
        const content = input.slice(i + openMatch[0].length, closeIdx)
        const fullRaw = input.slice(i, closeIdx + closeTag.length)
        tokens.push({ type: 'pair', name: tagName, attrs: openMatch[2] || '', content, raw: fullRaw })
        i = closeIdx + closeTag.length
      } else {
        // No closing tag found — treat opening tag as raw text
        tokens.push({ type: 'text', value: openMatch[0], raw: openMatch[0] })
        i += openMatch[0].length
      }
      continue
    }

    // Not a recognized tag — emit '<' as text
    const nextLt = input.indexOf('<', i + 1)
    const textEnd = nextLt === -1 ? input.length : nextLt
    tokens.push({ type: 'text', value: input.slice(i, textEnd), raw: input.slice(i, textEnd) })
    i = textEnd
  }

  return tokens
}

/**
 * `ref_key` is XML-attribute-escaped by the parser on the way into
 * `source_rich_text`. Decode it back to the original segment key wherever the
 * attribute is read — RefTerms maps and segment indexes are keyed on the
 * decoded form. Lives here (not ref-terms.ts) because ingestion happens in
 * this file and ref-terms already imports from it.
 */
export function decodeRefKey(raw: string): string {
  return raw
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

function parseTagAttrs(attrStr: string): Record<string, string> {
  const attrs: Record<string, string> = {}
  const attrRe = /(\w+)="([^"]*)"/g
  let m
  while ((m = attrRe.exec(attrStr)) !== null) {
    attrs[m[1]] = m[2]
  }
  return attrs
}

/**
 * The attribute that identifies an inline tag PAIR.
 *
 * Not always `id`: XLIFF 1.2 pairs with `rid` and gives each half a distinct
 * `id`, while TMX 1.4 pairs with `i` and an SDLX export carries no `id` at all
 * (`<bpt i="1" x="1">…</bpt>…<ept i="1">`). Reading only `id` leaves both halves
 * unidentified, so they fall to the running ordinal and render `<1>…</2>`
 * instead of `<1>…</1>`.
 *
 * A pair shares ONE number — the reason the standards carry a separate pairing
 * attribute at all.
 */
function pairKey(attrs: Record<string, string>): string {
  return attrs.rid || attrs.i || attrs.id || ''
}

/**
 * Convert XLIFF rich text to TipTap paragraph content nodes.
 */
export function richTextToTipTapContent(richText: string): TipTapNode {
  const tokens = tokenize(richText)
  const content: TipTapNode[] = []
  // Stack of the identity of each still-open paired tag, so the closing chip
  // can inherit its id and ctype — never the rendered label, so missing-tags
  // can match by identity rather than display text.
  const gStack: Array<{ id: string; ctype: string; num: number; originalMarkup: string }> = []
  // Ordinals are allocated PER PAIR, not per chip: both halves of a tag must
  // show the same number. Chips with no pairing attribute at all (HTML <i>/<b>
  // in rich text) still get a fresh number each, since nothing links them —
  // the <g> stack handles those instead.
  let nextNum = 1
  const numByKey = new Map<string, number>()
  const numberFor = (key: string): number => {
    if (!key) return nextNum++
    const seen = numByKey.get(key)
    if (seen !== undefined) return seen
    const n = nextNum++
    numByKey.set(key, n)
    return n
  }

  for (const token of tokens) {
    switch (token.type) {
      case 'text':
        if (token.value) content.push({ type: 'text', text: token.value })
        break

      case 'open': {
        // <g> or HTML paired tag (<i>, <b>, etc.) → opening chip. The
        // matching close comes later. Text between stays editable.
        const attrs = parseTagAttrs(token.attrs || '')
        const tagName = token.name || 'g'
        // An HTML paired tag IS its own element name; <g> carries it in ctype.
        const ctype = attrs.ctype || (tagName === 'g' ? '' : `x-html-${tagName}`)
        const key = pairKey(attrs)
        const num = numberFor(key)
        const originalMarkup = attrs.original_markup || ''
        gStack.push({ id: key, ctype, num, originalMarkup })
        content.push({
          type: 'xliffTag',
          attrs: {
            tagType: tagName,
            tagId: key,
            ctype,
            tagNum: num,
            originalMarkup,
            rawXml: token.raw,
          },
        })
        break
      }

      case 'close': {
        // </g> or HTML paired close → closing chip, pop matching open. The
        // close tag has no attributes of its own, so it inherits the opening
        // tag's identity.
        const tagName = token.name || 'g'
        const open = gStack.pop()
        content.push({
          type: 'xliffTag',
          attrs: {
            tagType: tagName,
            tagId: open?.id ?? '',
            ctype: open?.ctype ?? (tagName === 'g' ? '' : `x-html-${tagName}`),
            tagNum: open?.num,
            // Inherited so the closing chip's tooltip can render </a>.
            originalMarkup: open?.originalMarkup ?? '',
            rawXml: token.raw,
            closing: true,
          },
        })
        break
      }

      case 'selfclose': {
        const attrs = parseTagAttrs(token.attrs || '')
        const tagName = token.name || 'x'

        // Line-break self-closing tags (XLIFF
        // <x ctype="lb"/> — produced by parsers when they see HTML <br>)
        // need to render as an actual visual line break in the editor,
        // not as an inline `{id}` chip on a single line. We emit a
        // hardBreak node only. The WhitespaceMarkers extension adds the
        // `¶` widget on hardBreaks when the toggle is on, so
        // the hidden-character indicator works the same as for a
        // user-typed Shift+Enter. On save, `tipTapToRichText` serializes
        // the hardBreak back to `<x ctype="lb" original_tag="br"/>` so
        // the round-trip preserves <br>'s identity for export.
        if (tagName === 'x' && attrs.ctype === 'lb') {
          content.push({ type: 'hardBreak' })
          break
        }

        content.push({
          type: 'xliffTag',
          attrs: {
            tagType: tagName,
            tagId: pairKey(attrs),
            ctype: attrs.ctype || '',
            tagNum: numberFor(pairKey(attrs)),
            originalMarkup: attrs.original_markup || '',
            // Decode at the single ingestion point so node attrs, label attrs,
            // data-ref-key and the resolver all carry the ORIGINAL segment key
            // (RefTerms map keys are the decoded form — collectRefKeys). The
            // round-trip is unaffected: serialization uses rawXml verbatim.
            refKey: attrs.ref_key ? decodeRefKey(attrs.ref_key) : '',
            rawXml: token.raw,
          },
        })
        break
      }

      case 'pair': {
        const attrs = parseTagAttrs(token.attrs || '')
        const tagName = token.name || 'ph'
        // bpt and ept are separate tokens; the pair key is what lets the
        // closing half reuse the opening half's number.
        const key = pairKey(attrs)
        content.push({
          type: 'xliffTag',
          attrs: {
            tagType: tagName,
            tagId: key,
            ctype: attrs.ctype || '',
            tagNum: numberFor(key),
            originalMarkup: attrs.original_markup || '',
            rawXml: token.raw,
            // ept is the closing half of a bpt/ept pair.
            closing: tagName === 'ept',
          },
        })
        break
      }
    }
  }

  return {
    type: 'doc',
    content: [{ type: 'paragraph', content: content.length > 0 ? content : undefined }],
  }
}

/**
 * Serialize TipTap document back to XLIFF rich text.
 * Walks the document and converts xliffTag nodes back to their rawXml.
 */
export function tipTapToRichText(doc: TipTapNode): string {
  if (!doc.content) return ''

  let result = ''
  let breakId = 1
  // Count any xliffTag id="N" already in the doc so emitted hardBreak
  // wrappers don't collide with existing tag ids.
  for (const paragraph of doc.content) {
    if (paragraph.type !== 'paragraph' || !paragraph.content) continue
    for (const node of paragraph.content) {
      if (node.type === 'xliffTag') {
        const id = parseInt(String(node.attrs?.tagId ?? '0'), 10)
        if (id >= breakId) breakId = id + 1
      }
    }
  }

  for (const paragraph of doc.content) {
    if (paragraph.type !== 'paragraph' || !paragraph.content) continue
    for (const node of paragraph.content) {
      if (node.type === 'text') {
        result += node.text || ''
      } else if (node.type === 'xliffTag') {
        result += node.attrs?.rawXml || ''
      } else if (node.type === 'hardBreak') {
        // Serialize hardBreak as XLIFF <x ctype="lb"/>
        // so the round-trip preserves the line break across save → reload,
        // and the export-side `XLIFFInlineToHTML` can convert it back to
        // `<br>`. User-typed Shift+Enter and parser-imported <br> both
        // round-trip via this single representation.
        result += `<x id="${breakId}" ctype="lb" original_tag="br"/>`
        breakId++
      }
    }
  }
  return result
}

/**
 * The plain text a reader sees in a paragraph's inline nodes, and the offset
 * where each node starts in it: a text node contributes its text, a hard
 * break a newline, a tag chip nothing.
 */
export function flattenInlineNodes(nodes: TipTapNode[]): { flat: string; offsets: number[] } {
  let flat = ''
  const offsets: number[] = []
  for (const node of nodes) {
    offsets.push(flat.length)
    if (node.type === 'text') flat += node.text ?? ''
    else if (node.type === 'hardBreak') flat += '\n'
  }
  return { flat, offsets }
}

/** The plain text a reader sees when rich text renders as chips. */
export function flattenRichText(richText: string): string {
  return flattenInlineNodes(richTextToTipTapContent(richText).content?.[0]?.content ?? []).flat
}

// HasXliffTags only detects XLIFF rich-text markers.
// HTML inline-tag detection is done at IMPORT time by the Go-side parsers
// (`internal/parsers/common/inline_tag.go`), which populate `source_rich_text` with
// XLIFF `<g>` / `<x>` wrappers. The display layer then routes through the
// chip parser when richText contains those.
//
// We don't auto-detect HTML in segment text bodies at render time because
// that conflates "imported with formatting" vs. "user typed/MT-output
// containing literal markup"; typed text stays text by default. The tokenizer
// below still understands HTML inline tags when they appear in proper
// rich-text fields, so chips render correctly for imports that emit them.
const XLIFF_TAG_DETECT_RE = /<\/?(bpt|ept|ph|it|x|g|hi)[\s>/]/

/**
 * Check if a rich-text string contains XLIFF inline tag wrappers. Returns
 * false for plain text even when that text contains HTML markup — chip
 * detection at the display layer is intentionally limited to richText.
 */
export function hasXliffTags(text: string): boolean {
  return XLIFF_TAG_DETECT_RE.test(text)
}
