// Drag-to-sort for a vertical list (dnd-kit, pointer + keyboard). The list
// renders no DOM of its own; its rows are `SortableGridRow`s (or any
// `useSortable` row) whose grip is the only activator. Rows cannot leave the
// list's parent element, so they never poke out of a scroll box, and a list
// placed after some fixed rows can never be dropped above them.
import type { ReactNode } from 'react'
import { DndContext, closestCenter, type DragEndEvent, type UniqueIdentifier } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { restrictToParentElement, restrictToVerticalAxis } from '@dnd-kit/modifiers'
import { useSortableSensors } from '@/hooks/useSortableSensors'

/** The positions a drop of `active` on `over` moves between, or null when it
 *  moves nothing (dropped on itself, outside the list, or an unknown id). */
export function dropMove(ids: UniqueIdentifier[], active: UniqueIdentifier, over: UniqueIdentifier | null | undefined): [number, number] | null {
  if (over == null || active === over) return null
  const from = ids.indexOf(active)
  const to = ids.indexOf(over)
  return from < 0 || to < 0 ? null : [from, to]
}

/** The list: `ids` in display order; `onMove` reports a drop as positions in it. */
export function SortableList({ ids, onMove, children }: {
  ids: UniqueIdentifier[]
  onMove: (from: number, to: number) => void
  children: ReactNode
}) {
  const sensors = useSortableSensors()
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    const move = dropMove(ids, active.id, over?.id)
    if (move) onMove(...move)
  }
  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis, restrictToParentElement]} onDragEnd={onDragEnd}>
      <SortableContext items={ids} strategy={verticalListSortingStrategy}>
        {children}
      </SortableContext>
    </DndContext>
  )
}
