import { SEGMENT_GRID_COLS } from './SegmentRow'

/**
 * A segment grid row whose text is still loading: its number, and a muted
 * line where the source and translation will be. Same grid and height as a
 * compact SegmentRow, so the row does not jump when the text arrives.
 */
export function SegmentRowPlaceholder({ number }: { number: string }) {
  return (
    <div
      className={`${SEGMENT_GRID_COLS} min-h-(--seg-row-min-h) border-b border-gray-100 dark:border-gray-800`}
      data-testid="segment-row-loading"
      aria-busy="true"
    >
      <div className="segment-cell px-3 text-xs text-gray-400 dark:text-gray-500">{number}</div>
      <div className="segment-cell px-3"><div className="h-4 w-3/4 rounded bg-muted animate-pulse" /></div>
      <div className="segment-cell px-3"><div className="h-4 w-2/3 rounded bg-muted animate-pulse" /></div>
    </div>
  )
}
