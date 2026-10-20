import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import QAFindingsSection from './QAFindingsSection'
import type { QaFinding } from '../lib/qa-findings'

const mockIgnore = vi.fn()
const mockUnignore = vi.fn()

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    ignoreQAFinding: (...args: unknown[]) => mockIgnore(...args),
    unignoreQAFinding: (...args: unknown[]) => mockUnignore(...args),
  }
})

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const finding = (over: Partial<QaFinding> = {}): QaFinding => ({
  id: 1, translation_id: 10, check_type: 'tag_missing', severity: 'error',
  message: 'Source tag(s) missing from target: </1>',
  created_at: '2026-09-07T10:00:00Z', ignored: false, ...over,
})

function setup(
  issues: QaFinding[],
  opts: { open?: boolean; onRefresh?: (t: number, i: QaFinding[]) => void; translationId?: number | null } = {},
) {
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      <QAFindingsSection
        issues={issues}
        translationId={opts.translationId === undefined ? 10 : opts.translationId}
        open={opts.open ?? true}
        onOpenChange={onOpenChange}
        onRefresh={opts.onRefresh}
      />
    </QueryClientProvider>,
  )
  return { onOpenChange }
}

beforeEach(() => {
  mockIgnore.mockReset().mockResolvedValue([])
  mockUnignore.mockReset().mockResolvedValue([])
})
afterEach(cleanup)

describe('QAFindingsSection', () => {
  it('lists every finding with its full message', () => {
    setup([
      finding(),
      finding({ id: 2, check_type: 'terminology', severity: 'warning', message: "term 'world' not translated as 'mundo'" }),
    ])
    const rows = screen.getAllByTestId('qa-finding-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('Source tag(s) missing from target')
    expect(rows[1].textContent).toContain("term 'world' not translated as 'mundo'")
  })

  it('keeps ignored findings listed so they can be un-ignored', () => {
    setup([finding({ ignored: true, ignored_by_name: 'Jane', ignored_at: '2026-09-07T10:00:00Z' })])
    const row = screen.getByTestId('qa-finding-row')
    expect(row).toHaveAttribute('data-ignored', 'true')
    expect(screen.getByTestId('qa-finding-ignored-meta').textContent).toContain('Ignored by Jane')
    expect(screen.getByTestId('qa-finding-unignore-btn')).toBeTruthy()
    expect(screen.queryByTestId('qa-finding-ignore-btn')).toBeNull()
  })

  it('ignores a finding by check_type and hands the fresh list back', async () => {
    const fresh = [finding({ ignored: true })]
    mockIgnore.mockResolvedValueOnce(fresh)
    const onRefresh = vi.fn()
    setup([finding()], { onRefresh })

    await userEvent.click(screen.getByTestId('qa-finding-ignore-btn'))

    expect(mockIgnore).toHaveBeenCalledExactlyOnceWith(10, 'tag_missing')
    await waitFor(() => expect(onRefresh).toHaveBeenCalledWith(10, fresh))
    expect(mockUnignore).not.toHaveBeenCalled()
  })

  it('un-ignores an ignored finding', async () => {
    const onRefresh = vi.fn()
    setup([finding({ ignored: true })], { onRefresh })

    await userEvent.click(screen.getByTestId('qa-finding-unignore-btn'))

    expect(mockUnignore).toHaveBeenCalledExactlyOnceWith(10, 'tag_missing')
    expect(mockIgnore).not.toHaveBeenCalled()
    await waitFor(() => expect(onRefresh).toHaveBeenCalled())
  })

  // The header number has to mean the same thing as the toolbar's `QA (N)`,
  // which counts open errors + warnings and excludes ignored findings.
  it('counts open findings apart from ignored ones', () => {
    setup([
      finding(),
      finding({ id: 2, check_type: 'punctuation', severity: 'warning' }),
      finding({ id: 3, check_type: 'terminology', ignored: true }),
    ])
    expect(screen.getByTestId('qa-findings-count').textContent).toBe('2 + 1 ignored')
  })

  it('shows only the ignored count when nothing is open', () => {
    setup([finding({ ignored: true })])
    expect(screen.getByTestId('qa-findings-count').textContent).toBe('1 ignored')
  })

  it('shows no count at all on a clean segment', () => {
    setup([])
    expect(screen.queryByTestId('qa-findings-count')).toBeNull()
    expect(screen.getByText('No issues')).toBeTruthy()
  })

  // "No issues" is a claim about a segment. With nothing selected there is no
  // segment to make it about, and the toolbar can be reading "QA (2)" at the
  // same time — so the section would contradict it. Matches the sibling
  // sections (EditorSidebar TM/MT, CommentPanel).
  it('says "Select a segment" instead of "No issues" when none is active', () => {
    setup([], { translationId: null })
    expect(screen.getByText('Select a segment')).toBeTruthy()
    expect(screen.queryByText('No issues')).toBeNull()
  })

  it('hides the list when collapsed but keeps the header clickable', async () => {
    const { onOpenChange } = setup([finding()], { open: false })
    expect(screen.queryByTestId('qa-finding-row')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: /QA findings/ }))
    expect(onOpenChange).toHaveBeenCalledWith(true)
  })
})
