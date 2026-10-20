import { useState, type ReactNode } from 'react'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, arrayMove, rectSortingStrategy, sortableKeyboardCoordinates, useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { EmptyState } from '@/components/layout/EmptyState'
import { DragHandle } from '@/components/ui/drag-handle'

/**
 * The grid of chart cards on a dashboard, sortable by drag when `canArrange`.
 * The order is applied optimistically: a card that snaps back to its old slot
 * while the request flies reads as a failed drag. The server's order wins
 * until a drag overrides it, and a chart the drag did not know about goes last.
 */
export function ChartGrid<T extends { id: number; title: string }>({ charts, canArrange, onReorder, emptyMessage, children }: {
  charts: T[]
  canArrange: boolean
  /** Every chart id, in the new order. */
  onReorder: (ids: number[]) => void
  emptyMessage: string
  children: (chart: T) => ReactNode
}) {
  const [order, setOrder] = useState<number[] | null>(null)
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const byId = new Map(charts.map(c => [c.id, c]))
  const known = (order ?? []).filter(id => byId.has(id))
  const ids = [...known, ...charts.map(c => c.id).filter(id => !known.includes(id))]
  const ordered = ids.map(id => byId.get(id)).filter((c): c is T => c !== undefined)

  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const from = ids.indexOf(Number(active.id))
    const to = ids.indexOf(Number(over.id))
    if (from < 0 || to < 0) return
    const next = arrayMove(ids, from, to)
    setOrder(next)
    onReorder(next)
  }

  if (ordered.length === 0) return <EmptyState message={emptyMessage} />

  const grid = (
    // auto-rows-fr: every row as tall as the tallest card, so a card with a
    // legend, a filter row or a longer title never leaves its neighbours short.
    <div className="grid auto-rows-fr grid-cols-1 gap-4 lg:grid-cols-2" data-testid="dashboard-charts">
      {/* Tours point at the first chart, not the whole grid (see ProjectList). */}
      {ordered.map((chart, i) =>
        canArrange
          ? <SortableChart key={chart.id} id={chart.id} title={chart.title} guide={i === 0 ? 'dashboard.charts' : undefined}>{children(chart)}</SortableChart>
          : <div key={chart.id} className="h-full" data-guide={i === 0 ? 'dashboard.charts' : undefined}>{children(chart)}</div>,
      )}
    </div>
  )
  if (!canArrange) return grid
  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
      <SortableContext items={ids} strategy={rectSortingStrategy}>{grid}</SortableContext>
    </DndContext>
  )
}

function SortableChart({ id, title, guide, children }: {
  id: number
  title: string
  guide?: string
  children: ReactNode
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id })
  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`group/sort relative h-full ${isDragging ? 'z-10 opacity-80' : ''}`}
      data-testid="dashboard-chart-sortable"
      data-guide={guide}
    >
      <DragHandle
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        aria-label={`Reorder ${title}`}
        title="Drag to reorder. Keyboard: Space, arrows, Space."
        data-testid="chart-drag-handle"
        className="absolute left-1/2 top-0 z-10 -translate-x-1/2 opacity-0 transition-opacity focus-visible:opacity-100 group-hover/sort:opacity-100"
      />
      {children}
    </div>
  )
}
