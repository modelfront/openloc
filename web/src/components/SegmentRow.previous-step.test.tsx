// The Status column shows one thing about the previous workflow step: that it
// auto-approved the segment. Same icon as this step's Auto-approved, in grey,
// with a tooltip that says where it came from, and only while this step has
// not marked the segment itself: a row never shows two icons.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
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
        number="1"
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
        inClearWindow={false}
        autoApprovedBeforeStep={autoApprovedBeforeStep}
      />
    </QueryClientProvider>,
  )
}

afterEach(cleanup)

describe('SegmentRow — previous step icon', () => {
  it('shows a grey Auto-approved icon and explains it on hover', async () => {
    renderRow(translation, true)
    const icon = within(screen.getByTestId('previous-step-status')).getByRole('img', { name: 'Auto-approved in the previous step.' })
    expect(icon.getAttribute('class')).toContain('text-gray-400')

    await userEvent.hover(icon)
    expect(await screen.findByText('Auto-approved in the previous step.', { selector: ':not(svg)' })).toBeInTheDocument()
  })

  it('uses the same icon as this step\'s Auto-approved, apart from the colour', () => {
    renderRow({ ...translation, status: 'auto_approved' }, false)
    const current = screen.getByRole('img', { name: 'Auto-approved' })
    const currentIcon = { kind: current.getAttribute('data-status-icon'), shape: current.innerHTML, colour: current.getAttribute('class') }
    cleanup()

    renderRow(translation, true)
    const previous = within(screen.getByTestId('previous-step-status')).getByRole('img')
    expect(previous.getAttribute('data-status-icon')).toBe(currentIcon.kind)
    expect(previous.innerHTML).toBe(currentIcon.shape)
    expect(previous.getAttribute('class')).not.toBe(currentIcon.colour)
  })

  // One icon per row: once this step marks the segment, its own icon replaces
  // the previous step's.
  it.each([
    ['Draft', { status: 'needs_review', step_edited: true }],
    ['Approved', { status: 'approved', confirmed_via: 'user' }],
    ['Auto-approved', { status: 'auto_approved' }],
  ] as const)('shows only the %s icon once this step marks the segment', (name, fields) => {
    renderRow({ ...translation, ...fields }, true)
    expect(screen.getAllByRole('img').map(i => i.getAttribute('aria-label'))).toEqual([name])
    expect(screen.queryByTestId('previous-step-status')).not.toBeInTheDocument()
  })

  it('shows nothing when the previous step did not auto-approve the segment', () => {
    renderRow(translation, false)
    expect(screen.queryByTestId('previous-step-status')).not.toBeInTheDocument()
  })
})
