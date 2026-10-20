// The Status column shows one thing about the previous workflow step: that it
// auto-approved the segment. Same chip as this step's Auto-approved, in grey,
// with a tooltip that says where it came from.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import { SegmentRow } from './SegmentRow'
import type { Segment, Translation } from '../api'

const segment: Segment = {
  id: 1, file_id: 11, source_text: 'Hello world', segment_key: null, position: 0, word_count: 2,
}
const translation: Translation = {
  id: 99, segment_id: 1, target_language: 'de', status: 'needs_review', tm_match_percent: null,
  predict_outcome: { predict_outcome: '', valid: false }, active_text: 'Hallo Welt', edited_by: null,
  updated_at: '2026-10-01T10:00:00Z',
}

function renderRow(t: Translation, autoApprovedBeforeStep: boolean) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <SegmentRow
        segment={segment}
        translation={t}
        index={1}
        jobId={7}
        fileId={11}
        projectId={null}
        targetLanguage="de"
        sourceLanguage="en"
        isActive={false}
        onActivate={() => {}}
        onNavigate={() => {}}
        onDeactivate={() => {}}
        dirtyRef={{ current: false }}
        insertRef={{ current: { selectAndReplace: () => {}, selectTerm: () => {} } }}
        flushDraftRef={{ current: async () => {} }}
        readOnly={false}
        qaIssues={[]}
        isAdmin
        inFirstStep={false}
        autoApprovedBeforeStep={autoApprovedBeforeStep}
      />
    </QueryClientProvider>,
  )
}

afterEach(cleanup)

describe('SegmentRow — previous step chip', () => {
  it('shows a grey Auto-approved chip, without the step name, and explains it on hover', async () => {
    renderRow(translation, true)
    const chip = screen.getByTestId('previous-step-chip')
    expect(chip).toHaveTextContent(/^Auto-approved$/)
    expect(chip.className).toContain('bg-gray-100')

    await userEvent.hover(chip)
    expect(await screen.findByText('Auto-approved in the previous step.')).toBeInTheDocument()
  })

  it('uses the same chip as this step\'s Auto-approved, apart from the colour', () => {
    renderRow({ ...translation, status: 'auto_approved' }, true)
    const [current, previous] = screen.getAllByText('Auto-approved')
    expect(previous).toBe(screen.getByTestId('previous-step-chip'))
    const withoutColour = (el: HTMLElement) => el.className.split(/\s+/)
      .filter(c => !/(^|:)(bg|text|ring)-(gray|emerald)-/.test(c)).sort()
    expect(withoutColour(previous)).toEqual(withoutColour(current))
    expect(previous.className).not.toBe(current.className)
  })

  it('shows nothing when the previous step did not auto-approve the segment', () => {
    renderRow(translation, false)
    expect(screen.queryByTestId('previous-step-chip')).not.toBeInTheDocument()
  })
})
