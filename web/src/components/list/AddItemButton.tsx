import { Plus } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LIST_ROW_HEIGHT } from './list-row'

/**
 * A slim, full-width dashed "+ New …" button — the standard "add a row" call to
 * action for editable lists. Shares `LIST_ROW_HEIGHT` with `listRowClass` so it
 * is exactly a row tall (it has no action button of its own to set the height).
 */
export function AddItemButton({
  label, onClick, testId, className, disabled, title,
}: {
  label: string
  onClick: () => void
  testId?: string
  className?: string
  /** Closes the affordance when there is nothing useful a new row could be. */
  disabled?: boolean
  /** Hover text — say why, when the button is disabled. */
  title?: string
}) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- shared add-row affordance primitive; the sanctioned non-action control
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      data-testid={testId}
      className={cn(
        LIST_ROW_HEIGHT,
        'flex w-full items-center justify-center gap-2 rounded-md border border-dashed border-gray-300 dark:border-gray-700 px-3 py-1 text-sm font-medium text-muted-foreground transition-colors',
        'not-disabled:hover:border-primary not-disabled:hover:text-foreground',
        'disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
    >
      <Plus className="size-4" /> {label}
    </button>
  )
}
