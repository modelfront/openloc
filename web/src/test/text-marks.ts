/** The text of every element under `root` that matches `selector`: the words
 *  a highlight or a strikethrough covers, in document order. */
export function markTexts(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map(e => e.textContent)
}
