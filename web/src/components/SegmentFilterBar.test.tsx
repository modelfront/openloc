import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SegmentFilterBar from './SegmentFilterBar'
import { DEFAULT_FILTERS, type SegmentFilters } from '../lib/segment-filters'

afterEach(cleanup)

function renderBar(filters: Partial<SegmentFilters> = {}, statusCounts?: Record<string, number>) {
  const onChange = vi.fn()
  render(
    <SegmentFilterBar
      filters={{ ...DEFAULT_FILTERS, ...filters }}
      onChange={onChange}
      totalCount={10}
      filteredCount={10}
      statusCounts={statusCounts}
    />,
  )
  return { onChange }
}

const searchInput = () => screen.getByPlaceholderText('Search segments...')

describe('search clear button', () => {
  it('is hidden when the input is empty and appears after typing', async () => {
    renderBar()
    expect(screen.queryByTestId('segment-search-clear')).toBeNull()
    await userEvent.type(searchInput(), 'hello')
    expect(screen.getByTestId('segment-search-clear')).toBeInTheDocument()
  })

  it('stays out of the tab order, matching the native search clear', async () => {
    // The X is on every text field, so making it tabbable
    // would roughly double the tab stops in a form. Cmd+A then Delete is the
    // keyboard path. Programmatic focus + Enter still invokes it.
    const { onChange } = renderBar({ searchText: 'hello' })
    expect(screen.getByTestId('segment-search-clear')).toHaveAttribute('tabindex', '-1')

    searchInput().focus()
    await userEvent.tab()
    expect(screen.getByTestId('segment-search-clear')).not.toHaveFocus()

    screen.getByTestId('segment-search-clear').focus()
    await userEvent.keyboard('{Enter}')
    expect(searchInput()).toHaveValue('')
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, searchText: '' })
  })

  it('clears the input and only the search term, preserving other filters', async () => {
    const { onChange } = renderBar({ searchText: 'hello', statuses: ['approved'], caseSensitive: true })
    await userEvent.click(screen.getByTestId('segment-search-clear'))
    expect(searchInput()).toHaveValue('')
    expect(onChange).toHaveBeenLastCalledWith({
      ...DEFAULT_FILTERS,
      searchText: '',
      statuses: ['approved'],
      caseSensitive: true,
    })
  })
})

