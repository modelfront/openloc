import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

type Props = {
  /** Message shown when the list/collection is empty. */
  message: ReactNode
  /** Extra classes merged with the base muted/centered/padded classes. */
  className?: string
}

/**
 * Muted empty-state message for list pages.
 *
 * Kept intentionally minimal: no icon, no CTA button. Every list page already
 * has its "Create" button in the PageHeader, so this is just the explanatory
 * copy underneath the table/grid.
 */
export function EmptyState({ message, className }: Props) {
  return (
    <p
      className={cn(
        'text-gray-500 dark:text-gray-400 text-center py-8',
        className,
      )}
    >
      {message}
    </p>
  )
}
