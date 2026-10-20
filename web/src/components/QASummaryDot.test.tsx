import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import QASummaryDot from './QASummaryDot'
import type { QaFinding } from '../lib/qa-findings'

const finding = (over: Partial<QaFinding> = {}): QaFinding => ({
  id: 1, translation_id: 10, check_type: 'tag_missing', severity: 'error',
  message: 'Source tag(s) missing from target: </1>',
  created_at: '2026-09-07T10:00:00Z', ignored: false, ...over,
})

const warning = finding({ id: 2, check_type: 'punctuation', severity: 'warning', message: 'Punctuation mismatch' })
const info = finding({ id: 3, check_type: 'double_space', severity: 'info', message: 'Double space' })

// The tooltip mounts in a portal after hover; 3 s rides out full-suite load.
const tooltip = () => screen.findByTestId('qa-issue-tooltip', undefined, { timeout: 3000 })

describe('QASummaryDot', () => {
  afterEach(cleanup)

  it('renders nothing without findings', () => {
    render(<QASummaryDot issues={[]} />)
    expect(screen.queryByTestId('qa-issue-badge')).toBeNull()
  })

  it('shows ONE dot in the worst open severity, however many findings', () => {
    render(<QASummaryDot issues={[info, warning, finding()]} />)
    const dots = screen.getAllByTestId('qa-issue-badge')
    expect(dots).toHaveLength(1)
    expect(dots[0]).toHaveAttribute('data-severity', 'error')
    expect(dots[0]).toHaveAttribute('data-count', '3')
  })

  it('an ignored error does not colour the dot — the open warning does', () => {
    render(<QASummaryDot issues={[finding({ ignored: true }), warning]} />)
    const dot = screen.getByTestId('qa-issue-badge')
    expect(dot).toHaveAttribute('data-severity', 'warning')
    expect(dot).not.toHaveAttribute('data-ignored')
  })

  it('is a hollow ring when every finding is ignored', () => {
    render(<QASummaryDot issues={[finding({ ignored: true })]} />)
    const dot = screen.getByTestId('qa-issue-badge')
    expect(dot).toHaveAttribute('data-ignored', 'true')
    expect(dot).not.toHaveAttribute('data-severity')
  })

  it('hover lists every finding, ignored ones marked, and offers no controls', async () => {
    render(<QASummaryDot issues={[finding(), warning, finding({ id: 4, check_type: 'numbers', severity: 'warning', message: 'Number mismatch', ignored: true })]} />)
    await userEvent.hover(screen.getByTestId('qa-issue-badge'))
    const list = await tooltip()
    const items = within(list).getAllByRole('listitem').map(li => li.textContent)
    expect(items).toEqual([
      'Error: Source tag(s) missing from target: </1>',
      'Warning: Punctuation mismatch',
      'Warning (ignored): Number mismatch',
    ])
    expect(within(list).queryByRole('button')).toBeNull()
  })

  it('names every finding for screen readers without hovering', () => {
    render(<QASummaryDot issues={[finding(), warning]} />)
    expect(screen.getByTestId('qa-issue-badge').getAttribute('aria-label'))
      .toBe('QA: Error: Source tag(s) missing from target: </1>; Warning: Punctuation mismatch')
  })
})
