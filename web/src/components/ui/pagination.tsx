import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'

/**
 * The page numbers to show: always the first and last page and the pages next
 * to the current one; 'gap' where pages are left out. A gap never hides a
 * single page: that page is shown instead.
 */
export function pageItems(page: number, pageCount: number): (number | 'gap')[] {
  const shown = new Set([1, pageCount, page - 1, page, page + 1].filter(p => p >= 1 && p <= pageCount))
  const sorted = [...shown].sort((a, b) => a - b)
  const items: (number | 'gap')[] = []
  for (const p of sorted) {
    const prev = items.at(-1)
    if (typeof prev === 'number' && p - prev === 2) items.push(prev + 1)
    else if (typeof prev === 'number' && p - prev > 2) items.push('gap')
    items.push(p)
  }
  return items
}

/**
 * Numbered pages under a list whose total is known: Previous, the page
 * numbers, Next. Nothing when everything fits on one page. For a long list
 * without a total use `LoadMoreButton` (components/list) instead. A slim row
 * that pads itself, so it sits straight under a table that fills its card.
 */
export function Pagination({ page, pageCount, onPageChange, testId }: {
  page: number
  pageCount: number
  onPageChange: (page: number) => void
  testId: string
}) {
  if (pageCount <= 1) return null
  return (
    <nav aria-label="Pagination" className="flex flex-wrap items-center justify-center gap-1 px-3 py-1" data-testid={testId}>
      <Button
        variant="ghost"
        size="icon-xs"
        disabled={page <= 1}
        onClick={() => onPageChange(page - 1)}
        aria-label="Previous page"
        data-testid={`${testId}-prev`}
      >
        <ChevronLeft />
      </Button>
      {pageItems(page, pageCount).map((item, i) =>
        item === 'gap' ? (
          <span key={`gap-${i}`} className="px-1 text-xs text-gray-400" aria-hidden>…</span>
        ) : (
          <Button
            key={item}
            variant={item === page ? 'outline' : 'ghost'}
            size="xs"
            aria-current={item === page ? 'page' : undefined}
            aria-label={`Page ${item}`}
            onClick={() => onPageChange(item)}
            data-testid={`${testId}-page-${item}`}
          >
            {item}
          </Button>
        ),
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        disabled={page >= pageCount}
        onClick={() => onPageChange(page + 1)}
        aria-label="Next page"
        data-testid={`${testId}-next`}
      >
        <ChevronRight />
      </Button>
    </nav>
  )
}
