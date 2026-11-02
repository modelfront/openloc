import type { ReactNode } from 'react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { UniqueIdentifier } from '@dnd-kit/core'
import { cn } from '@/lib/utils'

export interface SortableHandle {
  handleRef: (el: HTMLElement | null) => void
  handleProps: Record<string, unknown>
}

/**
 * One sortable row of a CSS grid list. dnd-kit owns the transform and
 * transition (rows slide out of the way while dragging), and the activator is
 * the grip the children render with `handleRef`/`handleProps`, so a click
 * anywhere else in the row never starts a drag. The dragged row is the
 * placeholder: tinted and outlined, fully opaque.
 */
export function SortableGridRow({ id, disabled, gridCols, testId, className, children }: {
  id: UniqueIdentifier
  disabled: boolean
  gridCols: string
  testId: string
  className?: string
  children: (h: SortableHandle) => ReactNode
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id, disabled })
  return (
    <div ref={setNodeRef} data-testid={testId} data-dragging={isDragging ? 'true' : undefined}
      className={cn('grid items-center', className, isDragging && 'relative z-10 bg-primary/10 ring-1 ring-inset ring-primary/50')}
      style={{ gridTemplateColumns: gridCols, transform: CSS.Transform.toString(transform), transition }}>
      {children({ handleRef: setActivatorNodeRef, handleProps: { ...attributes, ...listeners } })}
    </div>
  )
}
