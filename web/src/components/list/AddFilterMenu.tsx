// The "add a filter" button for a bar whose chips show only filters in use.
// One popover, two steps: the list of filters not on the bar, then — right
// away, in the same popover — the picked filter's options. Choosing an option
// sets the filter, which is what puts its chip on the bar; closing without one
// adds nothing. The options are the same render function the filter's
// FilterChip uses, so the two always offer the same choices.

import { useState, type ReactNode } from 'react'
import { ListFilter } from 'lucide-react'

import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { FILTER_CHIP_HEIGHT, FILTER_CHIP_IDLE_FRAME, FilterOption, FilterOptions } from './FilterChip'

export interface AddableFilter {
  key: string
  label: string
  /** The filter's option rows; call `close` after a pick. */
  options: (close: () => void) => ReactNode
  /** False for a short fixed list, as on its FilterChip. */
  searchable?: boolean
}

export function AddFilterMenu({ filters, testId, size = 'sm' }: {
  filters: AddableFilter[]
  testId: string
  /** `md` matches FilterChip's `md` (h-input), for a bar of md chips. */
  size?: 'sm' | 'md'
}) {
  const [open, setOpen] = useState(false)
  const [pickedKey, setPickedKey] = useState<string | null>(null)
  const picked = filters.find(f => f.key === pickedKey)
  const onOpenChange = (next: boolean) => {
    setOpen(next)
    // Every opening starts at the list of filters.
    if (!next) setPickedKey(null)
  }

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      {/* Drawn as an idle FilterChip, not a Button, so it matches the chips
          beside it. Square: md is h-input wide; sm is the chip's padding on all
          sides of a 16px icon, standing in for the chip's 16px line of text. */}
      <PopoverTrigger
        className={`inline-flex shrink-0 items-center justify-center ${size === 'md' ? 'size-input' : `${FILTER_CHIP_HEIGHT.sm} px-1`} ${FILTER_CHIP_IDLE_FRAME}`}
        aria-label="Add a filter"
        title="Add a filter"
        data-testid={testId}
      >
        <ListFilter className={size === 'md' ? 'size-3.5' : 'size-4'} />
      </PopoverTrigger>
      <PopoverContent className="w-56 p-1 max-h-72 overflow-y-auto" align="start" data-testid={`${testId}-popup`}>
        {picked ? (
          <>
            <div className="px-2 pt-1 pb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">{picked.label}</div>
            <FilterOptions searchable={picked.searchable} testId={`${testId}-search`}>{picked.options(() => onOpenChange(false))}</FilterOptions>
          </>
        ) : (
          filters.map(f => (
            <FilterOption key={f.key} selected={false} onClick={() => setPickedKey(f.key)} data-testid={`${testId}-option-${f.key}`}>
              {f.label}
            </FilterOption>
          ))
        )}
      </PopoverContent>
    </Popover>
  )
}
