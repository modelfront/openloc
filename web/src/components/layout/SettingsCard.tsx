import { useId, type ReactNode, type Ref } from 'react'
import { cn } from '@/lib/utils'
import { slugifyHeading } from '@/lib/markdown-headings'

/**
 * One section of a settings page (personal Settings, the Organization pages):
 * a slim header row with icon, title, a one-line `summary` of the current
 * state and the `action` buttons; then an optional `toolbar` strip (search,
 * filters, a mode switch), the description and the body. Sections are always
 * open.
 *
 * The section's `id` (the slug of the title by default) is its anchor, so
 * `#members` scrolls to it. Renders a `<section>` whose `h2` names it, so a
 * test finds it by heading.
 *
 * `flush` drops the body's padding, so a table runs from wall to wall right
 * under the header or the toolbar; the body then pads its own non-table parts.
 *
 * `ref` reaches the `<section>`, so a link elsewhere can point at it
 * (`useAttentionTarget`).
 *
 * No outer margin: the page spaces its sections (`space-y-5` on a stack of
 * them, a grid's gap in a multi-column editor), so a gap is never doubled.
 */
export function SettingsCard({
  title, id, icon, info, action, summary, toolbar, description, flush = false,
  className, 'data-testid': testId, 'data-guide': guide, ref, children,
}: {
  title: string
  /** Anchor; defaults to the slug of the title. Keep it stable. */
  id?: string
  /** Leading icon left of the title (a lucide icon, sized by the row). */
  icon?: ReactNode
  /** Element right of the title (a SectionInfo tooltip, a count). */
  info?: ReactNode
  /** Right-aligned header buttons. */
  action?: ReactNode
  /** One line on the section's current state, next to the title. */
  summary?: ReactNode
  /** Compact strip of filters or inputs under the header. */
  toolbar?: ReactNode
  /** Muted line at the top of the body saying what the section controls. */
  description?: ReactNode
  /** No body padding: a table fills the section edge to edge. */
  flush?: boolean
  /** Layout classes only (grid placement, sticky); the page spaces sections. */
  className?: string
  'data-testid'?: string
  /** Anchor for the in-app guide tour. */
  'data-guide'?: string
  ref?: Ref<HTMLElement>
  children: ReactNode
}) {
  const key = id ?? slugifyHeading(title)
  const uid = useId()

  return (
    <section
      ref={ref}
      id={key}
      aria-labelledby={`${uid}-title`}
      className={cn('scroll-mt-4 rounded-lg border border-gray-200 bg-card dark:border-gray-800', className)}
      data-testid={testId}
      data-guide={guide}
    >
      <div className="flex min-h-11 items-center gap-x-2.5 px-3 py-1.5 max-md:flex-wrap">
        {icon != null && (
          <span className="flex shrink-0 text-gray-500 dark:text-gray-400 [&_svg]:size-4" aria-hidden="true">{icon}</span>
        )}
        <h2 id={`${uid}-title`} className="shrink-0 text-sm font-medium text-gray-900 dark:text-gray-100">{title}</h2>
        {info != null && <span className="shrink-0">{info}</span>}
        {summary != null && (
          <span
            className={cn(
              'min-w-0 flex-1 truncate text-sm text-gray-500 dark:text-gray-400',
              "before:mr-2.5 before:text-gray-300 before:content-['·'] dark:before:text-gray-600",
              // A phone gives the summary its own line under the title, in full.
              'max-md:order-last max-md:basis-full max-md:whitespace-normal max-md:before:content-none',
              icon != null && 'max-md:pl-6.5',
            )}
            data-testid={testId ? `${testId}-summary` : undefined}
          >
            {summary}
          </span>
        )}
        {action != null && (
          <span className={cn('ml-auto flex shrink-0 items-center gap-2', summary == null && 'flex-1 justify-end')}>
            {action}
          </span>
        )}
      </div>
      {toolbar != null && (
        <div className="flex flex-wrap items-center gap-2 border-y border-gray-100 bg-muted/40 px-3 py-1.5 dark:border-gray-800">
          {toolbar}
        </div>
      )}
      <div className={cn(
        !flush && 'px-3 pb-3',
        !flush && toolbar != null && 'pt-3',
        toolbar == null && 'border-t border-gray-100 dark:border-gray-800',
        !flush && toolbar == null && 'pt-3',
      )}>
        {description != null && (
          <p className={cn('text-xs text-gray-500 dark:text-gray-400', flush ? 'px-3 py-2' : 'mb-3')}>{description}</p>
        )}
        {children}
      </div>
    </section>
  )
}
