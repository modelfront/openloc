import { Extension } from '@tiptap/core'

/**
 * Keeps the `<x ctype="lb"/>` a hardBreak was loaded from: its `id` and
 * `original_tag` go back out unchanged on save (see `tipTapToRichText`). Tag QA
 * compares ids with the source, and export reads `original_tag` (br, hr, img).
 * A break the user types has neither and gets a fresh id.
 */
export const XliffBreakAttrs = Extension.create({
  name: 'xliffBreakAttrs',
  addGlobalAttributes() {
    return [{
      types: ['hardBreak'],
      attributes: {
        xliffId: { default: null, rendered: false },
        originalTag: { default: null, rendered: false },
      },
    }]
  },
})
