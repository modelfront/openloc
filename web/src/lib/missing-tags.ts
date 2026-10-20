import { richTextToTipTapContent } from './xliff-tiptap'

/**
 * A tag the target is missing from the source. The structure mirrors the
 * attrs of an `xliffTag` ProseMirror node so it can be inserted directly
 * via `editor.commands.insertContent({ type: 'xliffTag', attrs: missing[i] })`.
 */
export interface MissingTag {
  tagType: string
  tagId: string
  ctype: string
  tagNum?: number
  rawXml: string
  closing?: boolean
}

type ChipAttrs = MissingTag

// Walk a TipTap doc produced by `richTextToTipTapContent` and pull out the
// xliffTag chip attrs in document order. The function tolerates plain-text
// inputs (no chips → empty list) and inputs with display shorthand like
// `<1>` typed by hand (those don't tokenize as XLIFF tags → not counted).
function extractChips(richText: string): ChipAttrs[] {
  if (!richText) return []
  const doc = richTextToTipTapContent(richText)
  const out: ChipAttrs[] = []
  for (const para of doc.content ?? []) {
    if (para.type !== 'paragraph' || !para.content) continue
    for (const node of para.content) {
      if (node.type !== 'xliffTag' || !node.attrs) continue
      out.push({
        tagType: String(node.attrs.tagType ?? ''),
        tagId: String(node.attrs.tagId ?? ''),
        ctype: String(node.attrs.ctype ?? ''),
        tagNum: typeof node.attrs.tagNum === 'number' ? node.attrs.tagNum : undefined,
        rawXml: String(node.attrs.rawXml ?? ''),
        closing: Boolean(node.attrs.closing),
      })
    }
  }
  return out
}

// Stable identity for matching: tagType + tagId + ctype + open/close.
//
// This deliberately does NOT use the chip's visible label. Labels depend on
// the editor's full-tags toggle, so keying on them
// would let source and target rendered in different modes fail to match.
// ctype and the closing flag tell the halves apart: `</g>` chips share
// tagType='g' and inherit their opening tag's id, so the pair is distinguished
// by direction rather than by display text.
function chipKey(c: ChipAttrs): string {
  return `${c.tagType}|${c.tagId}|${c.ctype}|${c.closing ? 'close' : 'open'}`
}

/**
 * Diff source vs target as a multiset by chip identity. Returns the source
 * chips that don't have a matching counterpart in target, in source order.
 *
 * Tags are inserted from the source (F8 / Alt+F8 / click). Display shorthand (`<1>`, `{1}`, `</1>`)
 * typed by hand into target does NOT count as a tag — it has no metadata
 * and can't round-trip on export. Only real XLIFF markup
 * (`<bpt id="1">…</bpt>`, `<ph id="X"/>`, etc.) is recognized.
 */
export function getMissingTags(sourceRich: string, targetRich: string): MissingTag[] {
  const sourceChips = extractChips(sourceRich)
  if (sourceChips.length === 0) return []

  const targetChips = extractChips(targetRich)
  const targetCounts = new Map<string, number>()
  for (const c of targetChips) {
    const k = chipKey(c)
    targetCounts.set(k, (targetCounts.get(k) ?? 0) + 1)
  }

  const missing: MissingTag[] = []
  for (const c of sourceChips) {
    const k = chipKey(c)
    const remaining = targetCounts.get(k) ?? 0
    if (remaining > 0) {
      targetCounts.set(k, remaining - 1)
      continue
    }
    missing.push({
      tagType: c.tagType,
      tagId: c.tagId,
      ctype: c.ctype,
      tagNum: c.tagNum,
      rawXml: c.rawXml,
      closing: c.closing,
    })
  }
  return missing
}
