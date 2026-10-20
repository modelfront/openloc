// The grid of dashboard cards on the dashboards list: Pinned, Organization
// dashboards and My dashboards all use it.
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, arrayMove, rectSortingStrategy, sortableKeyboardCoordinates, useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { DashboardPreviewChart } from '@/api'
import { DragHandle } from '@/components/ui/drag-handle'
import { EditableTitle } from '@/components/list/EditableTitle'
import { DashboardPreview } from './DashboardPreview'

export interface DashboardCardItem {
  id: number
  name: string
  chart_count: number
  preview_charts: DashboardPreviewChart[]
}

/**
 * Where a dragged card lands. Pure, so the ordering decision is testable
 * without a browser.
 *
 * Drag carries ONE meaning — reorder within the tab. Moving between tabs is an
 * explicit action on the card: one gesture for both would leave the drop
 * guessing whether the user meant to sort or to move.
 */
export type DropOutcome =
  | { kind: 'none' }
  | { kind: 'reorder'; from: number; to: number }

export function resolveDrop(
  items: { id: number }[],
  activeId: string | number,
  overId: string | number | undefined,
): DropOutcome {
  if (overId === undefined || String(overId) === String(activeId)) return { kind: 'none' }
  const from = items.findIndex(d => String(d.id) === String(activeId))
  const to = items.findIndex(d => String(d.id) === String(overId))
  if (from < 0 || to < 0) return { kind: 'none' }
  return { kind: 'reorder', from, to }
}

/**
 * Sortable grid of cards. The server's order wins until a drag overrides it,
 * so a refetch (or someone else's reorder) is not fighting local state. Key it
 * by tab: each tab is its own order.
 */
export function DashboardCardGrid<T extends { id: number }>({ items, onReorder, children }: {
  items: T[]
  /** Every id in the grid, in the new order. */
  onReorder: (ids: number[]) => void
  children: (item: T, index: number) => ReactNode
}) {
  // Pointer AND keyboard: dragging is the only way to reorder dashboards, so it
  // cannot be mouse-only.
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const [order, setOrder] = useState<number[] | null>(null)
  const byId = new Map(items.map(d => [d.id, d]))
  const shown = order
    ? order.map(id => byId.get(id)).filter((d): d is T => d !== undefined)
    : items

  const onDragEnd = ({ active, over }: DragEndEvent) => {
    const outcome = resolveDrop(shown, active.id, over?.id)
    if (outcome.kind !== 'reorder') return
    const next = arrayMove(shown.map(d => d.id), outcome.from, outcome.to)
    setOrder(next)
    onReorder(next)
  }

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
      <SortableContext items={shown.map(d => d.id)} strategy={rectSortingStrategy}>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2" data-testid="dashboards-grid">
          {shown.map((d, i) => children(d, i))}
        </div>
      </SortableContext>
    </DndContext>
  )
}

/**
 * One dashboard card: a link to `href` with the name, chart count and preview,
 * or the rename field in its place while `renaming`.
 *
 * The `pin` sits where a leading icon would, before the name, always shown.
 * Like the actions it is laid over the card rather than inside the link: a
 * button nested in an anchor is not valid. The actions sit over the end of the
 * name, on the page's own background, and show while the card is hovered or
 * focused, or while one of their menus is open.
 */
export function DashboardCard({ item, href, guide, renaming, onRename, onRenameDone, pin, actions }: {
  item: DashboardCardItem
  href: string
  /** The tour anchor this card carries (the first card only). */
  guide?: string
  renaming: boolean
  onRename: (name: string) => void
  onRenameDone: () => void
  pin: ReactNode
  actions?: ReactNode
}) {
  const count = (
    <p className="mt-1 text-sm text-gray-500">
      {item.chart_count} {item.chart_count === 1 ? 'chart' : 'charts'}
    </p>
  )
  return (
    <DraggableDashboardCard id={item.id} name={item.name} editing={renaming} guide={guide}>
      {renaming ? (
        // In place of the link, not inside it: an input nested in an anchor
        // swallows its own clicks.
        <div className="p-4">
          {/* pl-6: the room the pin takes before the name. */}
          <div className="flex items-center pl-6">
            <EditableTitle
              name={item.name}
              onRename={onRename}
              editing
              onEditingChange={open => { if (!open) onRenameDone() }}
              fieldClassName="font-medium"
              fieldLabel="Rename dashboard"
            />
          </div>
          {count}
          <DashboardPreview charts={item.preview_charts} />
        </div>
      ) : (
        <Link to={href} className="block p-4" data-testid="dashboard-card">
          {/* One line, the full card width. The hover actions cover the end of
              it rather than reserving room, so a name is only cut short while
              the pointer is over its card. A name too long for the card gets a
              tooltip. */}
          <div className="flex items-center pl-6">
            <span className="flex min-w-0" data-testid="dashboard-card-name">
              <EditableTitle name={item.name} canEdit={false} className="font-medium" />
            </span>
          </div>
          {count}
          <DashboardPreview charts={item.preview_charts} />
        </Link>
      )}
      {/* Centred on the name's line: p-4 above, a 24px button on a 24px line. */}
      <div className="absolute left-3 top-4">{pin}</div>
      {/* The field owns the card while it is open: hover actions would overlap
          it and read as belonging to the input. Not rendered rather than
          hidden — an invisible button is still tabbable. */}
      {!renaming && (
        <div
          data-testid="dashboard-card-actions"
          className="absolute right-2 top-3 flex gap-1 rounded-md bg-background pl-2 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 has-[[data-popup-open]]:opacity-100"
        >
          {actions}
        </div>
      )}
    </DraggableDashboardCard>
  )
}

// The card is the drag source. The grip is the only activator, so clicking the
// card still opens the dashboard — a whole-card drag would swallow the link.
function DraggableDashboardCard({ id, name, editing, guide, children }: {
  id: number
  name: string
  guide?: string
  editing: boolean
  children: ReactNode
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id })
  return (
    <div
      ref={setNodeRef}
      data-testid="dashboard-card-draggable"
      data-guide={guide}
      data-dragging={isDragging ? 'true' : undefined}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`group relative rounded-lg border border-gray-200 transition-colors hover:border-primary dark:border-gray-800 ${
        // The overlay is what the pointer carries, so the original reads as the
        // slot it came from rather than a second copy of the same card.
        isDragging ? 'opacity-40' : ''
      }`}
    >
      {/* Absent while the name is being edited: it sits over the field, and
          dragging a card you are typing into would cancel the edit. */}
      {!editing && (
        <DragHandle
          ref={setActivatorNodeRef}
          {...attributes}
          {...listeners}
          aria-label={`Reorder ${name}`}
          title="Drag to reorder. Keyboard: Space, arrows, Space."
          data-testid="dashboard-drag-handle"
          className="absolute left-1/2 top-0 -translate-x-1/2 opacity-0 transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
        />
      )}
      {children}
    </div>
  )
}
