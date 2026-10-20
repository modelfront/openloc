import { tokenize, type Token } from './xliff-tiptap'

/**
 * Re-anchor a TM match's inline tags onto the current source segment
 *
 * A TM stores tag STRUCTURE, not tag identity: the entry's markup came from
 * whatever file it was harvested from, and a TMX numbers pairs with `i` while
 * our parsers emit `<g id=… ctype=…>`. Inserting the match verbatim drops
 * foreign markup into the target, which
 *
 *   - trips the missing-tags bar and tag QA, because source and target no
 *     longer describe the same tags, and
 *   - breaks export: the XML reconstructor resolves chips by `<g id>` / `<x id>`
 *     and doesn't recognise `<bpt i="1">`, so the tag is silently lost from the
 *     delivered file.
 *
 * Every major CAT tool re-anchors instead: the ids come from the segment being
 * translated. The pipeline does this server-side for pre-translate
 * (`internal/pipeline/tm_tagtransfer.go`); this is the same rule for the
 * interactive "click a TM match" path, which the backend can't reach without an
 * import cycle (pipeline → translationmemory, not the reverse).
 *
 * ## Why UNITS rather than tags
 *
 * The two sides often model the same construct differently. An ST4
 * `<meta-ref>Wet</meta-ref>` becomes a paired `<g …>Wet</g>` in our parser, but
 * Trados exported it to TMX as a standalone `<ph x="2"/>` placeholder. Counting
 * raw tags makes those look incompatible (6 vs 4) and throws away a perfectly
 * good mapping; counting logical units sees 3 vs 3 and lines them up. Text
 * always comes from the TM (it's the translation); tags always come from the
 * source (it owns their identity).
 */

/** Everything the remap needs from a TM match. */
export interface TMInsertable {
  target_text: string
  target_rich_text?: string | null
}

/** One logical inline tag: a pair, or a standalone placeholder (close === ''). */
interface TagUnit {
  open: string
  close: string
}

/** A tag token's place in the unit sequence. */
interface TagRole {
  unit: number
  role: 'open' | 'close' | 'standalone'
}

/**
 * The text to insert when a translator clicks a TM match.
 *
 * Returns rich text carrying the SOURCE's tags when both sides have the same
 * number of logical tag units, and the plain target otherwise — a genuine
 * structure mismatch means we can't know which tag is which, so the translator
 * places them with the missing-tags bar. A plain-text file always gets the
 * plain target.
 */
export function tmInsertText(match: TMInsertable, sourceRichText?: string | null, plainText = false): string {
  const targetRich = match.target_rich_text ?? ''
  if (!targetRich || plainText) return match.target_text

  const sourceUnits = tagUnits(sourceRichText ?? '')
  // Untagged source: never import the entry's tags into a segment that has
  // none — they'd have nothing to correspond to.
  if (sourceUnits.length === 0) return match.target_text

  return remapTagsToSource(targetRich, sourceUnits) || match.target_text
}

/**
 * Rebuild targetRich with its tags replaced, unit by unit, by the source's.
 * Returns "" when the unit counts differ, signalling a fallback to plain text.
 */
export function remapTagsToSource(targetRich: string, sourceUnits: TagUnit[]): string {
  const tokens = tokenize(targetRich)
  const roles = tagRoles(tokens)
  const targetUnitCount = roles.filter(r => r.role !== 'close').length

  if (targetUnitCount !== sourceUnits.length) return ''

  let out = ''
  let tagIdx = 0
  for (const t of tokens) {
    if (t.type === 'text') {
      out += t.value ?? ''
      continue
    }
    const { unit, role } = roles[tagIdx++]
    const src = sourceUnits[unit]
    if (role === 'open') out += src.open
    else if (role === 'close') out += src.close
    // A standalone placeholder maps onto the whole source unit. When the
    // source side is a pair this emits an empty one — tags present, no text —
    // which is exactly what the TM says belongs there.
    else out += src.open + src.close
  }
  return out
}

/** The logical tag units of a rich-text string, in order. */
export function tagUnits(richText: string): TagUnit[] {
  if (!richText) return []
  const tokens = tokenize(richText)
  const roles = tagRoles(tokens)

  const units: TagUnit[] = []
  let tagIdx = 0
  for (const t of tokens) {
    if (t.type === 'text') continue
    const { unit, role } = roles[tagIdx++]
    if (role === 'close') {
      if (units[unit]) units[unit].close = t.raw
      continue
    }
    units[unit] = { open: t.raw, close: '' }
  }
  return units
}

/**
 * Assign every tag token to a logical unit.
 *
 * `<g>`/`</g>` nest, so they pair on a stack. `bpt`/`ept` don't nest reliably
 * across tools and carry an explicit pairing attribute, so they pair on that
 * (`i`, or `rid`/`id` as XLIFF writes it). Everything else — `ph`, `x`, `it` —
 * stands alone.
 */
function tagRoles(tokens: Token[]): TagRole[] {
  const roles: TagRole[] = []
  const gStack: number[] = []
  const openByKey = new Map<string, number>()
  let nextUnit = 0

  for (const t of tokens) {
    if (t.type === 'text') continue
    const name = t.name ?? ''

    if (t.type === 'open') {
      const unit = nextUnit++
      gStack.push(unit)
      roles.push({ unit, role: 'open' })
      continue
    }
    if (t.type === 'close') {
      const unit = gStack.pop() ?? nextUnit++
      roles.push({ unit, role: 'close' })
      continue
    }
    if (name === 'bpt') {
      const unit = nextUnit++
      openByKey.set(pairAttr(t.attrs ?? ''), unit)
      roles.push({ unit, role: 'open' })
      continue
    }
    if (name === 'ept') {
      const key = pairAttr(t.attrs ?? '')
      const unit = openByKey.get(key)
      roles.push(unit === undefined ? { unit: nextUnit++, role: 'standalone' } : { unit, role: 'close' })
      continue
    }
    roles.push({ unit: nextUnit++, role: 'standalone' })
  }
  return roles
}

/** The pairing attribute of a bpt/ept: TMX writes `i`, XLIFF `rid` (or `id`). */
function pairAttr(attrText: string): string {
  for (const name of ['i', 'rid', 'id']) {
    const m = new RegExp(`\\b${name}="([^"]*)"`).exec(attrText)
    if (m) return `${name}:${m[1]}`
  }
  return ''
}
