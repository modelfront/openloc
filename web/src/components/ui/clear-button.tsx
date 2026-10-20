import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/**
 * The "empty this field" X shown inside `Input` whenever it holds text.
 * `Textarea` has none.
 *
 * Not in the tab order, matching Chrome's own `type="search"` clear button —
 * with the X on every text field in the app, tabbing through a form would
 * otherwise double in length. `onMouseDown` swallows the event so the click
 * never blurs the field: fields that commit on blur would otherwise commit the
 * old value first and then clear.
 */
export function ClearButton({
  onClear,
  className,
  'data-testid': testId,
}: {
  onClear: () => void
  className?: string
  'data-testid'?: string
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      tabIndex={-1}
      aria-label="Clear"
      data-slot="input-clear"
      data-testid={testId}
      onMouseDown={e => e.preventDefault()}
      onClick={onClear}
      className={cn('shrink-0 text-muted-foreground hover:text-foreground', className)}
    >
      <X className="size-3.5" />
    </Button>
  )
}