describe('comment filter', () => {
  const pill = () => screen.getByTestId('segment-filter-comments')

  it('toggles hasComments on and off without disturbing other filters', async () => {
    const { onChange } = renderBar({ statuses: ['approved'] })
    expect(pill()).toHaveAttribute('aria-pressed', 'false')
    await userEvent.click(pill())
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, statuses: ['approved'], hasComments: true })
  })

  it('renders pressed when the filter is on and turns it off on click', async () => {
    const { onChange } = renderBar({ hasComments: true })
    expect(pill()).toHaveAttribute('aria-pressed', 'true')
    await userEvent.click(pill())
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, hasComments: false })
  })

  it('Clear resets it along with the rest', async () => {
    const { onChange } = renderBar({ hasComments: true })
    await userEvent.click(screen.getByTestId('segment-filter-clear'))
    expect(onChange).toHaveBeenLastCalledWith(DEFAULT_FILTERS)
  })

  it('is disabled and explains itself when comment counts failed to load', async () => {
    const onChange = vi.fn()
    render(
      <SegmentFilterBar
        filters={DEFAULT_FILTERS}
        onChange={onChange}
        totalCount={10}
        filteredCount={10}
        commentCountsUnavailable
      />,
    )
    expect(pill()).toBeDisabled()
    expect(pill()).toHaveAttribute('title', expect.stringMatching(/couldn't be loaded/i))
    // A disabled pill can't be toggled into a state that would match 0 rows.
    await userEvent.click(pill())
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('QA filter when the report failed to load', () => {
  const qaPill = () => screen.getByRole('button', { name: 'Has errors' })

  it('is disabled and explains itself', async () => {
    const onChange = vi.fn()
    render(
      <SegmentFilterBar
        filters={DEFAULT_FILTERS}
        onChange={onChange}
        totalCount={10}
        filteredCount={10}
        qaReportUnavailable
      />,
    )
    expect(qaPill()).toBeDisabled()
    expect(qaPill()).toHaveAttribute('title', expect.stringMatching(/couldn't be loaded/i))
    // A disabled pill can't be toggled into a state that would match 0 rows.
    await userEvent.click(qaPill())
    expect(onChange).not.toHaveBeenCalled()
  })

  it('stays enabled when the report loaded', () => {
    renderBar()
    expect(qaPill()).toBeEnabled()
  })
})

// The pill list is a plain module-local array with no type-level tie to the
// `qaFilter` union, so nothing but a test stops an option disappearing.
describe('Ignored QA filter', () => {
  const ignoredPill = () => screen.getByRole('button', { name: 'Ignored' })

  it('selects the ignored filter', async () => {
    const { onChange } = renderBar()
    await userEvent.click(ignoredPill())
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, qaFilter: 'ignored' })
  })

  it('toggles back off when it is already the active filter', async () => {
    const { onChange } = renderBar({ qaFilter: 'ignored' })
    await userEvent.click(ignoredPill())
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, qaFilter: 'all' })
  })
})

describe('advanced options', () => {
  it('row is collapsed by default (options out of the a11y tree) and expands via the toggle', async () => {
    renderBar()
    // The row stays mounted for the open/close animation; collapsed means
    // aria-hidden + inert, so the checkboxes are unreachable by role.
    expect(screen.queryByRole('checkbox', { name: /regex/i })).toBeNull()
    await userEvent.click(screen.getByTestId('segment-search-advanced-toggle'))
    expect(screen.getByRole('checkbox', { name: /regex/i })).toBeInTheDocument()
  })

  it('checking Case sensitive commits immediately', async () => {
    const { onChange } = renderBar()
    await userEvent.click(screen.getByTestId('segment-search-advanced-toggle'))
    await userEvent.click(screen.getByRole('checkbox', { name: /case sensitive/i }))
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, caseSensitive: true })
  })

  it('Search in shows the current scope and commits a new one immediately', async () => {
    const { onChange } = renderBar({ searchTarget: false })
    await userEvent.click(screen.getByTestId('segment-search-advanced-toggle'))
    expect(screen.getByRole('radio', { name: 'Source' })).toBeChecked()
    await userEvent.click(screen.getByRole('radio', { name: 'Translation' }))
    expect(onChange).toHaveBeenLastCalledWith({ ...DEFAULT_FILTERS, searchSource: false, searchTarget: true })
  })

  it('shows an inline error for an invalid regex draft', async () => {
    renderBar({ useRegex: true })
    await userEvent.click(screen.getByTestId('segment-search-advanced-toggle'))
    await userEvent.type(searchInput(), '(')
    expect(screen.getByTestId('segment-search-regex-error')).toBeInTheDocument()
    await userEvent.type(searchInput(), 'a)')
    expect(screen.queryByTestId('segment-search-regex-error')).toBeNull()
  })
})

describe('status filters', () => {
  it('show how many segments each status holds, in brackets', () => {
    renderBar({}, { untranslated: 0, pre_translated: 4, needs_review: 28, approved: 3, auto_approved: 0 })
    expect(screen.getByRole('button', { name: 'Draft (28)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Approved (3)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Untranslated (0)' })).toBeInTheDocument()
  })
})

describe('filter groups', () => {
  it('are separated by a divider: status | origin | QA | Commented', () => {
    renderBar()
    const row = screen.getByTestId('segment-filter-comments').parentElement!
    const kinds = [...row.children].map(el =>
      el.getAttribute('data-slot') === 'vertical-divider' ? '|'
        : el.textContent?.includes('Untranslated') ? 'status'
          : el.textContent?.includes('File import') ? 'origin'
            : el.textContent?.includes('Has errors') ? 'qa'
              : el.textContent?.includes('Commented') ? 'comments' : null,
    ).filter(Boolean)
    expect(kinds).toEqual(['status', '|', 'origin', '|', 'qa', '|', 'comments'])
  })
})

describe('actions slot', () => {
  it('renders the given controls in the same row as search, after the filters', () => {
    render(
      <SegmentFilterBar
        filters={DEFAULT_FILTERS}
        onChange={vi.fn()}
        totalCount={10}
        filteredCount={10}
        actions={<button type="button">Keyboard shortcuts</button>}
      />,
    )
    const action = screen.getByRole('button', { name: 'Keyboard shortcuts' })
    const row = searchInput().closest('.flex-wrap')!
    expect(row).toContainElement(action)
    // Last in the row, so ml-auto pushes it to the right end.
    expect(row.lastElementChild).toContainElement(action)
  })
})
