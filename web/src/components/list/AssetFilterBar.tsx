// AssetFilterBar — the filter row of the lists fetched whole (TMs, TM profiles,
// term bases): a name search box, then the optional "Created by" chip and
// "Clear filters".

import { Search } from 'lucide-react'
import { Input } from '@/components/ui/input'
import type { CreatorRef } from '../../lib/creators'
import { CreatedByFilterChip } from './CreatedByFilterChip'
import { ClearFiltersButton } from './ClearFiltersButton'

interface CreatedByProps {
  value: number | null
  onChange: (id: number | null) => void
  myUserId: number | null
  options: CreatorRef[]
  testId: string
}

interface Props {
  /** Rows in the list before filtering. */
  total: number
  search: string
  onSearchChange: (q: string) => void
  placeholder: string
  /** Lands on the search `<input>`. */
  searchTestId: string
  /** Adds the "Created by" chip. */
  createdBy?: CreatedByProps
  /** Adds "Clear filters": the bar empties the search box, then calls this to clear the other filters and the URL. */
  onClear?: () => void
  clearTestId?: string
  testId?: string
}

export function AssetFilterBar({ total, search, onSearchChange, placeholder, searchTestId, createdBy, onClear, clearTestId, testId }: Props) {
  const activeCount = (search ? 1 : 0) + (createdBy?.value != null ? 1 : 0)
  // An empty list with no filter shows only its empty state.
  if (total === 0 && activeCount === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-2 mb-4" data-testid={testId}>
      <Input
        placeholder={placeholder}
        value={search}
        onChange={e => onSearchChange(e.target.value)}
        icon={<Search />}
        // A fixed width, not w-full: inside a parent that is only as wide as
        // its content, a full-width box pushes the chip onto a second line.
        className="text-sm w-80 max-w-full"
        data-testid={searchTestId}
      />
      {/* The chip shows when there's something to distinguish (2+ creators),
          or when a filter is already active, so a URL-set filter can always be
          cleared from the UI. */}
      {createdBy && (createdBy.options.length > 1 || createdBy.value != null) && (
        <CreatedByFilterChip
          size="md"
          value={createdBy.value}
          myUserId={createdBy.myUserId}
          options={createdBy.options}
          onChange={createdBy.onChange}
          testId={createdBy.testId}
        />
      )}
      {onClear && (
        <ClearFiltersButton
          count={activeCount}
          // A search typed in the last moments may not be in the URL yet, so
          // clearing the URL alone would let it come back.
          onClear={() => { onSearchChange(''); onClear() }}
          testId={clearTestId ?? 'filter-clear-all'}
        />
      )}
    </div>
  )
}
