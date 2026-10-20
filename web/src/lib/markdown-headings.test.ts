import { describe, expect, it } from 'vitest'
import { extractHeadings, headingIdsByLine, slugifyHeading, textOfChildren } from './markdown-headings'

describe('slugifyHeading', () => {
  it('lowercases, drops punctuation and joins words with hyphens', () => {
    expect(slugifyHeading('Confirm a segment')).toBe('confirm-a-segment')
    expect(slugifyHeading('  TM & terminology sidebar (Ctrl+1)  ')).toBe('tm-terminology-sidebar-ctrl1')
    expect(slugifyHeading('`Code` in *heading*')).toBe('code-in-heading')
    expect(slugifyHeading('Übersetzen – Schritt 1')).toBe('übersetzen-schritt-1')
  })
})

describe('extractHeadings', () => {
  it('lists ## and ### headings in order, skipping # and fenced code', () => {
    const md = [
      '# Title (ignored — comes from frontmatter)',
      'Lead paragraph.',
      '## Open the editor',
      '### From the Tasks page',
      '```',
      '## not a heading',
      '```',
      '## Confirm a segment',
      '#### too deep',
    ].join('\n')
    expect(extractHeadings(md)).toEqual([
      { level: 2, text: 'Open the editor', id: 'open-the-editor', line: 3 },
      { level: 3, text: 'From the Tasks page', id: 'from-the-tasks-page', line: 4 },
      { level: 2, text: 'Confirm a segment', id: 'confirm-a-segment', line: 8 },
    ])
  })

  it('maps source lines to ids for the renderer', () => {
    expect(headingIdsByLine('## A\n\n## B')).toEqual(new Map([[1, 'a'], [3, 'b']]))
  })

  it('suffixes duplicate headings so every id is unique', () => {
    const md = '## Steps\ntext\n## Steps\n## Steps'
    expect(extractHeadings(md).map(h => h.id)).toEqual(['steps', 'steps-2', 'steps-3'])
  })
})

describe('textOfChildren', () => {
  it('flattens strings, numbers, arrays and element props', () => {
    expect(textOfChildren(['Press ', { props: { children: 'Ctrl+Enter' } }, ' now', 3])).toBe('Press Ctrl+Enter now3')
    expect(textOfChildren(null)).toBe('')
    expect(textOfChildren(true)).toBe('')
  })
})
