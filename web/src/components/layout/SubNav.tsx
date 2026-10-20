import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

export interface SubNavItem {
  to: string
  label: string
  /** Match only the exact path (for the section's index route). */
  end?: boolean
  /** Shown but not reachable, e.g. a tab with nothing to show yet. */
  disabled?: boolean
  /** Why it is disabled — the tooltip on a disabled item. */
  disabledReason?: string
}

const itemClass = '-mb-px max-md:shrink-0 max-md:whitespace-nowrap border-b-2 px-3 py-2 text-sm transition-colors'

/**
 * Horizontal secondary navigation under a PageHeader for a section that owns
 * several sibling pages (Workflows | Steps). The URL is the state — each item
 * is a route, so deep links and back/forward work without extra wiring.
 * Styled like the in-page Tabs list so the two read as the same control.
 *
 * A disabled item renders without a link. With a `disabledReason` it takes
 * focus, so the tooltip saying why is reachable from the keyboard too.
 *
 * `actions` sit at the end of the row, for a control that belongs to the
 * section rather than to the page header.
 */
export function SubNav({
  items,
  className,
  dataGuide,
  actions,
}: {
  items: SubNavItem[]
  className?: string
  dataGuide?: string
  actions?: ReactNode
}) {
  return (
    <nav
      aria-label="Section"
      data-guide={dataGuide}
      className={cn('mb-6 flex gap-1 max-md:overflow-x-auto max-md:overflow-y-hidden border-b border-gray-200 dark:border-gray-800', className)}
    >
      {items.map(item => item.disabled ? (
        <DisabledItem key={item.to} item={item} />
      ) : (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          className={({ isActive }) =>
            cn(
              itemClass,
              isActive
                ? 'border-primary text-gray-900 dark:text-gray-100 font-medium'
                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100',
            )
          }
        >
          {item.label}
        </NavLink>
      ))}
      {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
    </nav>
  )
}

function DisabledItem({ item }: { item: SubNavItem }) {
  const label = (
    <span
      role="link"
      aria-disabled="true"
      tabIndex={item.disabledReason ? 0 : undefined}
      className={cn(itemClass, 'cursor-not-allowed border-transparent text-gray-300 outline-none focus-visible:text-gray-500 dark:text-gray-600')}
      data-testid={`subnav-disabled-${item.label}`}
    >
      {item.label}
    </span>
  )
  if (!item.disabledReason) return label
  return (
    <Tooltip>
      <TooltipTrigger render={label} />
      <TooltipContent>{item.disabledReason}</TooltipContent>
    </Tooltip>
  )
}
