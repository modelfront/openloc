import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Pagination, pageItems } from './pagination'

describe('pageItems', () => {
  it('shows every page when there are few', () => {
    expect(pageItems(1, 3)).toEqual([1, 2, 3])
  })

  it('keeps the first, last and neighbouring pages, with gaps between', () => {
    expect(pageItems(6, 12)).toEqual([1, 'gap', 5, 6, 7, 'gap', 12])
    expect(pageItems(1, 12)).toEqual([1, 2, 'gap', 12])
    expect(pageItems(12, 12)).toEqual([1, 'gap', 11, 12])
  })

  it('shows a lone page instead of a gap that would hide only it', () => {
    expect(pageItems(4, 12)).toEqual([1, 2, 3, 4, 5, 'gap', 12])
  })
})

describe('Pagination', () => {
  it('renders nothing for one page', () => {
    const { container } = render(<Pagination page={1} pageCount={1} onPageChange={() => {}} testId="p" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('marks the current page and moves by number, previous and next', async () => {
    const user = userEvent.setup()
    const onPageChange = vi.fn()
    render(<Pagination page={2} pageCount={3} onPageChange={onPageChange} testId="p" />)
    expect(screen.getByTestId('p-page-2')).toHaveAttribute('aria-current', 'page')
    await user.click(screen.getByTestId('p-page-3'))
    await user.click(screen.getByTestId('p-prev'))
    await user.click(screen.getByTestId('p-next'))
    expect(onPageChange.mock.calls).toEqual([[3], [1], [3]])
  })

  it('cannot go before the first or past the last page', () => {
    const { rerender } = render(<Pagination page={1} pageCount={3} onPageChange={() => {}} testId="p" />)
    expect(screen.getByTestId('p-prev')).toBeDisabled()
    rerender(<Pagination page={3} pageCount={3} onPageChange={() => {}} testId="p" />)
    expect(screen.getByTestId('p-next')).toBeDisabled()
  })
})
