import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { restrictToParentElement, restrictToVerticalAxis } from '@dnd-kit/modifiers'
import { Settings2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from '@/components/ui/popover'
import { DragHandle } from '@/components/ui/drag-handle'
import {
  SIDEBAR_SECTION_LABELS,
  isSectionVisible,
  moveSection,
  toggleSectionVisible,
  visibleSections,
  type SidebarLayout,
  type SidebarSectionId,
} from '@/lib/sidebar-sections'

interface Props {
  layout: SidebarLayout
  onChange: (layout: SidebarLayout) => void
}

/**
 * The gear in the Resources header. Opens a popover listing every sidebar
 * section in its current order: drag the grip to reorder, tick the box to show
 * or hide. Each change is reported through `onChange` immediately — the parent
 * persists it, so there is no Save button.
 */
export function EditorSidebarCustomizer({ layout, onChange }: Props) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const handleDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const from = layout.order.indexOf(active.id as SidebarSectionId)
    const to = layout.order.indexOf(over.id as SidebarSectionId)
    onChange(moveSection(layout, from, to))
  }
  const onlyOneVisible = visibleSections(layout).length === 1

  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Customize panel"
            title="Customize panel"
            data-testid="sidebar-customize-trigger"
          />
        }
      >
        <Settings2 className="w-3.5 h-3.5 text-gray-400" />
      </PopoverTrigger>
      <PopoverContent side="bottom" align="end" size="compact" className="w-64" data-testid="sidebar-customize-popover">
        <PopoverTitle>Customize panel</PopoverTitle>
        <PopoverDescription>Drag to reorder. Untick a section to hide it.</PopoverDescription>
        <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis, restrictToParentElement]} onDragEnd={handleDragEnd}>
          <SortableContext items={layout.order} strategy={verticalListSortingStrategy}>
            <div className="flex flex-col">
              {layout.order.map(id => {
                const visible = isSectionVisible(layout, id)
                return (
                  <SortableSectionRow
                    key={id}
                    id={id}
                    checked={visible}
                    // The last visible section stays on; an empty panel would have nothing left to click.
                    disabled={visible && onlyOneVisible}
                    onToggle={() => onChange(toggleSectionVisible(layout, id))}
                  />
                )
              })}
            </div>
          </SortableContext>
        </DndContext>
      </PopoverContent>
    </Popover>
  )
}

// The grip is the only drag activator, so a click on the checkbox toggles the
// section and never starts a drag.
function SortableSectionRow({ id, checked, disabled, onToggle }: {
  id: SidebarSectionId
  checked: boolean
  disabled: boolean
  onToggle: () => void
}) {
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id })
  const label = SIDEBAR_SECTION_LABELS[id]
  return (
    <div
      ref={setNodeRef}
      data-testid={`sidebar-section-row-${id}`}
      data-dragging={isDragging ? 'true' : undefined}
      className={`flex items-center gap-1 rounded-md ${isDragging ? 'relative z-10 bg-primary/10 ring-1 ring-inset ring-primary/50' : ''}`}
      style={{ transform: CSS.Transform.toString(transform), transition }}
    >
      <DragHandle
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        aria-label={`Reorder ${label}`}
        title="Drag to reorder. Keyboard: Space, arrows, Space."
        data-testid={`sidebar-section-drag-${id}`}
      />
      <CheckboxField
        checked={checked}
        disabled={disabled}
        onChange={onToggle}
        label={label}
        className="flex-1 mx-0"
        data-testid={`sidebar-section-toggle-${id}`}
      />
    </div>
  )
}
