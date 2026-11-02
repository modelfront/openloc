import { useCallback, type RefObject } from 'react'
import type { TermLookupResult } from '../api/terminology'
import type { SegmentEditorHandle } from '../components/SegmentEditor'
import type { MissingTag } from '../lib/missing-tags'
import { getMissingTerms, type MissingTerm } from '../lib/missing-terms'
import { getMissingPlaceables, type Placeable } from '../lib/placeables'
import { sourceInserts, type SourceInsert } from '../lib/source-inserts'

const NO_PLACEABLES: Placeable[] = []
const NO_MISSING_TERMS: MissingTerm[] = []

/** The ProseMirror content that inserts one item: a tag chip or plain text. */
function contentOf(item: SourceInsert) {
  if ('placeable' in item) return { type: 'text', text: item.placeable.text }
  if ('term' in item) return { type: 'text', text: item.term.target_term }
  const { tagType, tagId, ctype, tagNum, closing, rawXml } = item.tag
  return { type: 'xliffTag', attrs: { tagType, tagId, ctype, tagNum, closing, rawXml } }
}

/**
 * What F8 and Alt+F8 insert into the active row's target: the source's tags,
 * placeables and term targets the target lacks, in source order. F8 inserts
 * the first, Alt+F8 all of them, with a space beside each text item (tags stay
 * glued, as they wrap the text around them). `insertTag` serves the
 * missing-tags bar and `insertText` a click on a placeable in the source; a
 * placeable goes in as plain text, as the source writes it, and a term as its
 * target term.
 */
export function useSourceInserts({ sourceRich, flatSource, placeables, terms, missingTags, isActive, readOnly, target, editorRef }: {
  sourceRich: string | null | undefined
  /** The source as the reader sees it, chips flattened out. */
  flatSource: string
  placeables: readonly Placeable[]
  /** The open segment's term matches, as the Terminology panel lists them. */
  terms: readonly TermLookupResult[]
  missingTags: readonly MissingTag[]
  isActive: boolean
  readOnly: boolean
  /** The target as plain text. */
  target: string
  editorRef: RefObject<SegmentEditorHandle | null>
}) {
  const missingPlaceables = isActive && placeables.length > 0 ? getMissingPlaceables(placeables, target) : NO_PLACEABLES
  const missingTerms = isActive && terms.length > 0 ? getMissingTerms(terms, flatSource, target) : NO_MISSING_TERMS
  const items = missingTags.length > 0 || missingPlaceables.length > 0 || missingTerms.length > 0
    ? sourceInserts(sourceRich, flatSource, placeables, missingTags, missingPlaceables, missingTerms)
    : []

  const insert = useCallback((content: object | object[]) => {
    if (readOnly) return
    editorRef.current?.getEditor()?.chain().focus().insertContent(content).run()
  }, [readOnly, editorRef])

  const insertTag = useCallback((tag: MissingTag) => insert(contentOf({ tag })), [insert])
  const insertText = useCallback((text: string) => { if (text) insert({ type: 'text', text }) }, [insert])

  // Plain functions: the editor's keymap reads them through refs.
  const insertNext = () => {
    if (items.length > 0) insert(contentOf(items[0]))
  }

  const insertAll = () => {
    if (items.length === 0) return
    const content: object[] = []
    items.forEach((item, i) => {
      if (i > 0 && (!('tag' in item) || !('tag' in items[i - 1]))) content.push({ type: 'text', text: ' ' })
      content.push(contentOf(item))
    })
    insert(content)
  }

  return { insertTag, insertText, insertNext, insertAll }
}
