import { cn } from '@/lib/utils'
import { chipTextClass } from './chip'

/**
 * The inline state of a change that saves on its own: "Saving..." while the
 * request runs, then "Saved ✓" while `saved` holds (useSavedFlash). Nothing
 * otherwise.
 */
export function SaveStatus({ saving, saved, testId }: { saving: boolean; saved: boolean; testId?: string }) {
  if (saving) {
    return <span role="status" className="text-xs text-muted-foreground animate-pulse" data-testid={testId}>Saving...</span>
  }
  if (saved) {
    return <span role="status" className={cn('text-xs', chipTextClass('green'))} data-testid={testId}>Saved ✓</span>
  }
  return null
}
