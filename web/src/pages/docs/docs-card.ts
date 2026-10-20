import { cn } from '@/lib/utils'

/** The card look of the docs pages, shared by page cards and guided-tour cards. */
export const docsCardClassName =
  'block h-full w-full rounded-md border border-gray-200 p-4 text-left transition-colors hover:border-gray-300 hover:bg-gray-50 dark:border-gray-800 dark:hover:border-gray-700 dark:hover:bg-gray-900'

/** A page link in a docs sidebar. */
export const docsNavLinkClassName = (active: boolean) =>
  cn(
    'block truncate rounded-md px-2 py-1 text-sm text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100',
    active && 'bg-gray-100 font-medium text-gray-900 dark:bg-gray-800 dark:text-gray-100',
  )

/** A section heading in a docs sidebar. */
export const docsNavSectionClassName = (active: boolean) =>
  cn(
    'mb-1 block px-2 text-xs font-medium uppercase tracking-wide text-gray-400 hover:text-gray-700 dark:text-gray-500 dark:hover:text-gray-300',
    active && 'text-gray-900 dark:text-gray-100',
  )
