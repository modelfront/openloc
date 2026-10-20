// Shared chip-style filter primitives: one popover chip + one option row. Each
// page composes its own bar with page-specific chips.
//
// FilterChip = popover trigger + content. Trigger shows label + active value.
// Controlled `open` state: picking an option closes the popover. `render` is a
// function so children can call `close()` from their onClick. `onRemove` adds
// an × after the value, for a bar where the user picks which chips it shows.

import { useState, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

export function FilterChip({
  label, value, testId, render, size = 'sm', onRemove,
}: {
  label: string
  value: string | null
  testId: string
  render: (close: () => void) => ReactNode
  /**
   * Chip height. `sm` (default) is the compact 26px chip for a stand-alone
   * filter row. `md` matches the shared form-control height (h-input, 30px) so
   * a bar that sits next to inputs/search boxes lines up — see ProjectFilterBar.
   */
  size?: 'sm' | 'md'
  /** Shows an × that takes the chip off the bar (the caller clears its value). */
  onRemove?: () => void
}) {
  const [open, setOpen] = useState(false)
  const active = value != null
  const heightClass = size === 'md' ? 'h-input' : 'py-1'
  const frameClass = `rounded-md border transition-colors ${
    active
      ? 'bg-primary/10 text-primary border-primary'
      : 'bg-card text-gray-600 dark:text-gray-400 border-gray-200 dark:border-gray-800 hover:border-gray-300 dark:hover:border-gray-700'
  }`
  const trigger = (
    <PopoverTrigger
      data-testid={testId}
      className={`inline-flex items-center gap-1.5 pl-2.5 ${onRemove ? 'pr-1' : `pr-2.5 ${frameClass}`} ${heightClass} rounded-md text-xs font-medium`}
    >
      <span className="text-gray-500 dark:text-gray-400">{label}:</span>
      {/* A value can be a long file name; the open list shows it whole. */}
      <span className="max-w-48 truncate font-semibold">{value ?? 'All'}</span>
    </PopoverTrigger>
  )
  return (
    <Popover open={open} onOpenChange={setOpen}>
      {onRemove ? (
        // The × is a sibling of the trigger, not inside it: a button can't
        // hold a button. The frame wraps both so they read as one chip.
        <span className={`inline-flex items-center ${frameClass}`}>
          {trigger}
          <Button
            variant="ghost"
            size="icon-xs"
            className="mr-0.5 size-5"
            onClick={onRemove}
            aria-label={`Remove the ${label} filter`}
            title={`Remove the ${label} filter`}
            data-testid={`${testId}-remove`}
          >
            <X />
          </Button>
        </span>
      ) : trigger}
      <PopoverContent className="w-56 p-1 max-h-72 overflow-y-auto" align="start">
        {render(() => setOpen(false))}
      </PopoverContent>
    </Popover>
  )
}

export function FilterOption({
  selected, onClick, children, 'data-testid': testId,
}: { selected: boolean; onClick: () => void; children: ReactNode; 'data-testid'?: string }) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- shared filter-chip primitive; the sanctioned non-action control
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      className={`w-full text-left px-2 py-1.5 rounded text-sm transition-colors ${
        selected
          ? 'bg-primary/10 text-primary font-medium'
          : 'hover:bg-gray-100 dark:hover:bg-gray-800 text-gray-700 dark:text-gray-300'
      }`}
    >
      {children}
    </button>
  )
}
