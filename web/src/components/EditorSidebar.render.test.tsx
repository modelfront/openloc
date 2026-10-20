// Render tests for EditorSidebar's MT-suggestion panel (Phase C3).
// The pure-logic comparator test lives in EditorSidebar.test.ts; this file
// covers the user-visible MT panel: live-fetch render, empty state, and
// click-to-insert wiring.
//
// Anti-tautology: the click handler is the load-bearing assertion — if the
// MT panel rendered without firing onInsertMatch on click, the user couldn't
// actually swap MT into the cell. The onInsertMatch spy assertion catches that.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PanelInsert } from '../lib/panel-insert'
import EditorSidebar from './EditorSidebar'
import type { TMMatch, TermLookupResult } from '../api'
import type { QaFinding } from '../lib/qa-findings'

const mockGetTMMatches = vi.fn()
const mockGetMTPreview = vi.fn()
const mockIgnoreQAFinding = vi.fn()
vi.mock('../api', () => ({
  getTMMatches: (...args: unknown[]) => mockGetTMMatches(...args),
  getMTPreview: (...args: unknown[]) => mockGetMTPreview(...args),
  ignoreQAFinding: (...args: unknown[]) => mockIgnoreQAFinding(...args),
  unignoreQAFinding: vi.fn(),
}))

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

// CommentPanel uses its own queries; stub it out so we don't need to mock those.
vi.mock('./CommentPanel', () => ({
  default: () => <div data-testid="comment-panel-stub" />,
}))

// SegmentText owns chip rendering and has its own tests. Stub it, but surface
// the richText it was handed — passing that down is the sidebar's half of the
// contract.
vi.mock('./SegmentRow.shared', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./SegmentRow.shared')>()),
  SegmentText: ({ text, richText }: { text: string; richText?: string | null }) => (
    <span data-rich={richText ?? ''}>{text}</span>
  ),
}))

function renderSidebar(overrides: {
  onInsertMatch?: (insert: PanelInsert) => void
  sourceText?: string
  references?: import('./EditorSidebar').SegmentReference[]
  onJumpToReference?: (refKey: string) => void
  qaIssues?: QaFinding[]
  onQARefresh?: (translationId: number, issues: QaFinding[]) => void
  terms?: TermLookupResult[]
  editText?: string
  plainText?: boolean
} = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onInsertMatch = overrides.onInsertMatch ?? vi.fn()
  const props = {
    jobId: 1,
    sourceText: overrides.sourceText ?? 'Hello world',
    editText: overrides.editText ?? '',
    terms: overrides.terms ?? ([] as TermLookupResult[]),
    references: overrides.references,
    onJumpToReference: overrides.onJumpToReference,
    onInsertMatch,
    onTermClick: vi.fn(),
    onClose: vi.fn(),
    translationId: 42,
    qaIssues: overrides.qaIssues,
    onQARefresh: overrides.onQARefresh,
    plainText: overrides.plainText,
  }
  return {
    ...render(
      <QueryClientProvider client={qc}>
        <EditorSidebar {...props} />
      </QueryClientProvider>,
    ),
    onInsertMatch,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetTMMatches.mockResolvedValue([] as TMMatch[])
})

