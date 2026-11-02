// Track changes in the target column: an inactive row marks what changed since
// the previous workflow step, removed words struck through and added words
// highlighted. The row being edited shows plain text in its editor.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import { SegmentRow, segmentRowEqual, type SegmentRowProps } from './SegmentRow'
import type { Segment, Translation } from '../api'
import { HIGHLIGHT_TINT, SEARCH_HIT_TINT } from '../lib/text-highlight'
import { markTexts } from '../test/text-marks'

const segment: Segment = {
  id: 1, file_id: 11, source_text: 'Report received', segment_key: null, position: 0, word_count: 2,
}
const translation: Translation = {
  id: 99, segment_id: 1, target_language: 'es', status: 'needs_review', tm_match_percent: null,
  predict_outcome: { predict_outcome: '', valid: false }, active_text: 'Denuncia recibida', edited_by: null,
  updated_at: '2026-10-01T10:00:00Z',
}

function props(over: Partial<SegmentRowProps>): SegmentRowProps {
  return {
    segment,
    translation,
    number: "2",
    jobId: 7,
    fileId: 11,
    projectId: null,
    targetLanguage: 'es',
    sourceLanguage: 'en',
    isActive: false,
    onActivate: () => {},
    onNavigate: () => {},
    onDeactivate: () => {},
    dirtyRef: { current: false },
    insertRef: { current: { selectAndReplace: () => {}, selectTerm: () => {} } },
    flushDraftRef: { current: async () => {} },
    readOnly: false,
    qaIssues: [],
    inClearWindow: false,
    trackChanges: true,
    ...over,
  }
}

function renderRow(over: Partial<SegmentRowProps>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SegmentRow {...props(over)} />
    </QueryClientProvider>,
  )
}

const struck = (root: ParentNode) => Array.from(root.querySelectorAll('span.line-through')).map(e => e.textContent)
const ADDED = `span.${HIGHLIGHT_TINT}`
const HIT = `span[class~="${SEARCH_HIT_TINT}"]`

afterEach(cleanup)

describe('SegmentRow — track changes', () => {
  it('marks the words the target changed since the previous step', () => {
    const { container } = renderRow({ trackChangesFrom: 'Denuncia enviada' })
    const cell = screen.getByTestId('track-changes')
    expect(struck(cell)).toEqual(['enviada'])
    expect(markTexts(cell, ADDED)).toEqual(['recibida'])
    expect(cell.textContent).toBe('Denuncia enviadarecibida')
    expect(container.querySelector('[data-translation-cell]')).toContainElement(cell)
  })

  it('marks nothing when the text is unchanged or there is no baseline', () => {
    renderRow({ trackChangesFrom: 'Denuncia recibida' })
    expect(screen.queryByTestId('track-changes')).not.toBeInTheDocument()
    expect(screen.getByText('Denuncia recibida')).toBeInTheDocument()
    cleanup()

    renderRow({ trackChangesFrom: null })
    expect(screen.queryByTestId('track-changes')).not.toBeInTheDocument()
    expect(screen.getByText('Denuncia recibida')).toBeInTheDocument()
  })

  it('shows no marks in the row being edited', () => {
    const { container } = renderRow({ isActive: true, trackChangesFrom: 'Denuncia enviada' })
    expect(screen.queryByTestId('track-changes')).not.toBeInTheDocument()
    expect(struck(container)).toEqual([])
  })

  it('keeps the Find highlight on a marked row', () => {
    renderRow({ trackChangesFrom: 'Denuncia enviada', findHighlightTerm: 'denuncia' })
    const cell = screen.getByTestId('track-changes')
    expect(markTexts(cell, HIT)).toEqual(['Denuncia'])
    expect(struck(cell)).toEqual(['enviada'])
  })

  it('tints Find hits the same quiet way on unchanged rows, apart from added words', () => {
    const { container } = renderRow({ trackChangesFrom: 'Denuncia recibida', findHighlightTerm: 'recibida' })
    const cell = container.querySelector('[data-translation-cell]')!
    expect(markTexts(cell, HIT)).toEqual(['recibida'])
    expect(cell.querySelector(`mark, ${ADDED}`)).toBeNull()
    expect(SEARCH_HIT_TINT).not.toBe(HIGHLIGHT_TINT)
  })

  it('keeps bold and italic formatting, without marks, on a formatted row', () => {
    const { container } = renderRow({
      translation: { ...translation, active_text: 'Denuncia <strong>recibida</strong>' },
      trackChangesFrom: 'Denuncia enviada',
    })
    expect(screen.queryByTestId('track-changes')).not.toBeInTheDocument()
    expect(container.querySelector('[data-translation-cell] strong')).toHaveTextContent('recibida')
    expect(container.querySelector('[data-translation-cell]')!.textContent).not.toContain('<strong>')
  })

  it('keeps the usual Find highlight and no marks while the view is off', () => {
    const { container } = renderRow({ trackChanges: false, trackChangesFrom: null, findHighlightTerm: 'recibida' })
    const cell = container.querySelector('[data-translation-cell]')!
    expect(screen.queryByTestId('track-changes')).not.toBeInTheDocument()
    expect(markTexts(cell, `mark.${HIGHLIGHT_TINT}`)).toEqual(['recibida'])
    expect(markTexts(cell, HIT)).toEqual([])
  })

  it('repaints an inactive row when its baseline changes or the view turns on or off', () => {
    const base = props({ trackChangesFrom: null })
    expect(segmentRowEqual(base, { ...base })).toBe(true)
    expect(segmentRowEqual(base, { ...base, trackChangesFrom: 'Denuncia enviada' })).toBe(false)
    expect(segmentRowEqual(base, { ...base, trackChanges: false })).toBe(false)
  })
})
