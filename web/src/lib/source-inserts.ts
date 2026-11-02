import { chipKey, chipOffsets, type MissingTag } from './missing-tags'
import type { MissingTerm } from './missing-terms'
import { placeableRanges, type Placeable } from './placeables'
import type { TermLookupResult } from '../api/terminology'

/** Something from the source the target still lacks: a tag, a placeable or a term's target. */
export type SourceInsert = { tag: MissingTag } | { placeable: Placeable } | { term: TermLookupResult }

/**
 * The last `n` of each key's offsets, handed out in order. The missing ones
 * are the later occurrences: getMissingTags and getMissingPlaceables match a
 * key's first occurrences against the target first.
 */
function lastOffsets(all: Map<string, number[]>, counts: Map<string, number>) {
  const next = new Map<string, number>()
  return (key: string): number => {
    const list = all.get(key) ?? []
    const i = next.get(key) ?? list.length - (counts.get(key) ?? 0)
    next.set(key, i + 1)
    return list[i] ?? Infinity
  }
}

function countBy<T>(items: readonly T[], key: (t: T) => string): Map<string, number> {
  const out = new Map<string, number>()
  for (const t of items) out.set(key(t), (out.get(key(t)) ?? 0) + 1)
  return out
}

/**
 * The missing tags, placeables and term targets in the order the source has
 * them, so F8 walks the source left to right whatever each item is.
 * `flatSource` is the source as the reader sees it (lib/xliff-tiptap
 * `flattenRichText`), where tag offsets, placeable texts and terms are found.
 * Anything whose place is not found goes last, in its list's order. A term
 * whose target is the missing placeable at the same offset is left out: the
 * placeable inserts that text.
 */
export function sourceInserts(
  sourceRich: string | null | undefined,
  flatSource: string,
  placeables: readonly Placeable[],
  missingTags: readonly MissingTag[],
  missingPlaceables: readonly Placeable[],
  missingTerms: readonly MissingTerm[] = [],
): SourceInsert[] {
  const tagAt = lastOffsets(sourceRich ? chipOffsets(sourceRich) : new Map(), countBy(missingTags, chipKey))
  const placeableOffsets = new Map<string, number[]>()
  for (const r of placeableRanges(flatSource, placeables)) {
    const text = r.insertText ?? ''
    placeableOffsets.set(text, [...(placeableOffsets.get(text) ?? []), r.start])
  }
  const placeableAt = lastOffsets(placeableOffsets, countBy(missingPlaceables, p => p.text))

  const placeableItems = missingPlaceables.map(placeable => ({ at: placeableAt(placeable.text), item: { placeable } }))
  const asPlaceable = new Set(placeableItems.map(p => `${p.at}|${p.item.placeable.text}`))
  const items: { at: number; item: SourceInsert }[] = [
    ...missingTags.map(tag => ({ at: tagAt(chipKey(tag)), item: { tag } })),
    ...placeableItems,
    ...missingTerms.filter(t => !asPlaceable.has(`${t.at}|${t.term.target_term}`)).map(({ at, term }) => ({ at, item: { term } })),
  ]
  // Stable: at the same offset a tag comes before the text after it.
  return items.sort((a, b) => (a.at === b.at ? 0 : a.at < b.at ? -1 : 1)).map(i => i.item)
}
