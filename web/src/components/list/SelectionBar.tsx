// The bar that appears above a list once something in it is selected: what is
// selected, a button that clears it, then the actions for it. One shell, so
// every selectable list (the Tasks matrix, the Files table) reads the same.

import type { ReactNode } from 'react'
import { X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { useStuck } from '@/hooks/useStuck'
import { cn } from '@/lib/utils'

interface Props {
  /** "3 tasks selected" — the caller words it; the bar only shows it. */
  label: string
  onClear: () => void
  /** Disables the clear button, e.g. while an action is running. */
  busy?: boolean
  /** Test ids are `<prefix>-actions-bar`, `-selected-count`, `-clear-selection`. */
  testIdPrefix?: string
  /** Spacing around the bar (`mb-2`). Pass it here, never on a wrapper: a
   *  sticky element cannot leave its parent, so a wrapper stops it sticking. */
  className?: string
  children: ReactNode
}

export function SelectionBar({ label, onClear, busy = false, testIdPrefix = 'bulk', className, children }: Props) {
  // A sticky element sticks below its scroll area's top padding. The app
  // shell's <main> declares that padding as `--scroll-inset-top`, and the bar
  // sticks that far up, flush under the header, so no rows show in a strip
  // above it; elsewhere it sticks at the edge. The outer layer is the page
  // colour and the tint sits on it, so rows never show through. Pinned, the
  // bar's top corners are square.
  const [ref, stuck] = useStuck<HTMLDivElement>()
  const corners = stuck ? 'rounded-b-lg' : 'rounded-lg'
  return (
    <div ref={ref} className={cn('sticky top-[calc(var(--scroll-inset-top,0px)*-1)] z-10 bg-background', corners, className)}>
      <div
        data-testid={`${testIdPrefix}-actions-bar`}
        className={cn('flex flex-wrap items-center gap-2 border border-primary/40 bg-primary/10 px-3 py-2', corners)}
      >
        <span className="text-sm font-medium text-primary" data-testid={`${testIdPrefix}-selected-count`}>
          {label}
        </span>
        <Button variant="ghost" size="icon-xs"
          onClick={onClear}
          disabled={busy}
          title="Clear selection"
          aria-label="Clear selection"
          className="text-primary hover:text-primary"
          data-testid={`${testIdPrefix}-clear-selection`}
        >
          <X className="size-3.5" />
        </Button>
        <SelectionBarDivider />
        {children}
      </div>
    </div>
  )
}

/** Separates groups of actions inside a SelectionBar. */
export function SelectionBarDivider() {
  return <div className="mx-1 h-4 w-px bg-primary/40" />
}
