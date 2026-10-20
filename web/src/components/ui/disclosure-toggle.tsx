import { type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

interface Props {
  open: boolean
  onToggle: () => void
  /** The toggle's label. */
  children: ReactNode
  /** Muted trailing text (e.g. a one-line summary of the collapsed content). */
  summary?: ReactNode
  /** id of the element the toggle shows/hides, for `aria-controls`. */
  controls?: string
  /**
   * `default`: small sentence-case text; `caps`: 11px uppercase section label;
   * `field`: reads as a `FormField` label, for a whole field that happens to
   * start collapsed — it sits among labelled fields and should carry the same
   * weight as them rather than look like a footnote under the last one.
   * `row`: the name of a list or table row that opens in place (an
   * accordion row), at the row's own text size.
   */
  variant?: 'default' | 'caps' | 'field' | 'row'
  className?: string
  disabled?: boolean
  'data-testid'?: string
}

const VARIANT_CLASS = {
  default: 'gap-1 text-xs text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100',
  caps: 'gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground',
  // A whole field that starts collapsed. Reads at FormField's label weight,
  // but as a row you can press: its own hover fill and a chevron a size up, so
  // the label alone never looks like a heading with nothing behind it.
  field: 'gap-1.5 -mx-1.5 rounded-md px-1.5 py-1 text-xs text-gray-700 dark:text-gray-300 '
    + 'hover:bg-muted hover:text-gray-900 dark:hover:text-gray-100 '
    + 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
  row: 'min-w-0 max-w-full gap-1.5 text-sm font-normal text-gray-900 dark:text-gray-100 disabled:cursor-default',
} as const

const CHEVRON_SIZE = { default: 'size-3.5', caps: 'h-3 w-3', field: 'size-4', row: 'size-4' } as const

/**
 * Inline show/hide control: a chevron that turns when open, `aria-expanded`
 * for the state and `aria-controls` for the body. The one disclosure toggle
 * to use inside cards and rows; page-level sections use SlimCollapsibleSection.
 */
export function DisclosureToggle({ open, onToggle, children, summary, controls, variant = 'default', className, disabled, 'data-testid': testId }: Props) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- shared disclosure-toggle primitive; the sanctioned non-action control
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={open}
      aria-controls={controls}
      disabled={disabled}
      className={cn(
        'flex items-center font-medium',
        VARIANT_CLASS[variant],
        className,
      )}
      data-testid={testId}
    >
      <ChevronRight
        className={cn(
          CHEVRON_SIZE[variant],
          'shrink-0 text-muted-foreground transition-transform',
          open && 'rotate-90',
        )}
        aria-hidden="true"
      />
      {children}
      {summary && <span className="font-normal text-gray-400 dark:text-gray-500">{summary}</span>}
    </button>
  )
}
