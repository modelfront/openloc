import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { SlimCollapsibleSection } from './SlimCollapsibleSection'

/** How a shared list section presents itself: a toggling row or a plain title. */
export type SectionLayout = 'collapsible' | 'static'

/**
 * A static section title: heading on the left with an optional leading icon
 * and info icon, an optional action (a create button) on the right, nothing
 * underneath. For a tab that holds one list — there is nothing to collapse and
 * no second section to separate from — and the title of a `SettingsCard`.
 * `SlimCollapsibleSection` stays the toggling form.
 */
export function SectionHeader({ title, icon, info, chip, action, className, 'data-testid': testId }: {
  title: string
  /** Leading icon left of the title. A lucide icon is sized by the header and needs no className. */
  icon?: ReactNode
  /** Element right of the title (e.g. a SectionInfo tooltip). */
  info?: ReactNode
  /** Element after the info icon (e.g. an "Advanced" chip). */
  chip?: ReactNode
  /** Right-aligned action, typically the section's create button. */
  action?: ReactNode
  /** Spacing against the body below (replaces the default `mb-3`). Layout classes only. */
  className?: string
  'data-testid'?: string
}) {
  return (
    <div className={cn('mb-3 flex items-center justify-between gap-2', className)} data-testid={testId}>
      <div className="flex items-center gap-2 min-w-0">
        {icon != null && <span className="shrink-0 flex text-gray-500 dark:text-gray-400 [&_svg]:size-5" aria-hidden="true">{icon}</span>}
        <h2 className="text-lg font-medium text-gray-900 dark:text-gray-100 truncate">{title}</h2>
        {info}
        {chip}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

/**
 * A titled section holding one list, in whichever layout the page asked for:
 * `static` puts a `SectionHeader` above the body, `collapsible` hands the whole
 * thing to `SlimCollapsibleSection`. The header slots (`title`, `info`, `chip`)
 * mean the same in both, so a section describes itself once and the layout is
 * the page's choice. Omit `title` for a tab whose own name says what the list
 * is: the section then has no header and the action sits above the body.
 */
export function ListSection({
  layout = 'collapsible', title, info, chip, action, defaultOpen, className, 'data-testid': testId, children,
}: {
  /** Ignored without a `title` — a headerless section has nothing to toggle. */
  layout?: SectionLayout
  /** Omitted renders the section headerless, with the action above the body. */
  title?: string
  /** Element right of the title (e.g. a SectionInfo tooltip). */
  info?: ReactNode
  /** Element after the info icon (e.g. an "Advanced" chip). */
  chip?: ReactNode
  /**
   * Right-aligned header action, typically the section's create button. Static
   * layout only: a collapsible section's create affordance is an `AddItemButton`
   * at the top of its body, so a header action there would be a second one.
   */
  action?: ReactNode
  /** Collapsible layout only: start expanded. */
  defaultOpen?: boolean
  /** Static layout only: spacing against the section above. Layout classes only. */
  className?: string
  /** Marks the section in both layouts, open or closed. */
  'data-testid'?: string
  children: ReactNode
}) {
  if (!title) {
    return (
      <section className={className} data-testid={testId}>
        {action && <div className="mb-4">{action}</div>}
        {children}
      </section>
    )
  }
  if (layout === 'collapsible') {
    return (
      <SlimCollapsibleSection title={title} info={info} chip={chip} defaultOpen={defaultOpen} data-testid={testId}>
        {children}
      </SlimCollapsibleSection>
    )
  }
  return (
    <section className={className} data-testid={testId}>
      <SectionHeader title={title} info={info} chip={chip} action={action} />
      {children}
    </section>
  )
}
