// A locked row's padlock, in the last column, is how a segment is unlocked:
// one click, for anyone who may edit the row. The open segment's box has no
// Lock or Unlock control; locking is a selection-bar action.

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { cleanup, render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

const { unlockTranslation } = vi.hoisted(() => ({ unlockTranslation: vi.fn() }))
vi.mock('../api', async (importOriginal) => ({ ...(await importOriginal<typeof import('../api')>()), unlockTranslation }))

import { SegmentRow } from './SegmentRow'
import type { Segment, Translation } from '../api'

const segment: Segment = {
  id: 1, file_id: 11, source_text: 'Hello world', segment_key: null, position: 0, word_count: 2,
}
const translation: Translation = {
  id: 99, segment_id: 1, target_language: 'de', status: 'approved', tm_match_percent: null,
  predict_outcome: { predict_outcome: '', valid: false }, active_text: 'Hallo Welt', edited_by: null,
  updated_at: '2026-10-01T10:00:00Z', locked: true,
}

function renderRow(t: Translation, { readOnly = false, isActive = false } = {}) {
  const onActivate = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <SegmentRow
        segment={segment}
        translation={t}
        number="1"
        jobId={7}
        fileId={11}
        projectId={null}
        targetLanguage="de"
        sourceLanguage="en"
        isActive={isActive}
        onActivate={onActivate}
        onNavigate={() => {}}
        onDeactivate={() => {}}
        dirtyRef={{ current: false }}
        insertRef={{ current: { selectAndReplace: () => {}, selectTerm: () => {} } }}
        flushDraftRef={{ current: async () => {} }}
        readOnly={readOnly}
        qaIssues={[]}
        inClearWindow={false}
      />
    </QueryClientProvider>,
  )
  return { onActivate }
}

beforeEach(() => { unlockTranslation.mockReset().mockResolvedValue(undefined) })
afterEach(cleanup)

describe('SegmentRow — the padlock unlocks', () => {
  it('unlocks the segment on click, without opening the row', async () => {
    const { onActivate } = renderRow(translation)
    await userEvent.click(screen.getByRole('button', { name: 'Unlock' }))
    await waitFor(() => expect(unlockTranslation).toHaveBeenCalledWith(11, 99))
    expect(onActivate).not.toHaveBeenCalled()
  })

  it('is only an icon for someone who may not edit the row', () => {
    renderRow(translation, { readOnly: true })
    expect(screen.getByRole('img', { name: 'Locked' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Unlock' })).not.toBeInTheDocument()
  })

  it('shows nothing on an unlocked row', () => {
    renderRow({ ...translation, locked: false })
    expect(screen.queryByRole('img', { name: 'Locked' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Unlock' })).not.toBeInTheDocument()
  })
})

describe('SegmentRow — the open segment has no lock control', () => {
  it('an unlocked segment: the box around the translation offers neither Lock nor Unlock', () => {
    renderRow({ ...translation, locked: false }, { isActive: true })
    const actions = screen.getByTestId('segment-editor-actions')
    expect(within(actions).queryByText(/lock/i)).not.toBeInTheDocument()
  })

  // Read-only, the box has no toolbar and no Clear, so no action bar either:
  // an empty one would leave a blank patch on the box's top border.
  it.each([
    ['locked', { locked: true }, {}],
    ['not the user\'s to edit', { locked: false }, { readOnly: true }],
  ])('%s: the box draws no action bar', (_name, fields, opts) => {
    renderRow({ ...translation, ...fields }, { isActive: true, ...opts })
    expect(screen.queryByTestId('segment-editor-actions')).not.toBeInTheDocument()
  })
})
