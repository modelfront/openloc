import { cn } from '@/lib/utils'

const SIZES = {
  default: 'text-lg',
  large: 'text-2xl',
} as const

/**
 * The OpenLoc wordmark in the brand display face. Renders only the text;
 * the caller wraps it in the link or heading the spot needs.
 */
export function Wordmark({ size = 'default', className }: { size?: keyof typeof SIZES; className?: string }) {
  return (
    <span className={cn('font-display font-medium tracking-[-0.01em] text-gray-900 dark:text-gray-100', SIZES[size], className)}>
      OpenLoc
    </span>
  )
}
