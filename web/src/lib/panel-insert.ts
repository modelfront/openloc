import { tmInsertText, type TMInsertable } from './tm-tag-remap'

/**
 * What a click in the editor's Resources panel inserts. `insertToken` is the
 * server's signed note of where the text came from; the draft save that
 * inserts it hands it back, so the segment records the TM match (with its %)
 * or the MT suggestion, as pre-translation does.
 */
export type PanelInsert =
  | { kind: 'tm'; match: TMInsertable; insertToken?: string }
  | { kind: 'mt'; text: string; insertToken?: string }

/** The editor text for an insert. A TM match's tags are re-anchored onto the
 *  current source, or dropped in a plain-text file; an MT suggestion was made
 *  for this source, so it is inserted as is. */
export function panelInsertText(insert: PanelInsert, sourceRichText?: string | null, plainText = false): string {
  return insert.kind === 'tm' ? tmInsertText(insert.match, sourceRichText, plainText) : insert.text
}
