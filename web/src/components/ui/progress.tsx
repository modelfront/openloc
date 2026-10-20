import { Progress as ProgressPrimitive } from '@base-ui/react/progress'

import { chipDotClass, chipSurfaceClass, type ChipColor } from '@/components/ui/chip'
import { cn } from '@/lib/utils'

const SIZE_CLASS = {
  sm: 'h-1.5',
  default: 'h-2',
} as const

/**
 * A determinate progress bar: an upload, an import, a batch run, the share of
 * segments done. `value` is a percentage, clamped to 0–100; `null` is the
 * indeterminate form (a full, pulsing bar) for work whose end is unknown.
 *
 * Without a `tone` the bar is neutral (muted track, primary fill). A `tone`
 * colours it with a chip state colour — track from `chipSurfaceClass`, fill
 * from `chipDotClass` — for a bar inside a banner of that colour or one that
 * measures a state (green for segments done).
 *
 * Name it with `aria-label` or `aria-labelledby`; `aria-valuetext` replaces
 * the default "45%" reading when a count says more ("3 of 10 segments").
 * `className` is for layout (margin, width) only.
 */
export function Progress({
  value,
  size = 'default',
  tone,
  className,
  title,
  'aria-label': ariaLabel,
  'aria-labelledby': ariaLabelledBy,
  'aria-valuetext': ariaValueText,
  'data-testid': testId,
}: {
  value: number | null
  size?: keyof typeof SIZE_CLASS
  tone?: ChipColor
  className?: string
  title?: string
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-valuetext'?: string
  'data-testid'?: string
}) {
  const clamped = value === null ? null : Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0))
  return (
    <ProgressPrimitive.Root
      value={clamped}
      title={title}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      aria-valuetext={ariaValueText}
      data-testid={testId}
      data-slot="progress"
      className={cn('w-full', className)}
    >
      <ProgressPrimitive.Track
        data-slot="progress-track"
        className={cn(
          'relative w-full overflow-hidden rounded-full',
          SIZE_CLASS[size],
          tone ? chipSurfaceClass(tone) : 'bg-muted',
        )}
      >
        <ProgressPrimitive.Indicator
          data-slot="progress-indicator"
          className={cn(
            'h-full rounded-full transition-[width] duration-300',
            'data-[indeterminate]:w-full data-[indeterminate]:animate-pulse',
            tone ? chipDotClass(tone) : 'bg-primary',
          )}
        />
      </ProgressPrimitive.Track>
    </ProgressPrimitive.Root>
  )
}
