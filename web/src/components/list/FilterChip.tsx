// Shared chip-style filter primitives: one popover chip + one option row. Each
// page composes its own bar with page-specific chips.
//
// FilterChip = popover trigger + content. Trigger shows label + active value.
// Controlled `open` state: picking an option closes the popover. `render` is a
// function so children can call `close()` from their onClick. `onRemove` adds
// an × after the value, for a bar where the user picks which chips it shows.
// A chip that takes several values leaves `close()` out of its toggles and
// marks them `multi`, so the popover stays open and each pick shows a check.
//
// FilterOptions = the option list inside the popover, with a search box that
// hides the options whose text does not match; an option that is not plain
// text says what to match on with `keywords`. Search is on by default, like
// Combobox's: only a short fixed list (a status, deadline bands) passes
// `searchable={false}`, never a choice based on how many options there are.

import { Children, createContext, Fragment, isValidElement, useContext, useState, type ReactNode } from 'react'
import { Check, Search, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

const FilterSearchContext = createContext('')

const FRAME_BASE = 'rounded-md border transition-colors'
/** A chip with no value set; AddFilterMenu's button wears it too, so the bar reads as one row. */
export const FILTER_CHIP_IDLE_FRAME = `${FRAME_BASE} bg-card text-gray-600 dark:text-gray-400 border-gray-200 dark:border-gray-800 hover:border-gray-300 dark:hover:border-gray-700`
const FILTER_CHIP_ACTIVE_FRAME = `${FRAME_BASE} bg-primary/10 text-primary border-primary`
/**
 * A chip's height at each size: sm is its padding around one 16px line of
 * text-xs (26px with the border), md the form-control height (30px). The
 * filter icon (AddFilterMenu) takes the same, so a bar is one height.
 */
export const FILTER_CHIP_HEIGHT = { sm: 'py-1', md: 'h-input' } as const

export function FilterChip({
  label, value, testId, render, size = 'sm', onRemove, searchable = true,
}: {
  label: string
  value: string | null
  testId: string
  render: (close: () => void) => ReactNode
  /**
   * Chip height. `sm` (default) is the compact 26px chip for a stand-alone
   * filter row. `md` matches the shared form-control height (h-input, 30px) so
   * a bar that sits next to inputs/search boxes lines up — see ProjectFilterBar.
   */
  size?: 'sm' | 'md'
  /** Shows an × that takes the chip off the bar (the caller clears its value). */
  onRemove?: () => void
  /** False for a short fixed list, which needs no search box. */
  searchable?: boolean
}) {
  const [open, setOpen] = useState(false)
  const active = value != null
  const heightClass = FILTER_CHIP_HEIGHT[size]
  // A removable chip's border is on the frame around the trigger and the ×,
  // so at md the frame takes the fixed height and the trigger fills it;
  // a fixed-height trigger inside the border would make the chip 2px taller.
  const fixedFrame = onRemove && size === 'md'
  const frameClass = active ? FILTER_CHIP_ACTIVE_FRAME : FILTER_CHIP_IDLE_FRAME
  const trigger = (
    <PopoverTrigger
      data-testid={testId}
      className={`inline-flex items-center gap-1.5 pl-2.5 ${onRemove ? 'pr-1' : `pr-2.5 ${frameClass}`} ${fixedFrame ? 'h-full' : heightClass} rounded-md text-xs font-medium`}
    >
      <span className="text-gray-500 dark:text-gray-400">{label}:</span>
      {/* A value can be a long file name; the open list shows it whole. */}
      <span className="max-w-48 truncate font-semibold">{value ?? 'All'}</span>
    </PopoverTrigger>
  )
  return (
    <Popover open={open} onOpenChange={setOpen}>
      {onRemove ? (
        // The × is a sibling of the trigger, not inside it: a button can't
        // hold a button. The frame wraps both so they read as one chip.
        <span className={`inline-flex items-center ${fixedFrame ? 'h-input' : ''} ${frameClass}`}>
          {trigger}
          <Button
            variant="ghost"
            size="icon-xs"
            className="mr-0.5 size-5"
            onClick={onRemove}
            aria-label={`Remove the ${label} filter`}
            title={`Remove the ${label} filter`}
            data-testid={`${testId}-remove`}
          >
            <X />
          </Button>
        </span>
      ) : trigger}
      <PopoverContent className="w-56 p-1 max-h-72 overflow-y-auto" align="start">
        <FilterOptions searchable={searchable} testId={`${testId}-search`}>{render(() => setOpen(false))}</FilterOptions>
      </PopoverContent>
    </Popover>
  )
}

/**
 * A popover's option rows, under a search box unless `searchable` is false.
 * The box starts empty on every opening: the list mounts with the popover.
 */
export function FilterOptions({ children, testId, searchable = true }: { children: ReactNode; testId: string; searchable?: boolean }) {
  const [query, setQuery] = useState('')
  if (!searchable) return <>{children}</>

  const q = query.trim().toLowerCase()
  const noMatch = q !== '' && !optionTexts(children).some(t => t.toLowerCase().includes(q))
  return (
    <>
      {/* Sticky, so it stays in reach while the list scrolls under it. */}
      <div className="sticky -top-1 z-10 -mx-1 -mt-1 mb-1 bg-popover p-1">
        <Input
          autoFocus
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder="Search…"
          icon={<Search />}
          className="text-sm"
          data-testid={testId}
        />
      </div>
      <FilterSearchContext.Provider value={q}>{children}</FilterSearchContext.Provider>
      {noMatch && <div className="px-2 py-1.5 text-sm text-gray-500 dark:text-gray-400">No matches</div>}
    </>
  )
}

/** What a search matches an option on: its `keywords`, else its text. */
function optionText(props: { keywords?: string; children?: ReactNode }): string {
  if (props.keywords != null) return props.keywords
  return typeof props.children === 'string' || typeof props.children === 'number' ? String(props.children) : ''
}

/** The search text of every FilterOption in a rendered list, fragments and arrays included. */
function optionTexts(node: ReactNode): string[] {
  const out: string[] = []
  Children.forEach(node, child => {
    if (!isValidElement<{ keywords?: string; children?: ReactNode }>(child)) return
    if (child.type === FilterOption) out.push(optionText(child.props))
    else if (child.type === Fragment) out.push(...optionTexts(child.props.children))
  })
  return out
}

export function FilterOption({
  selected, onClick, children, multi = false, keywords, 'data-testid': testId,
}: {
  selected: boolean
  onClick: () => void
  children: ReactNode
  /** One of several values the chip can hold at once: a toggle with a check. */
  multi?: boolean
  /** What the list's search matches on, for an option that is not plain text. */
  keywords?: string
  'data-testid'?: string
}) {
  const query = useContext(FilterSearchContext)
  if (query && !optionText({ keywords, children }).toLowerCase().includes(query)) return null
  return (
    // eslint-disable-next-line no-restricted-syntax -- shared filter-chip primitive; the sanctioned non-action control
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      aria-pressed={multi ? selected : undefined}
      className={`w-full text-left px-2 py-1.5 rounded text-sm transition-colors ${
        multi ? 'flex items-center justify-between gap-2' : ''
      } ${
        selected
          ? 'bg-primary/10 text-primary font-medium'
          : 'hover:bg-gray-100 dark:hover:bg-gray-800 text-gray-700 dark:text-gray-300'
      }`}
    >
      {children}
      {multi && selected && <Check className="size-4 shrink-0" aria-hidden />}
    </button>
  )
}
