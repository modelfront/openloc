// Heading ids for rendered Markdown. The renderer (components/markdown) and the
// "On this page" list (pages/docs/DocsToc) both derive ids from the same text
// through the same function, so anchors always line up without touching the DOM.

/** `line` is the 1-based source line — the key the renderer uses to look an id up. */
export type MarkdownHeading = { level: 2 | 3; text: string; id: string; line: number }

/** GitHub-style anchor: lowercase, punctuation dropped, spaces → hyphens. */
export function slugifyHeading(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[`*_~[\]()]/g, '')
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '')
}

/** Flattens React children (strings, arrays, elements) to their text. */
export function textOfChildren(children: unknown): string {
  if (children == null || typeof children === 'boolean') return ''
  if (typeof children === 'string' || typeof children === 'number') return String(children)
  if (Array.isArray(children)) return children.map(textOfChildren).join('')
  if (typeof children === 'object' && 'props' in children) {
    return textOfChildren((children as { props: { children?: unknown } }).props.children)
  }
  return ''
}

const headingLine = /^(#{2,3})\s+(.+?)\s*#*\s*$/

/**
 * Lists the `##` and `###` headings of a Markdown string in order, skipping
 * fenced code blocks. Duplicate texts get `-2`, `-3` suffixes. The renderer
 * assigns ids by looking up each heading's source line in this list, so the
 * ids are a pure function of the Markdown — stable across re-renders — and the
 * TOC and the headings can never disagree.
 */
export function extractHeadings(markdown: string): MarkdownHeading[] {
  const out: MarkdownHeading[] = []
  const seen = new Map<string, number>()
  let inFence = false
  const lines = markdown.split('\n')
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trimEnd()
    if (/^(```|~~~)/.test(line.trimStart())) {
      inFence = !inFence
      continue
    }
    if (inFence) continue
    const m = headingLine.exec(line)
    if (!m) continue
    const text = m[2].replace(/[`*_~]/g, '')
    const base = slugifyHeading(text)
    const n = (seen.get(base) ?? 0) + 1
    seen.set(base, n)
    out.push({ level: m[1].length as 2 | 3, text, id: n === 1 ? base : `${base}-${n}`, line: i + 1 })
  }
  return out
}

/** Source line → id, for the renderer. */
export function headingIdsByLine(markdown: string): Map<number, string> {
  return new Map(extractHeadings(markdown).map(h => [h.line, h.id]))
}
