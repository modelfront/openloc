// CreatedByFilterChip — "Created by" filter chip shared by the three asset
// lists (projects, TMs, term bases).
//
// Options are always the creators that actually own something in the list —
// never the org member list. A member who created nothing is a filter option
// that can only produce "no results", and /api/members is full of them
// (colleagues in other roles, leftover test accounts, …). Same principle as
// the Tasks toolbar deriving its Target/Assignee options from the tasks present.
//
// Where the caller gets that set differs by list, because pagination does:
//   TMs, term bases — derived from the rows on screen (lib/creators.ts); those
//                     lists are fetched whole.
//   projects        — GET /api/projects/creators, a DISTINCT over the tenant's
//                     projects. The list is cursor-paginated, so its rows can't
//                     see creators on later pages.
//
// Both sources include creators whose accounts are deactivated, which the
// member list would have dropped while their name stayed visible on the row.

import type { CreatorRef } from '../../lib/creators'
import { FilterChip, FilterOption } from './FilterChip'

interface Props {
  /** Currently filtered creator id, or null for "anyone". */
  value: number | null
  onChange: (id: number | null) => void
  /** Current user id — powers the "Me" shortcut. Null while `me` loads. */
  myUserId: number | null
  /**
   * Creators to offer. Empty is meaningful ("nothing here has a creator", or
   * the source is still loading): only "Anyone" is offered.
   */
  options: CreatorRef[]
  /** Chip height — see FilterChip's `size`. */
  size?: 'sm' | 'md'
  testId?: string
}

export function CreatedByFilterChip({
  value, onChange, myUserId, options, size = 'sm', testId = 'filter-created-by',
}: Props) {
  // "Me" is pinned above the rest, and only when the current user owns
  // something here — otherwise it's the same dead option this chip avoids.
  const meIsAChoice = myUserId != null && options.some(c => c.id === myUserId)

  const label = value == null
    ? null
    : value === myUserId
      ? 'Me'
      // `#id` covers a hand-typed URL, and the window before the option source
      // loads — better than the chip reading "All" while a filter is active.
      : options.find(c => c.id === value)?.name ?? `#${value}`

  return (
    <FilterChip
      size={size}
      label="Created by"
      value={label}
      testId={testId}
      render={(close) => (
        <>
          <FilterOption selected={value == null} onClick={() => { onChange(null); close() }}>
            Anyone
          </FilterOption>
          {meIsAChoice && (
            <FilterOption selected={value === myUserId} onClick={() => { onChange(myUserId!); close() }}>
              Me
            </FilterOption>
          )}
          {options
            // "Me" already covers the current user — don't list them twice.
            .filter(c => c.id !== myUserId)
            .map(c => (
              <FilterOption key={c.id} selected={value === c.id} onClick={() => { onChange(c.id); close() }}>
                {c.name}
              </FilterOption>
            ))}
        </>
      )}
    />
  )
}
