import { Button } from '@/components/ui/button'

/**
 * The "Load more" footer of a cursor-paged list (useInfiniteQuery). Renders
 * nothing when there is no next page; disabled while the next page loads.
 */
export function LoadMoreButton({
  hasNextPage, isFetchingNextPage, onLoadMore, testId, size,
}: {
  hasNextPage: boolean
  isFetchingNextPage: boolean
  onLoadMore: () => void
  testId: string
  /** `sm` for a list inside a tab; the default size on a full page. */
  size?: 'sm'
}) {
  if (!hasNextPage) return null
  return (
    <div className="flex justify-center pt-4">
      <Button variant="outline" size={size} onClick={onLoadMore} disabled={isFetchingNextPage} data-testid={testId}>
        {isFetchingNextPage ? 'Loading…' : 'Load more'}
      </Button>
    </div>
  )
}
