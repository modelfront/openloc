import type { ComponentProps } from 'react'
import { GripVertical } from 'lucide-react'

import { cn } from '@/lib/utils'

/**
 * The drag strip on a column header's right edge, centred on it so it
 * straddles the column border. The header cell must be positioned (`relative`
 * or `sticky`) and must not clip its overflow. The caller wires the drag
 * (pointer/mouse handlers) and double-click reset; this owns the look and the
 * separator semantics.
 *
 * `hover` (default): the strip shows only on hover, for tables whose column
 * borders already show where to drag. `grip`: a grip always sits on the edge,
 * for a table without them, so the column reads as resizable before anyone
 * hovers it.
 */
export function ColumnResizeHandle({ active = false, variant = 'hover', className, ...props }: ComponentProps<'div'> & {
  active?: boolean
  variant?: 'hover' | 'grip'
}) {
  const grip = variant === 'grip'
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize column"
      className={cn(
        'group/resize absolute top-0 right-0 z-10 h-full translate-x-1/2 cursor-col-resize touch-none select-none',
        grip ? 'flex w-3 items-center justify-center rounded-sm' : 'w-1.5',
        active ? 'bg-primary/50' : 'hover:bg-primary/30',
        className,
      )}
      {...props}
    >
      {grip && (
        <GripVertical
          aria-hidden="true"
          className={cn('size-3.5', active ? 'text-primary' : 'text-gray-400 group-hover/resize:text-primary dark:text-gray-500')}
        />
      )}
    </div>
  )
}