describe('EditorSidebar MT suggestion panel', () => {
  it('renders the MT suggestion text from the live fetch', async () => {
    mockGetMTPreview.mockResolvedValue({ text: 'Bonjour le monde', engine: 'libretranslate' })
    renderSidebar()

    await waitFor(() => {
      expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument()
    })
    expect(screen.getByTestId('mt-suggestion')).toHaveTextContent('Bonjour le monde')
    expect(screen.getByText('libretranslate')).toBeInTheDocument()
  })

  it('click-to-insert fires onInsertMatch with the MT text and its insert token', async () => {
    mockGetMTPreview.mockResolvedValue({ text: 'Bonjour le monde', engine: 'libretranslate', insert_token: 'mt-token' })
    const { onInsertMatch } = renderSidebar()

    await waitFor(() => expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('mt-suggestion'))
    // The token is what lets the save record the insert as MT.
    expect(onInsertMatch).toHaveBeenCalledWith({ kind: 'mt', text: 'Bonjour le monde', insertToken: 'mt-token' })
  })

  // The pipeline stores an AI MT result as rich text with inline tag chips.
  // Rendering only the plain text showed the translator a tagless suggestion,
  // and inserting it dropped every tag from the segment.
  it('renders the MT suggestion as chips when it carries inline tags', async () => {
    mockGetMTPreview.mockResolvedValue({
      text: 'Klicken Sie Aktualisieren',
      rich_text: '<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>',
      engine: 'ai:GPT',
      stored: true,
    })
    renderSidebar()

    await waitFor(() => expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument())
    // Chip rendering itself is SegmentText's job; what the sidebar owes it is
    // the rich text. Handing over only the plain string is the bug.
    expect(screen.getByTestId('mt-suggestion').querySelector('[data-rich]'))
      .toHaveAttribute('data-rich', '<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>')
  })

  it('click-to-insert sends the rich text so the tags survive', async () => {
    mockGetMTPreview.mockResolvedValue({
      text: 'Klicken Sie Aktualisieren',
      rich_text: '<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>',
      engine: 'ai:GPT',
      stored: true,
    })
    const { onInsertMatch } = renderSidebar()

    await waitFor(() => expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('mt-suggestion'))
    expect(onInsertMatch).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'mt', text: '<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>',
    }))
  })

  it('click-to-insert sends the plain text in a plain-text file', async () => {
    mockGetMTPreview.mockResolvedValue({
      text: 'Klicken Sie Aktualisieren',
      rich_text: '<g id="1" ctype="bold">Klicken Sie Aktualisieren</g>',
      engine: 'ai:GPT',
      stored: true,
    })
    const { onInsertMatch } = renderSidebar({ plainText: true })

    await waitFor(() => expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('mt-suggestion'))
    expect(onInsertMatch).toHaveBeenCalledWith(expect.objectContaining({ kind: 'mt', text: 'Klicken Sie Aktualisieren' }))
  })

  it('shows "No MT available" when the engine call errors', async () => {
    mockGetMTPreview.mockRejectedValue(new Error('engine down'))
    renderSidebar()

    await waitFor(() => {
      expect(screen.getByText(/no mt available/i)).toBeInTheDocument()
    })
  })

  it('passes the translation id so the backend can serve stored MT', async () => {
    // Without the id, every segment click live-calls the
    // engine — a paid LLM round trip per click with AI-as-MT engines.
    mockGetMTPreview.mockResolvedValue({ text: 'Bonjour le monde', engine: 'ai:LLM', stored: true })
    renderSidebar()

    await waitFor(() => expect(mockGetMTPreview).toHaveBeenCalled())
    expect(mockGetMTPreview).toHaveBeenCalledWith(1, 'Hello world', 42)
  })

  it('skips the fetch when source text is too short', () => {
    renderSidebar({ sourceText: 'ab' })
    // The query is gated on debouncedSource.length > 2, so the API mock should
    // never be hit and the panel shows the "Select a segment" empty state.
    expect(mockGetMTPreview).not.toHaveBeenCalled()
  })
})

// Pins that clicking a TM hit hands up the match object, not flattened text,
// so the editor can re-anchor the TM entry's tags onto the current source,
// together with the insert token that records the match on the segment.
describe('TM match click hands up the match, not flattened text', () => {
  it('passes the whole match object and its insert token', async () => {
    const match = {
      id: 7,
      source_text: 'For all other gases (except CO2) the readings are dry.',
      target_text: 'Für alle anderen Gase (außer CO2) sind die Messwerte trocken.',
      target_rich_text:
        'Für alle anderen Gase (außer CO<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>) sind die Messwerte trocken.',
      score: 100,
      insert_token: 'tm-token',
    } as unknown as TMMatch
    mockGetTMMatches.mockResolvedValue([match])

    const { onInsertMatch } = renderSidebar({ sourceText: match.source_text })

    const hit = await screen.findByText(/Für alle anderen Gase/i)
    fireEvent.click(hit)

    expect(onInsertMatch).toHaveBeenCalledTimes(1)
    // The load-bearing assertion: the match object, not a pre-flattened string.
    expect(vi.mocked(onInsertMatch).mock.calls[0][0]).toMatchObject({
      kind: 'tm', match: { target_rich_text: match.target_rich_text }, insertToken: 'tm-token',
    })
  })
})

