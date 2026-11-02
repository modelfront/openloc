import { useState } from 'react'
import { arrayMove } from '@dnd-kit/sortable'
import { SortableGridRow } from '@/components/list/SortableGridRow'
import { SortableList } from '@/components/list/SortableList'
import { DragHandle } from '@/components/ui/drag-handle'

const GRID = '28px 32px minmax(0,1fr)'

/** Three grid rows reordered by their grip, as on the TM rows and segmentation rules. */
export function SortableGridRowDemo() {
  const [rows, setRows] = useState(['Client memory', 'Legal memory', 'Shared memory'])
  return (
    <div className="w-80">
      <SortableList ids={rows} onMove={(from, to) => setRows(prev => arrayMove(prev, from, to))}>
        {rows.map((row, i) => (
          <SortableGridRow key={row} id={row} disabled={false} gridCols={GRID} testId={`demo-sortable-row-${i}`}
            className="gap-2 border-b border-border px-1 py-1.5 text-sm">
            {({ handleRef, handleProps }) => (
              <>
                <DragHandle ref={handleRef} {...handleProps} />
                <span className="tabular-nums text-muted-foreground">{i + 1}</span>
                <span>{row}</span>
              </>
            )}
          </SortableGridRow>
        ))}
      </SortableList>
    </div>
  )
}
