import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { rowButtonProps } from '@/components/list/row-button'
import { ProviderIcon } from './ProviderIcon'

/**
 * A provider tile: the AI company's logo on the left, its name on the right, in a
 * rounded rectangle.
 *
 * Sized to fit FOUR across a collapsible section's body — via the 4-column grid
 * in `<ProviderTileRow>`, not via a fixed width on the tile.
 *
 * A fixed width cannot promise "four across": `w-60` (240px) needs 984px with
 * the gaps, which fits the collapsible body (1000px) but silently wraps to
 * three in the 976px component gallery. The grid divides whatever width it is
 * given by four, so the promise holds in any container. The tile is therefore
 * `w-full` and takes its size from the cell.
 */
export function ProviderTile({
  provider,
  name,
  meta,
  selected = false,
  enabled,
  onClick,
  className,
  'data-testid': testId,
}: {
  /** Provider id — drives the logo (`openai` | `claude` | `ext:<id>` | …). */
  provider: string
  /** Display name. Defaults to a title-cased provider id. */
  name?: string
  /** Optional second line (an endpoint, a key count) under the name. */
  meta?: ReactNode
  selected?: boolean
  /**
   * On/off state. `undefined` renders a stateless tile (the gallery, a picker);
   * only pass it where the thing genuinely has a switch, so an ordinary tile
   * isn't mistaken for a permanently-disabled one.
   */
  enabled?: boolean
  onClick?: () => void
  className?: string
  'data-testid'?: string
}) {
  const label = name ?? (provider.charAt(0).toUpperCase() + provider.slice(1))
  const interactive = !!onClick

  return (
    <div
      className={cn(
        'flex w-full items-center gap-2.5 rounded-lg border px-3 py-2.5',
        'border-gray-200 bg-card dark:border-gray-800',
        // Enabled is tinted rather than merely "not faded" so the two states are
        // told apart at a glance in a grid, not only side by side.
        enabled === true && 'border-primary/50 bg-primary/[0.04]',
        enabled === false && 'border-dashed bg-transparent opacity-60',
        interactive && 'cursor-pointer transition-colors hover:bg-muted',
        // Selection reads as a ring rather than a fill, so the brand colour of
        // the logo stays the loudest thing in the tile.
        selected && 'border-primary ring-1 ring-primary',
        interactive && 'outline-hidden focus-visible:ring-2 focus-visible:ring-ring/60',
        className,
      )}
      {...rowButtonProps(onClick)}
      aria-pressed={interactive ? selected : undefined}
      data-testid={testId}
    >
      <ProviderIcon provider={provider} className="size-7 rounded-md text-sm" />
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{label}</span>
        {meta && <span className="truncate text-xs text-muted-foreground">{meta}</span>}
      </span>
    </div>
  )
}

/**
 * Row wrapper for `ProviderTile`: a 4-column grid (2 on a phone), so the tiles
 * keep one size and the next one starts a second row.
 * Owns the gap so callers can't drift on spacing.
 */
export function ProviderTileRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('grid grid-cols-4 gap-2 max-md:grid-cols-2', className)}>{children}</div>
}
