/**
 * The reorder glyph: two wide horizontal lines flanked by up/down chevrons.
 * On a sortable row render `DragHandle` (which wraps this), not the bare icon.
 */
export function ReorderIcon({ className = 'size-4' }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      <path d="M8 5l4-3 4 3" />
      <path d="M4 10h16" />
      <path d="M4 14h16" />
      <path d="M8 19l4 3 4-3" />
    </svg>
  )
}
