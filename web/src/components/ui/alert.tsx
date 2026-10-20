import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { chipSurfaceClass, chipTextClass, type ChipColor } from '@/components/ui/chip'

// Alert — the inline bordered box for a warning or an error inside a page,
// dialog or form: an auth error above the sign-in fields, a section that
// could not load, a save conflict. Not for a full-width strip above the
// header (SessionBanner), a one-line note under a field, or async feedback
// (a toast).
//
// The colours are the Chip palette's: the surface from chipSurfaceClass, the
// text from chipTextClass, and the border is that text colour at low opacity,
// so a palette change moves the alert with the chips. A panel nested inside
// that needs neutral text takes `bg-background text-foreground border-border`.
const TONES = {
  warning: { color: 'yellow', role: 'status' },
  error: { color: 'red', role: 'alert' },
} as const satisfies Record<string, { color: ChipColor; role: 'alert' | 'status' }>

export type AlertTone = keyof typeof TONES

export interface AlertProps extends Omit<React.ComponentProps<'div'>, 'title'> {
  tone: AlertTone
  /** Leading icon; a lucide icon is sized by the alert. Decorative, hidden from screen readers. */
  icon?: ReactNode
  /** Bold first line. The children follow under it. */
  title?: ReactNode
  /**
   * Layout only (margins, width). The tone owns colour, border and padding.
   * `role` defaults to `alert` for an error and `status` for a warning; a
   * warning that blocks the user's next step may pass `role="alert"`.
   */
  className?: string
}

export function Alert({ tone, icon, title, className, children, role, ...props }: AlertProps) {
  const { color, role: toneRole } = TONES[tone]
  return (
    <div
      data-slot="alert"
      data-tone={tone}
      role={role ?? toneRole}
      className={cn(
        'flex items-start gap-2 rounded-md border border-current/20 p-3 text-sm',
        chipSurfaceClass(color),
        chipTextClass(color),
        className,
      )}
      {...props}
    >
      {icon != null && (
        <span aria-hidden="true" className="mt-0.5 inline-flex shrink-0 [&_svg]:size-4">
          {icon}
        </span>
      )}
      <div className="min-w-0 flex-1">
        {title != null && <div className="font-medium">{title}</div>}
        {children != null && <div className={title != null ? 'mt-1' : undefined}>{children}</div>}
      </div>
    </div>
  )
}
