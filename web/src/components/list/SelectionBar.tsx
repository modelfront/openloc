// The bar that appears above a list once something in it is selected: what is
// selected, a button that clears it, then the actions for it. One shell, so
// every selectable list (the Tasks matrix, the Files table) reads the same.

import type { ReactNode } from 'react'
import { X } from 'lucide-react'

import { Button } from '@/components/ui/button'

interface Props {
  /** "3 tasks selected" — the caller words it; the bar only shows it. */
  label: string
  onClear: () => void
  /** Disables the clear button, e.g. while an action is running. */
  busy?: boolean
  /** Test ids are `<prefix>-actions-bar`, `-selected-count`, `-clear-selection`. */
  testIdPrefix?: string
  children: ReactNode
}

export function SelectionBar({ label, onClear, busy = false, testIdPrefix = 'bulk', children }: Props) {
  return (
    <div
      data-testid={`${testIdPrefix}-actions-bar`}
      className="sticky top-0 z-10 flex flex-wrap items-center gap-2 rounded-lg border border-primary/40 bg-primary/10 px-3 py-2 backdrop-blur"
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
  )
}

/** Separates groups of actions inside a SelectionBar. */
export function SelectionBarDivider() {
  return <div className="mx-1 h-4 w-px bg-primary/40" />
}
