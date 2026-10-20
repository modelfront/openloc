import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LoadMoreButton } from './LoadMoreButton'

describe('LoadMoreButton', () => {
  it('renders nothing without a next page', () => {
    render(<LoadMoreButton hasNextPage={false} isFetchingNextPage={false} onLoadMore={() => {}} testId="more" />)
    expect(screen.queryByTestId('more')).not.toBeInTheDocument()
  })

  it('loads the next page on click, and is disabled while it loads', () => {
    const onLoadMore = vi.fn()
    const { rerender } = render(<LoadMoreButton hasNextPage isFetchingNextPage={false} onLoadMore={onLoadMore} testId="more" />)
    fireEvent.click(screen.getByTestId('more'))
    expect(onLoadMore).toHaveBeenCalledTimes(1)

    rerender(<LoadMoreButton hasNextPage isFetchingNextPage onLoadMore={onLoadMore} testId="more" />)
    expect(screen.getByTestId('more')).toBeDisabled()
    expect(screen.getByTestId('more')).toHaveTextContent('Loading…')
  })
})
