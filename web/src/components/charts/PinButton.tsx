import { Pin } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { hoverActionClass } from '@/components/list/hover-action'
import { cn } from '@/lib/utils'

/**
 * Pins a dashboard or chart to the person's Pinned view, or unpins it. Sits
 * just before the name and is always shown: a filled pin when pinned, an
 * outline when not.
 */
export function PinButton({ pinned, onToggle, subject, loading, size = 'icon-xs', className }: {
  pinned: boolean
  onToggle: () => void
  /** What is pinned, for the accessible name: "Pin Weekly volume". */
  subject: string
  loading?: boolean
  /** `icon-xs` beside a name; `icon-sm` where a larger target fits. */
  size?: 'icon-sm' | 'icon-xs'
  className?: string
}) {
  const label = pinned ? 'Unpin' : 'Pin'
  return (
    <Button
      variant="ghost"
      size={size}
      title={label}
      aria-label={`${label} ${subject}`}
      aria-pressed={pinned}
      disabled={loading}
      data-testid="pin-button"
      data-pinned={pinned ? 'true' : undefined}
      className={cn(hoverActionClass('muted'), pinned && 'text-gray-500 dark:text-gray-400', className)}
      onClick={e => {
        e.preventDefault()
        e.stopPropagation()
        onToggle()
      }}
    >
      <Pin className={cn(size === 'icon-xs' ? 'h-3.5 w-3.5' : 'h-4 w-4', pinned && 'fill-current')} />
    </Button>
  )
}