// A TM hit is truncated to one line so the panel stays scannable, which hides
// exactly the part a translator needs when segments are long. The toggle
// unwraps them; it persists so it survives moving between segments.
describe('TM matches full-text toggle', () => {
  const longMatch = {
    id: 9,
    tm_id: 1,
    tm_name: 'Master',
    source_text:
      'The influence of respiration rate and the inspiratory/expiratory time ratio on the accuracy of end-tidal gas readings was determined from measured concentration gradients.',
    target_text:
      'Der Einfluss von Atemfrequenz und dem Verhältnis Inspirationszeit zu Exspirationszeit auf die Genauigkeit der endtidalen Gaskonzentrationen wurde anhand gemessener Gradienten bestimmt.',
    score: 1,
  } as unknown as TMMatch

  beforeEach(() => {
    try { localStorage.removeItem('cat-tm-full-text') } catch { /* ignore */ }
  })

  it('truncates by default and unwraps when toggled', async () => {
    mockGetTMMatches.mockResolvedValue([longMatch])
    renderSidebar({ sourceText: longMatch.source_text })

    const target = await screen.findByText(/Der Einfluss von Atemfrequenz/i)
    expect(target.parentElement!.className).toContain('truncate')

    fireEvent.click(screen.getByTestId('tm-full-text-toggle'))

    await waitFor(() => {
      expect(screen.getByText(/Der Einfluss von Atemfrequenz/i).parentElement!.className)
        .not.toContain('truncate')
    })
    expect(screen.getByText(/Der Einfluss von Atemfrequenz/i).parentElement!.className)
      .toContain('whitespace-pre-wrap')
  })

  it('remembers the choice', async () => {
    mockGetTMMatches.mockResolvedValue([longMatch])
    renderSidebar({ sourceText: longMatch.source_text })

    await screen.findByTestId('tm-full-text-toggle')
    fireEvent.click(screen.getByTestId('tm-full-text-toggle'))

    await waitFor(() => expect(localStorage.getItem('cat-tm-full-text')).toBe('true'))
  })

  it('still inserts the match when the card is clicked while expanded', async () => {
    // The toggle must not swallow the insert action the panel exists for.
    mockGetTMMatches.mockResolvedValue([longMatch])
    const { onInsertMatch } = renderSidebar({ sourceText: longMatch.source_text })

    fireEvent.click(await screen.findByTestId('tm-full-text-toggle'))
    fireEvent.click(screen.getByText(/Der Einfluss von Atemfrequenz/i))

    expect(onInsertMatch).toHaveBeenCalledTimes(1)
  })
})

// Pins that a gated (short or empty) source shows none of the previous
// segment's TM hits or MT suggestion. Both queries are
// gated on `debouncedSource.length > 2`, so with `keepPreviousData` nothing
// would replace the previous segment's data.
//
// Anti-tautology: asserting only "No matches" appears would pass even if the
// stale card were still rendered above it. The load-bearing assertions are the
// `queryByText(...)` nulls on the previous segment's actual content.
describe('gated (too-short / no) source does not show the previous segment\'s data', () => {
  const match = {
    id: 3,
    tm_id: 1,
    tm_name: 'Default TM',
    source_text: 'Test Drive Probieren 92.00',
    target_text: 'Testfahrt Probieren 92.00',
    score: 1,
  } as unknown as TMMatch

  function renderPair() {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const props = {
      jobId: 1,
      editText: '',
      terms: [] as TermLookupResult[],
      onInsertMatch: vi.fn(),
      onTermClick: vi.fn(),
      onClose: vi.fn(),
    }
    const view = render(
      <QueryClientProvider client={qc}>
        <EditorSidebar {...props} sourceText={match.source_text} translationId={42} />
      </QueryClientProvider>,
    )
    const showSource = (sourceText: string, translationId: number | null) =>
      view.rerender(
        <QueryClientProvider client={qc}>
          <EditorSidebar {...props} sourceText={sourceText} translationId={translationId} />
        </QueryClientProvider>,
      )
    return { showSource }
  }

  it('drops stale TM + MT when switching to a 1-char segment', async () => {
    mockGetTMMatches.mockResolvedValue([match])
    mockGetMTPreview.mockResolvedValue({ text: 'MT für Segment 5', engine: 'libretranslate' })
    const { showSource } = renderPair()

    await screen.findByText('Testfahrt Probieren 92.00')
    expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument()

    // Segment 1: source "1" — below the length gate, so neither query runs.
    showSource('1', 43)

    await waitFor(() => {
      expect(screen.queryByText('Testfahrt Probieren 92.00')).toBeNull()
    })
    expect(screen.queryByText('Test Drive Probieren 92.00')).toBeNull()
    expect(screen.queryByTestId('mt-suggestion')).toBeNull()
    expect(screen.getByText(/no matches/i)).toBeInTheDocument()
  })

  it('drops stale TM when the segment is deactivated', async () => {
    mockGetTMMatches.mockResolvedValue([match])
    mockGetMTPreview.mockResolvedValue({ text: 'MT für Segment 5', engine: 'libretranslate' })
    const { showSource } = renderPair()

    await screen.findByText('Testfahrt Probieren 92.00')

    showSource('', null)

    await waitFor(() => {
      expect(screen.queryByText('Test Drive Probieren 92.00')).toBeNull()
    })
    expect(screen.queryByTestId('mt-suggestion')).toBeNull()
    expect(screen.getAllByText(/select a segment/i).length).toBeGreaterThanOrEqual(2)
  })
})

