// Render tests for the Resources sidebar's customizable layout: sections
// follow the stored order, hidden ones are not mounted, and the customize
// popover writes the change back to localStorage.
//
// Anti-tautology: the assertions read DOM order and localStorage contents —
// the two things a user would notice if the layout did not stick.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import EditorSidebar from './EditorSidebar'
import type { TermLookupResult } from '../api'
import { STORAGE_KEY, type SidebarLayout } from '../lib/sidebar-sections'

const mockGetTMMatches = vi.fn()
const mockGetMTPreview = vi.fn()
const mockListSegmentHistory = vi.fn()
vi.mock('../api', () => ({
  getTMMatches: (...args: unknown[]) => mockGetTMMatches(...args),
  getMTPreview: (...args: unknown[]) => mockGetMTPreview(...args),
  listSegmentHistory: (...args: unknown[]) => mockListSegmentHistory(...args),
  ignoreQAFinding: vi.fn(),
  unignoreQAFinding: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('./CommentPanel', () => ({ default: () => <div data-testid="comment-panel-stub" /> }))
vi.mock('./SegmentRow.shared', () => ({
  SegmentText: ({ text }: { text: string }) => <span>{text}</span>,
}))

function renderSidebar() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <EditorSidebar
        jobId={1}
        sourceText="Hello world"
        editText=""
        terms={[] as TermLookupResult[]}
        onInsertMatch={vi.fn()}
        onTermClick={vi.fn()}
        onClose={vi.fn()}
        translationId={42}
      />
    </QueryClientProvider>,
  )
}

function renderedSectionIds(): string[] {
  return Array.from(screen.getByTestId('editor-sidebar').querySelectorAll<HTMLElement>('[data-sidebar-section]'))
    .map(el => el.dataset.sidebarSection!)
}

function seed(layout: SidebarLayout) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(layout))
}

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  mockGetTMMatches.mockResolvedValue([])
  mockGetMTPreview.mockResolvedValue({ text: 'Bonjour', engine: 'libretranslate' })
  mockListSegmentHistory.mockResolvedValue([])
})

describe('EditorSidebar layout', () => {
  it('fetches the MT suggestion when the MT section is visible', async () => {
    renderSidebar()
    await waitFor(() => expect(mockGetMTPreview).toHaveBeenCalledTimes(1))
  })

  it('does not fetch an MT suggestion while the MT section is hidden', async () => {
    seed({ order: ['tm', 'mt', 'terminology', 'references', 'history', 'qa', 'comments'], hidden: ['mt'] })
    renderSidebar()
    // The TM query is the control: same gate, same debounce, and it must still
    // run because Ctrl+1..5 read its results even with the section hidden.
    await waitFor(() => expect(mockGetTMMatches).toHaveBeenCalledTimes(1))
    expect(mockGetMTPreview).not.toHaveBeenCalled()
  })

  // History opens with the sidebar like every other section. The `enabled` gate
  // is keyed on that flag, so the default is also what decides whether landing
  // on a segment fetches its history at all.
  it('opens History by default and fetches the active segment', async () => {
    renderSidebar()
    const history = screen.getByTestId('segment-history-section')
    expect(within(history).getByRole('button', { name: /History/ })).toHaveAttribute('aria-expanded', 'true')
    await waitFor(() => expect(mockListSegmentHistory).toHaveBeenCalledWith(42))
  })

  it('renders sections in the default order when nothing is stored', () => {
    renderSidebar()
    // References is absent: the active segment has none, so it never mounts.
    expect(renderedSectionIds()).toEqual(['tm', 'mt', 'terminology', 'history', 'qa', 'comments'])
  })

  it('follows the stored order and skips hidden sections', () => {
    seed({ order: ['comments', 'terminology', 'tm', 'mt', 'history', 'qa', 'references'], hidden: ['mt', 'qa'] })
    renderSidebar()
    expect(renderedSectionIds()).toEqual(['comments', 'terminology', 'tm', 'history'])
    expect(screen.queryByTestId('mt-suggestion-section')).toBeNull()
    expect(screen.queryByTestId('qa-findings-section')).toBeNull()
  })

  it('unticking a section in the popover hides it and persists the layout', async () => {
    renderSidebar()
    fireEvent.click(screen.getByTestId('sidebar-customize-trigger'))
    const popover = await screen.findByTestId('sidebar-customize-popover')

    fireEvent.click(within(popover).getByTestId('sidebar-section-toggle-comments'))

    await waitFor(() => expect(screen.queryByTestId('comments-section')).toBeNull())
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY)!) as SidebarLayout
    expect(stored.hidden).toEqual(['comments'])
    expect(stored.order).toEqual(['tm', 'mt', 'terminology', 'references', 'history', 'qa', 'comments'])

    // Tick it back on: visible again, storage cleared of it.
    fireEvent.click(within(popover).getByTestId('sidebar-section-toggle-comments'))
    await waitFor(() => expect(screen.getByTestId('comments-section')).toBeInTheDocument())
    expect((JSON.parse(localStorage.getItem(STORAGE_KEY)!) as SidebarLayout).hidden).toEqual([])
  })

  it('keeps the last visible section on — its checkbox is disabled', async () => {
    seed({ order: ['tm', 'mt', 'terminology', 'references', 'history', 'qa', 'comments'], hidden: ['mt', 'terminology', 'references', 'history', 'qa', 'comments'] })
    renderSidebar()
    expect(renderedSectionIds()).toEqual(['tm'])

    fireEvent.click(screen.getByTestId('sidebar-customize-trigger'))
    const popover = await screen.findByTestId('sidebar-customize-popover')
    expect(within(popover).getByTestId('sidebar-section-toggle-tm')).toBeDisabled()
    expect(within(popover).getByTestId('sidebar-section-toggle-comments')).not.toBeDisabled()
  })

  it('lists every section in the popover in the stored order, hidden ones unticked', async () => {
    seed({ order: ['qa', 'tm', 'mt', 'terminology', 'references', 'history', 'comments'], hidden: ['references'] })
    renderSidebar()
    fireEvent.click(screen.getByTestId('sidebar-customize-trigger'))
    const popover = await screen.findByTestId('sidebar-customize-popover')

    const rows = Array.from(popover.querySelectorAll<HTMLElement>('[data-testid^="sidebar-section-row-"]'))
      .map(el => el.dataset.testid!.replace('sidebar-section-row-', ''))
    expect(rows).toEqual(['qa', 'tm', 'mt', 'terminology', 'references', 'history', 'comments'])
    expect(within(popover).getByTestId('sidebar-section-toggle-references')).not.toBeChecked()
    expect(within(popover).getByTestId('sidebar-section-toggle-qa')).toBeChecked()
  })
})
