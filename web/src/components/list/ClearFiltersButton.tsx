import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'

/**
 * The "Clear filters (N)" button at the end of a filter bar. Renders nothing
 * while no filter is active.
 */
export function ClearFiltersButton({ count, onClear, testId }: { count: number; onClear: () => void; testId: string }) {
  if (count === 0) return null
  return (
    <Button variant="ghost" size="xs" onClick={onClear} color="muted" hoverColor="red" data-testid={testId}>
      <X className="mr-1 h-3 w-3" />
      Clear filters ({count})
    </Button>
  )
}
