// The Resources panel's internal matches: confirmed segments of the open
// files whose source is like the open segment's. Pins what the user sees
// (percent, source file) and what a click inserts (the match's translation,
// with no insert token: it saves as the person's own edit).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { FuzzyMatch } from '../api'

const mockGetEditorFuzzy = vi.fn()
vi.mock('../api/editor', () => ({ getEditorFuzzy: (...args: unknown[]) => mockGetEditorFuzzy(...args) }))

import { EditorSidebarFuzzySection } from './EditorSidebarFuzzySection'

const match = (over: Partial<FuzzyMatch> = {}): FuzzyMatch => ({
  segment_id: 9, file_id: 2, translation_id: 90, percent: 84,
  source_text: 'Delete this item?',
  target_text: 'Dieses Element löschen?',
  updated_at: '2026-11-02T10:00:00Z',
  ...over,
})

function renderSection(onInsertMatch = vi.fn(), segmentId: number | null = 8) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <EditorSidebarFuzzySection
        editorId="7:1,2"
        segmentId={segmentId}
        sourceText="Delete these items?"
        fileNames={new Map([[2, 'screen-02.txt']])}
        onInsertMatch={onInsertMatch}
      />
    </QueryClientProvider>,
  )
  return onInsertMatch
}

beforeEach(() => { mockGetEditorFuzzy.mockReset() })

describe('EditorSidebarFuzzySection', () => {
  it('lists each match with its percent and file, and looks up the open segment in the editor', async () => {
    mockGetEditorFuzzy.mockResolvedValue([match()])
    renderSection()
    expect(await screen.findByTestId('internal-match-percent')).toHaveTextContent('84%')
    expect(screen.getByTestId('internal-match-card')).toHaveTextContent('screen-02.txt')
    expect(mockGetEditorFuzzy).toHaveBeenCalledWith('7:1,2', 8)
  })

  it('inserts the match translation without an insert token', async () => {
    mockGetEditorFuzzy.mockResolvedValue([match({ target_rich_text: 'Dieses <b>Element</b> löschen?' })])
    const onInsert = renderSection()
    await userEvent.click(await screen.findByTestId('internal-match-card'))
    expect(onInsert).toHaveBeenCalledWith({
      kind: 'tm',
      match: { target_text: 'Dieses Element löschen?', target_rich_text: 'Dieses <b>Element</b> löschen?' },
    })
  })

  it('looks nothing up while no segment is open', async () => {
    renderSection(vi.fn(), null)
    expect(await screen.findByText('Select a segment')).toBeInTheDocument()
    await waitFor(() => expect(mockGetEditorFuzzy).not.toHaveBeenCalled())
  })
})
