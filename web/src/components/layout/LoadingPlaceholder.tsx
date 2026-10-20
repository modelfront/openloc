import { useEffect, useState } from 'react'
import { Loader2Icon } from 'lucide-react'
import { cn } from '@/lib/utils'

// Placeholders marked `continues` form one wait across screens (auth check →
// page code → page data): one that mounts while another is showing, or right
// after one went away, shows at once instead of blinking off for its delay.
// Unmarked placeholders neither count nor skip their delay.
const HANDOFF_MS = 300
let visibleCount = 0
let lastVisibleAt = -Infinity

function continuesAWait(): boolean {
  return visibleCount > 0 || performance.now() - lastVisibleAt < HANDOFF_MS
}

type Props = {
  /** Override the default "Loading..." text. */
  message?: string
  /** Extra classes merged onto the text element (e.g. legacy `px-6` callers). */
  className?: string
  /** Text size. "base" (default) matches list pages; "sm" matches dialogs + auth pages. */
  size?: 'base' | 'sm'
  /**
   * Delay (ms) before the indicator appears. Fast page changes resolve well
   * under this, so the spinner never flashes; only genuinely slow loads show
   * it. Pass 0 to render immediately (e.g. a deliberate, known-slow state).
   */
  delayMs?: number
  /** Part of the sign-in → page code → page data wait; see the note above. */
  continues?: boolean
}

/**
 * Shared loading indicator used by every list + detail page while a query is in flight.
 *
 * A spinner + muted label, centered horizontally with vertical breathing room so
 * it reads as a deliberate loading state rather than "Loading..." glued to the
 * top-left corner. Used both as a full-page early return and nested inside a
 * content area; centering works in both.
 *
 * Shows only after `delayMs` (default 1s): the component mounts when loading
 * starts, but stays invisible until the timer fires. If the data arrives first
 * the parent unmounts it and the timer is cleared — no flash on quick loads.
 * A `continues` placeholder that takes over from another one skips the delay.
 */
export function LoadingPlaceholder({ message = 'Loading...', className, size = 'base', delayMs = 1000, continues = false }: Props) {
  const [visible, setVisible] = useState(() => delayMs <= 0 || (continues && continuesAWait()))

  useEffect(() => {
    if (visible) return
    const timer = setTimeout(() => setVisible(true), delayMs)
    return () => clearTimeout(timer)
  }, [visible, delayMs])

  useEffect(() => {
    if (!visible || !continues) return
    visibleCount++
    return () => {
      visibleCount--
      lastVisibleAt = performance.now()
    }
  }, [visible, continues])

  if (!visible) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className={cn('flex items-center justify-center gap-2.5', size === 'sm' ? 'py-8' : 'py-16')}
    >
      <Loader2Icon
        aria-hidden="true"
        className={cn('animate-spin text-gray-400 dark:text-gray-500', size === 'sm' ? 'size-4' : 'size-5')}
      />
      <p className={cn('text-gray-500 dark:text-gray-400', size === 'sm' && 'text-sm', className)}>
        {message}
      </p>
    </div>
  )
}
