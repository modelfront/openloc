// The "add a filter" button for a bar whose chips show only filters in use.
// One popover, two steps: the list of filters not on the bar, then — right
// away, in the same popover — the picked filter's options. Choosing an option
// sets the filter, which is what puts its chip on the bar; closing without one
// adds nothing. The options are the same render function the filter's
// FilterChip uses, so the two always offer the same choices.

import { useState, type ReactNode } from 'react'
import { ListFilter } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { FilterOption } from './FilterChip'

export interface AddableFilter {
  key: string
  label: string
  /** The filter's option rows; call `close` after a pick. */
  options: (close: () => void) => ReactNode
}

export function AddFilterMenu({ filters, testId }: { filters: AddableFilter[]; testId: string }) {
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
      <PopoverTrigger
        render={<Button variant="outline" size="icon-sm" aria-label="Add a filter" title="Add a filter" data-testid={testId} />}
      >
        <ListFilter className="size-3.5" />
      </PopoverTrigger>
      <PopoverContent className="w-56 p-1 max-h-72 overflow-y-auto" align="start" data-testid={`${testId}-popup`}>
        {picked ? (
          <>
            <div className="px-2 pt-1 pb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">{picked.label}</div>
            {picked.options(() => onOpenChange(false))}
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