// References section: the active segment's cross-references,
// each clickable to jump to the defining row. Anti-tautology: the
// onJumpToReference spy is the load-bearing assertion — a section that renders
// but doesn't navigate would leave the linguist exactly where they started.
describe('References section', () => {
  const refs = [
    { refKey: '/tms/tmsnode[14]/metalist/meta', source: 'Wet', target: 'Feucht', segmentNumber: 126 },
    { refKey: '/tms/tmsnode[15]/metalist/meta', source: 'Dry', target: '', segmentNumber: 128 },
  ]

  it('is hidden entirely when the segment has no references', () => {
    renderSidebar({ references: [] })
    expect(screen.queryByTestId('references-section')).toBeNull()
    renderSidebar({})
    expect(screen.queryByTestId('references-section')).toBeNull()
  })

  it('lists each reference with its term, translation, and row number', () => {
    renderSidebar({ references: refs })
    const section = screen.getByTestId('references-section')
    expect(section).toHaveTextContent('{Wet}')
    expect(section).toHaveTextContent('Feucht')
    expect(section).toHaveTextContent('#126')
    // An untranslated reference says so instead of showing an empty cell.
    expect(section).toHaveTextContent('{Dry}')
    expect(section).toHaveTextContent('untranslated')
    expect(section).toHaveTextContent('#128')
  })

  it('clicking an entry fires onJumpToReference with that entry\'s key', () => {
    const onJumpToReference = vi.fn()
    renderSidebar({ references: refs, onJumpToReference })

    fireEvent.click(screen.getByText('{Dry}'))
    expect(onJumpToReference).toHaveBeenCalledTimes(1)
    expect(onJumpToReference).toHaveBeenCalledWith('/tms/tmsnode[15]/metalist/meta')
  })
})

// The section itself is covered in QAFindingsSection.test.tsx; these two pin
// the wiring — that the sidebar hands it the active segment's findings and
// forwards the refresh back up to the report cache.
describe('EditorSidebar QA findings section', () => {
  const issue: QaFinding = {
    id: 1, translation_id: 42, check_type: 'tag_missing', severity: 'error',
    message: 'Source tag(s) missing from target: </1>',
    created_at: '2026-09-07T10:00:00Z', ignored: false,
  }

  it('renders the active segment\'s findings', () => {
    renderSidebar({ qaIssues: [issue] })
    const section = screen.getByTestId('qa-findings-section')
    expect(section).toHaveTextContent('Source tag(s) missing from target')
    expect(section).toHaveTextContent('Error')
  })

  it('says "No issues" on a clean segment rather than hiding the section', () => {
    renderSidebar({ qaIssues: [] })
    expect(screen.getByTestId('qa-findings-section')).toHaveTextContent('No issues')
  })

  it('forwards the fresh list from an ignore up to onQARefresh', async () => {
    const fresh = [{ ...issue, ignored: true }]
    mockIgnoreQAFinding.mockResolvedValue(fresh)
    const onQARefresh = vi.fn()
    renderSidebar({ qaIssues: [issue], onQARefresh })

    fireEvent.click(screen.getByTestId('qa-finding-ignore-btn'))

    await waitFor(() => expect(onQARefresh).toHaveBeenCalledWith(42, fresh))
  })
})

