import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

// InlineCode — the app's inline code chip: monospace on a muted background,
// for placeholders, regex patterns and literal tokens quoted in running text.
// Pass className for size/tone variants (e.g. `py-0.5 text-[11px]`); the
// base look stays shared so every chip in the product reads the same.
export function InlineCode({ className, ...props }: ComponentProps<'code'>) {
  return <code className={cn('rounded bg-muted px-1 font-mono', className)} {...props} />
}
