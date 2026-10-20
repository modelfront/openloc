import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'
import { Button } from './button'
import { ReorderIcon } from './reorder-icon'

type Props = Omit<ComponentProps<typeof Button>, 'variant' | 'size' | 'color' | 'hoverColor' | 'children'>

/**
 * The grip on a sortable row: a ghost icon button holding `ReorderIcon`, so it
 * shares Button's focus ring, hover box and disabled state. dnd-kit callers
 * pass `ref` (`setActivatorNodeRef`) and spread `attributes` + `listeners`;
 * the HTML5-drag callers pass their own mouse handlers. `aria-label` / `title`
 * default to "Drag to reorder" so a caller only overrides them to say what the
 * order means.
 */
export function DragHandle({
  className,
  'aria-label': ariaLabel = 'Drag to reorder',
  title = 'Drag to reorder',
  ...props
}: Props) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      color="muted"
      hoverColor="muted"
      aria-label={ariaLabel}
      title={title}
      className={cn('cursor-grab active:cursor-grabbing touch-none', className)}
      {...props}
    >
      <ReorderIcon />
    </Button>
  )
}
