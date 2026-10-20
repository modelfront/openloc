import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import SegmentHistorySection from './SegmentHistorySection'
import type { SegmentHistoryEvent } from '../api'
import { HIGHLIGHT_TINT } from '../lib/text-highlight'
import { markTexts } from '../test/text-marks'

const mockList = vi.fn()

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, listSegmentHistory: (...args: unknown[]) => mockList(...args) }
})

const event = (over: Partial<SegmentHistoryEvent> = {}): SegmentHistoryEvent => ({
  id: 1, kind: 'human', text: 'Bienvenido a casa', rich_text: null,
  created_at: new Date().toISOString(),
  tm_match_percent: null, edit_distance: null, details: {},
  created_by: 7, created_by_name: 'Maria', created_by_email: 'maria@example.com',
  mt_engine_name: null, tm_name: null, step_name: null, job_step_assignment_id: null,
  ...over,
})

function setup(opts: { open?: boolean; translationId?: number | null } = {}) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <SegmentHistorySection
        translationId={opts.translationId === undefined ? 10 : opts.translationId}
        open={opts.open ?? true}
        onOpenChange={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

const rows = () => screen.queryAllByTestId('segment-history-row')

beforeEach(() => { mockList.mockReset().mockResolvedValue([]) })
afterEach(cleanup)

describe('SegmentHistorySection', () => {
  // The section sits in a sidebar that re-mounts on every segment switch, and
  // it opens by default. Collapsing it is what a linguist who doesn't want
  // history does, so collapsing has to actually stop the per-segment request.
  it('does not fetch while collapsed', async () => {
    setup({ open: false })
    await new Promise(r => setTimeout(r, 20))
    expect(mockList).not.toHaveBeenCalled()
  })

  it('does not fetch without an active segment', async () => {
    setup({ translationId: null })
    await new Promise(r => setTimeout(r, 20))
    expect(mockList).not.toHaveBeenCalled()
    expect(screen.getByText('Select a segment')).toBeInTheDocument()
  })

  it('lists events in the order the API returns them', async () => {
    mockList.mockResolvedValue([
      event({ id: 3, kind: 'human', text: 'Bienvenido a casa, señor.' }),
      event({ id: 2, kind: 'mt', text: 'Bienvenido a casa.', mt_engine_name: 'DeepL', created_by: null, created_by_name: null }),
      event({ id: 1, kind: 'tm', text: 'Bienvenido a casa', tm_name: 'Acme DE', tm_match_percent: 82 }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(3))
    expect(rows().map(r => r.getAttribute('data-kind'))).toEqual(['human', 'mt', 'tm'])
    expect(screen.getByTestId('segment-history-count')).toHaveTextContent('3')
  })

  it('labels a TM event with its match percent and names the TM', async () => {
    mockList.mockResolvedValue([event({ kind: 'tm', tm_name: 'Acme DE', tm_match_percent: 82 })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('82%')).toBeInTheDocument()
    expect(screen.getByText('Acme DE')).toBeInTheDocument()
  })

  it('names the engine on an MT event and renders its predict outcome', async () => {
    mockList.mockResolvedValue([
      event({ kind: 'mt', mt_engine_name: 'DeepL', details: { predict_outcome: 'rejected' } }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('MT')).toBeInTheDocument()
    expect(screen.getByText('DeepL')).toBeInTheDocument()
    expect(screen.getByText(/Rejected/)).toBeInTheDocument()
  })

  // The stored edit distance is measured against the previous save, not the
  // previous step, so the panel shows what changed instead of a number.
  it('names the step on a human event and shows no edit distance', async () => {
    mockList.mockResolvedValue([event({ kind: 'human', edit_distance: 12, step_name: 'Review' })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText(/Review/)).toBeInTheDocument()
    expect(screen.queryByText(/Δ/)).not.toBeInTheDocument()
    expect(screen.queryByText(/12/)).not.toBeInTheDocument()
  })

  // kind='cleared' is the one event with no text. Rendering SegmentText against
  // null would draw an empty row that reads as a rendering bug.
  it('renders a cleared event without text', async () => {
    mockList.mockResolvedValue([event({ kind: 'cleared', text: null, edit_distance: null })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Translation removed')).toBeInTheDocument()
  })

  it('shows an empty state when the segment has no recorded changes', async () => {
    setup()
    await waitFor(() => expect(screen.getByTestId('segment-history-empty')).toBeInTheDocument())
    expect(screen.queryByTestId('segment-history-count')).not.toBeInTheDocument()
  })

  it('surfaces a load failure instead of an empty list', async () => {
    mockList.mockRejectedValue(new Error('boom'))
    setup()
    await waitFor(() => expect(screen.getByText('Could not load history.')).toBeInTheDocument())
    expect(screen.queryByTestId('segment-history-empty')).not.toBeInTheDocument()
  })
})

// Status events (approved / auto_approved / unconfirmed) carry no text, so the
// row collapses to chip + who + when.
describe('SegmentHistorySection status events', () => {
  const statusEvent = (kind: string, over: Partial<SegmentHistoryEvent> = {}) =>
    event({ kind, text: null, rich_text: null, edit_distance: null, ...over })

  it.each(['approved', 'auto_approved', 'unconfirmed'])('renders %s without a text block', async (kind) => {
    mockList.mockResolvedValue([statusEvent(kind, { text: null })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    const row = rows()[0]
    expect(row).toHaveAttribute('data-kind', kind)
    // The only text-less kind that still prints a line is `cleared`.
    expect(row).not.toHaveTextContent('Translation removed')
    expect(row).not.toHaveTextContent('Hola')
  })

  it('still explains a cleared segment', async () => {
    mockList.mockResolvedValue([statusEvent('cleared')])
    setup()
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Translation removed')).toBeInTheDocument()
  })

  // Every row names an event, so losing approval reads as something that
  // happened rather than as the state it left behind.
  it('calls the lost-approval row Unapproved', async () => {
    mockList.mockResolvedValue([statusEvent('unconfirmed', { created_by_name: 'Olivia' })])
    setup()
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Unapproved')).toBeInTheDocument()
    expect(screen.queryByText('Unconfirmed')).not.toBeInTheDocument()
  })

  it('names the person who approved', async () => {
    mockList.mockResolvedValue([statusEvent('approved', { created_by_name: 'Maria' })])
    setup()
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Approved')).toBeInTheDocument()
    expect(screen.getByText('Maria')).toBeInTheDocument()
  })
})

// Both a confirm and a step handoff write `human` rows. Every confirm says
// `Approved`, so a `human` row WITHOUT it is the handoff — the distinction
// rides on that one label being reliable, which is what these two pin.
describe('SegmentHistorySection confirmed vs delivered', () => {
  it('leaves text a step handed over without a confirm unlabelled', async () => {
    mockList.mockResolvedValue([
      event({ kind: 'human', edit_distance: 12, details: { source: 'step_handoff' } }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Bienvenido a casa')).toBeInTheDocument()
    expect(screen.queryByText(/Approved/)).not.toBeInTheDocument()
  })

  it('says a confirmed edit was approved', async () => {
    mockList.mockResolvedValue([event({ kind: 'human', edit_distance: 12, details: {} })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText(/Approved/)).toBeInTheDocument()
  })

  // Propagation copies a confirmed translation onto its siblings and approves
  // them too, so those rows carry the same outcome.
  it('says a propagated row was approved', async () => {
    mockList.mockResolvedValue([event({ kind: 'propagation', text: 'Hola', details: {} })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText(/Approved/)).toBeInTheDocument()
  })

  // Machine events approve nothing by themselves.
  it('does not claim an MT row was approved', async () => {
    mockList.mockResolvedValue([event({ kind: 'mt', mt_engine_name: 'DeepL', details: {} })])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.queryByText(/Approved/)).not.toBeInTheDocument()
  })
})

// The pipeline's approval is one chip wherever it happens: alone when nothing
// was rewritten, next to APE when the rewrite itself was approved.
describe('SegmentHistorySection pipeline approval', () => {
  it('chips an approved APE rewrite as auto-approved, and keeps its text', async () => {
    mockList.mockResolvedValue([
      event({ kind: 'ape', text: 'Le agradecemos mucho su paciencia.', mt_engine_name: 'Mock QE',
              details: { predict_outcome: 'ape_approved' } }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('APE')).toBeInTheDocument()
    expect(screen.getByText('Auto-approved')).toBeInTheDocument()
    expect(screen.getByText('Le agradecemos mucho su paciencia.')).toBeInTheDocument()
    // The chip carries the outcome, so the meta does not repeat it.
    expect(screen.queryByText(/APE approved/)).not.toBeInTheDocument()
  })

  // An `ape` row is either approved or waiting for a human, so the absent
  // chip is the whole message and the meta stays silent.
  it('leaves an APE rewrite that still needs review without a chip or an outcome label', async () => {
    mockList.mockResolvedValue([
      event({ kind: 'ape', text: 'borrador', mt_engine_name: 'Mock QE', step_name: 'QE+APE',
              details: { predict_outcome: 'ape_needs_review' } }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.queryByText('Auto-approved')).not.toBeInTheDocument()
    expect(screen.queryByText(/review/i)).not.toBeInTheDocument()
    expect(screen.getByText('borrador')).toBeInTheDocument()
  })

  it('does not repeat itself on a QP approval', async () => {
    mockList.mockResolvedValue([
      event({ kind: 'auto_approved', text: null, edit_distance: null, step_name: 'QE+APE',
              details: { predict_outcome: 'approved' } }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('Auto-approved')).toBeInTheDocument()
    expect(screen.queryByText(/QP approved/)).not.toBeInTheDocument()
  })
})

describe('SegmentHistorySection grouping', () => {
  const edit = (id: number) => event({ id, kind: 'human', text: `draft ${id}`, edit_distance: 4, job_step_assignment_id: 1 })

  it('shows one person’s saves in a step as one row with the last text, and no count', async () => {
    mockList.mockResolvedValue([edit(3), edit(2), edit(1)])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('draft 3')).toBeInTheDocument()
    expect(screen.queryByText('draft 2')).not.toBeInTheDocument()
    expect(screen.queryByText(/×/)).not.toBeInTheDocument()
    // The header counts rows on screen, not raw events.
    expect(screen.getByTestId('segment-history-count')).toHaveTextContent('1')
  })

  // Re-running pre-translation over the same TM match writes a row per run;
  // the panel shows the result once.
  it('shows identical TM re-runs once, without a count', async () => {
    const run = (id: number) => event({ id, kind: 'tm', text: 'Denuncia enviada', tm_name: 'Acme TM',
                                        tm_match_percent: 98, created_by: null, created_by_name: null })
    mockList.mockResolvedValue([run(4), run(3), run(2), run(1)])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByText('98%')).toBeInTheDocument()
    expect(screen.queryByText(/×/)).not.toBeInTheDocument()
  })

  // A confirm that changed nothing, after an edit by the same person in the
  // same step, joins that edit: one row, its text, approved once.
  it('folds an approval into the edit before it', async () => {
    mockList.mockResolvedValue([
      event({ id: 2, kind: 'approved', text: null, job_step_assignment_id: 1 }),
      event({ id: 1, kind: 'human', text: 'primera version', job_step_assignment_id: 1 }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(rows()[0]).toHaveAttribute('data-kind', 'human')
    expect(screen.getByText('primera version')).toBeInTheDocument()
    expect(screen.getAllByText(/Approved/)).toHaveLength(1)
  })

  it('does not group across different people', async () => {
    mockList.mockResolvedValue([
      event({ id: 2, created_by: 8, created_by_name: 'Olivia', job_step_assignment_id: 1 }),
      event({ id: 1, created_by: 7, created_by_name: 'Maria', job_step_assignment_id: 1 }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(2))
  })
})

// Each row is compared with the row before it: what the deleted words were,
// struck through, and what replaced them, highlighted.
describe('SegmentHistorySection track changes', () => {
  const HL = `span.${HIGHLIGHT_TINT}`
  const DEL = 'span.line-through'

  it('shows what APE changed in the TM text, and what the reviewer changed in the APE text', async () => {
    mockList.mockResolvedValue([
      event({ id: 3, kind: 'human', text: 'Denuncia recibida', step_name: 'AI review', job_step_assignment_id: 5 }),
      event({ id: 2, kind: 'ape', text: 'Denuncia enviada', mt_engine_name: 'Mock QE', created_by: null,
              step_name: 'AI review', job_step_assignment_id: 5, details: { predict_outcome: 'ape_needs_review' } }),
      event({ id: 1, kind: 'tm', text: 'Informe enviado', tm_name: 'Acme TM', tm_match_percent: 98, created_by: null }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(3))
    const [edited, ape, tm] = rows()
    expect(markTexts(edited, DEL)).toEqual(['enviada'])
    expect(markTexts(edited, HL)).toEqual(['recibida'])
    expect(markTexts(ape, DEL)).toEqual(['Informe enviado'])
    expect(markTexts(ape, HL)).toEqual(['Denuncia enviada'])
    expect(markTexts(tm, DEL)).toEqual([])
    expect(markTexts(tm, HL)).toEqual([])
  })

  it('marks nothing on a row that follows a cleared translation', async () => {
    mockList.mockResolvedValue([
      event({ id: 3, kind: 'human', text: 'Hola mundo', job_step_assignment_id: 1 }),
      event({ id: 2, kind: 'cleared', text: null, created_by: 8 }),
      event({ id: 1, kind: 'mt', text: 'Adiós', created_by: null }),
    ])
    setup()

    await waitFor(() => expect(rows()).toHaveLength(3))
    expect(markTexts(rows()[0], DEL)).toEqual([])
    expect(markTexts(rows()[0], HL)).toEqual([])
  })
})
