import type { Locator } from "@playwright/test";

export type Point = { x: number; y: number };

/**
 * Viewport points on a word's own first and last characters, found in the
 * text under `root`. Aim clicks and drags at these, never at the element: a
 * click lands on the element's centre, which is a different word as soon as
 * the text wraps or the word is not in the middle.
 */
export function wordEnds(root: Locator, word: string): Promise<{ from: Point; to: Point }> {
  return root.evaluate((el, w) => {
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      const node = walker.currentNode as Text;
      const i = node.data.indexOf(w);
      if (i === -1) continue;
      const charRect = (at: number) => {
        const r = document.createRange();
        r.setStart(node, at);
        r.setEnd(node, at + 1);
        return r.getBoundingClientRect();
      };
      const first = charRect(i), last = charRect(i + w.length - 1);
      // Inside the first character's left half and the last one's right half,
      // so a caret placed there lands before and after the word.
      return {
        from: { x: first.left + 1, y: first.top + first.height / 2 },
        to: { x: last.right - 1, y: last.top + last.height / 2 },
      };
    }
    throw new Error(`"${w}" not found`);
  }, word);
}
