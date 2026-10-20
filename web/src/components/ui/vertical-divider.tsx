import { cn } from '@/lib/utils'

/** A thin vertical rule between groups of controls in a row. Decorative, so
 *  hidden from screen readers; height and colour are the caller's to adjust. */
export function VerticalDivider({ className }: { className?: string }) {
  return <span aria-hidden="true" data-slot="vertical-divider" className={cn('h-4 w-px shrink-0 bg-gray-200 dark:bg-gray-700', className)} />
}
