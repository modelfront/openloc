// Shared render harness for SegmentRow tests. Each test file still declares its
// own `vi.mock` calls (they are hoisted per file); this owns the fixtures and
// the props CATEditor would pass.

import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { vi } from 'vitest'
import type { ComponentProps, MutableRefObject } from 'react'
import { SegmentRow } from '../components/SegmentRow'
import type { Segment, Translation } from '../api'
import type { QaFinding } from '../lib/qa-findings'

export type InsertHandlers = NonNullable<ComponentProps<typeof SegmentRow>['insertRef']>['current']

const rowSegment: Segment = {
  id: 1, file_id: 11, source_text: 'Hello world', segment_key: null, position: 0, word_count: 2,
}

export function rowTranslation(overrides: Partial<Translation> = {}): Translation {
  return {
    id: 99, segment_id: 1, target_language: 'de', status: 'needs_review', tm_match_percent: null,
    predict_outcome: { predict_outcome: '', valid: false }, active_text: 'Hallo Welt',
    updated_at: '2026-10-01T10:00:00Z',
    ...overrides,
  }
}

export type RowProps = {
  translation: Translation
  isActive?: boolean
  readOnly?: boolean
  qaIssues?: QaFinding[]
  isAdmin?: boolean
  inFirstStep?: boolean
  heldByName?: string | null
  plainText?: boolean
  segment?: Partial<Segment>
}

export function renderSegmentRow(initial: RowProps) {
  const insertRef: MutableRefObject<InsertHandlers> = { current: { selectAndReplace: () => {}, selectTerm: () => {} } }
  const flushDraftRef: MutableRefObject<() => Promise<void>> = { current: async () => {} }
  const dirtyRef = { current: false }
  const onNavigate = vi.fn()
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const ui = (p: RowProps) => (
    <QueryClientProvider client={queryClient}>
      <SegmentRow
        segment={{ ...rowSegment, ...p.segment }}
        translation={p.translation}
        index={1}
        jobId={7}
        fileId={11}
        projectId={null}
        targetLanguage="de"
        sourceLanguage="en"
        isActive={p.isActive ?? true}
        onActivate={() => {}}
        onNavigate={onNavigate}
        onDeactivate={() => {}}
        dirtyRef={dirtyRef}
        insertRef={insertRef}
        flushDraftRef={flushDraftRef}
        readOnly={p.readOnly}
        qaIssues={p.qaIssues ?? []}
        onQARefresh={() => {}}
        isAdmin={p.isAdmin}
        inFirstStep={p.inFirstStep}
        heldByName={p.heldByName}
        plainText={p.plainText}
      />
    </QueryClientProvider>
  )
  let current = initial
  const result = render(ui(current))
  return {
    insertRef, flushDraftRef, dirtyRef, onNavigate,
    rerender: (next: Partial<RowProps>) => {
      current = { ...current, ...next }
      result.rerender(ui(current))
    },
    unmount: result.unmount,
  }
}

export const editorText = () => screen.getByTestId('segment-editor').textContent
