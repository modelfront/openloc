import { cn } from '@/lib/utils'

/**
 * The view over an area while files are dragged onto it. The area must be
 * positioned (`relative`, or `fixed` like the assistant panel); the view lets
 * every pointer event through, so the area's own drop handlers still fire.
 */
export function FileDropOverlay({ show, label, hint, className }: {
  show: boolean
  label: string
  /** A smaller line under the label, such as a limit. */
  hint?: string
  className?: string
}) {
  if (!show) return null
  return (
    <div
      data-testid="file-drop-overlay"
      className={cn(
        'pointer-events-none absolute inset-0 z-50 flex flex-col items-center justify-center gap-1 rounded-xl border-2 border-dashed border-primary bg-background/90 backdrop-blur-sm',
        className,
      )}
    >
      <p className="text-lg font-medium text-primary">{label}</p>
      {hint && <p className="text-sm text-primary">{hint}</p>}
    </div>
  )
}