// The panel keeps the previous segment's TM matches on screen while the new
// ones load (placeholderData: keepPreviousData), dimmed to half opacity. A
// human reads the dimming as "loading"; a click handler does not. Inserting a
// stale hit puts the PREVIOUS segment's translation into the current cell, and
// Cmd+1..5 reaches the same list without the user ever seeing the dimming.
describe('EditorSidebar stale TM matches', () => {
  const hit: TMMatch = {
    tm_id: 1, tm_name: 'Default TM', score: 1,
    source_text: 'Hello world', target_text: 'Hola mundo',
    source_rich_text: null, target_rich_text: null,
  } as unknown as TMMatch

  function renderWithSource(sourceText: string, onInsertMatch: () => void) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const props = {
      jobId: 1, editText: '', terms: [] as TermLookupResult[],
      onInsertMatch, onTermClick: vi.fn(), onClose: vi.fn(), translationId: 42,
    }
    const ui = (src: string) => (
      <QueryClientProvider client={qc}>
        <EditorSidebar {...props} sourceText={src} />
      </QueryClientProvider>
    )
    const { rerender } = render(ui(sourceText))
    return { switchTo: (src: string) => rerender(ui(src)) }
  }

  it('ignores a click on the previous segment\'s match while the new one loads', async () => {
    mockGetTMMatches.mockResolvedValue([hit])
    const onInsertMatch = vi.fn()
    const { switchTo } = renderWithSource('Hello world', onInsertMatch)
    await waitFor(() => expect(screen.getByText('Hola mundo')).toBeInTheDocument())

    // Move to another segment. Its own matches have not arrived, so the panel
    // still shows this one's.
    switchTo('An entirely different source segment')
    expect(screen.getByText('Hola mundo')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Hola mundo'))
    expect(onInsertMatch).not.toHaveBeenCalled()
  })

  it('ignores a click on the previous segment\'s MT suggestion while the new one loads', async () => {
    mockGetMTPreview.mockResolvedValueOnce({ text: 'Bonjour le monde', engine: 'e', insert_token: 'tok-old' })
    mockGetMTPreview.mockReturnValueOnce(new Promise(() => {}))
    const onInsertMatch = vi.fn()
    const { switchTo } = renderWithSource('Hello world', onInsertMatch)
    await waitFor(() => expect(screen.getByTestId('mt-suggestion')).toBeInTheDocument())

    switchTo('An entirely different source segment')
    // Before the debounce fires, and after it while the new request is in flight.
    fireEvent.click(screen.getByTestId('mt-suggestion'))
    await waitFor(() => expect(mockGetMTPreview).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByTestId('mt-suggestion'))
    expect(onInsertMatch).not.toHaveBeenCalled()
  })

  it('still inserts once the matches belong to the active segment', async () => {
    mockGetTMMatches.mockResolvedValue([hit])
    const onInsertMatch = vi.fn()
    renderWithSource('Hello world', onInsertMatch)
    await waitFor(() => expect(screen.getByText('Hola mundo')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Hola mundo'))
    expect(onInsertMatch).toHaveBeenCalledTimes(1)
  })
})

describe('Terminology term status', () => {
  const term = (id: number, status: string): TermLookupResult => ({
    id, concept_id: id, source_term: `src${id}`, target_term: `tgt${id}`, mt_translation: '',
    status, notes: '', domain: '', term_base_id: 1, term_base_name: 'TB', priority: 0,
  } as TermLookupResult)

  it('shows the status as an icon, with the label for screen readers', () => {
    renderSidebar({ terms: [term(1, 'approved'), term(2, 'forbidden')] })
    for (const [label, icon] of [['Approved', 'lucide-shield-check'], ['Forbidden', 'lucide-ban']]) {
      const text = screen.getByText(label)
      expect(text).toHaveClass('sr-only')
      const svgs = text.closest('[data-slot="chip"]')!.querySelectorAll('svg')
      expect(svgs).toHaveLength(1)
      expect(svgs[0]).toHaveClass(icon)
    }
  })

  it('shows the status label in a tooltip on hover', async () => {
    const user = userEvent.setup()
    renderSidebar({ terms: [term(1, 'approved')] })
    expect(document.querySelector('[data-slot="tooltip-content"]')).toBeNull()
    await user.hover(screen.getByText('Approved').closest('[data-slot="chip"]')!)
    await waitFor(() => {
      expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent('Approved')
    })
  })

  it('shows an applied term as a check inside its status chip, not beside it', () => {
    renderSidebar({ terms: [term(1, 'approved')], sourceText: 'the src1 here', editText: 'the tgt1 here' })
    const chip = screen.getByText('Approved · applied').closest('[data-slot="chip"]')!
    expect(chip.querySelector('svg.lucide-check')).not.toBeNull()
    expect(chip.querySelector('svg.lucide-shield-check')).toBeNull()
    expect(document.querySelectorAll('svg.lucide-check')).toHaveLength(1)
  })

  it('falls back to a visible label for a status with no icon', () => {
    renderSidebar({ terms: [term(1, 'legacy')] })
    const chip = screen.getByText('legacy')
    expect(chip).not.toHaveClass('sr-only')
    expect(chip.querySelector('svg')).toBeNull()
  })
})
