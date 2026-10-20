import type { ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { rowButtonProps } from '@/components/list/row-button'
import { buttonVariants } from '@/components/ui/button'
import { chipSurfaceClass, chipSurfaceHoverClass, type ChipColor } from '@/components/ui/chip'
import { useCollapsibleOpen } from '@/hooks/useCollapsibleOpen'

interface Props {
  title: string
  /** Optional action (e.g. an "Add" button) shown on the right of the header. */
  action?: ReactNode
  /** Optional element left of the title (e.g. an "Advanced" chip). */
  chip?: ReactNode
  /** Optional element right of the title (e.g. an info icon). */
  info?: ReactNode
  /** Section blurb, shown at the top of the expanded body. */
  description?: ReactNode
  defaultOpen?: boolean
  /**
   * `default` is a page section (18px title, 12px padding). `compact` is a row
   * among many — the Tasks tab's step sections: 14px title, 6px padding.
   */
  size?: 'default' | 'compact'
  /**
   * What the section holds, e.g. "3 tasks". Shown with the chevron as an outline button —
   * "Show 3 tasks" / "Hide 3 tasks" — for a list of sections where a closed one
   * has to read as something to open. A disabled section shows it as is.
   */
  toggleLabel?: string
  /**
   * `plain` puts the body under the header's hairline, with a gap. `grouped`
   * sections, rendered as siblings, read as cards: closed rows touch with a
   * line between each, and an open one keeps its body inside and ends its card
   * under it, the rows after it starting the next — for a list of sections
   * whose bodies are tables. The card's edges are the column's: no bleed.
   */
  variant?: 'plain' | 'grouped'
  /** A state colour for the header's background, as on a task row. */
  tone?: ChipColor
  /** Nothing to open: the header is a muted row with no chevron, and no body. */
  disabled?: boolean
  /** Auto-open the section once when this turns true. */
  openWhen?: boolean
  /**
   * Marks the SECTION, open or closed. Put it here rather than on something
   * inside the body: a testid in the body disappears when the section is
   * collapsed, so any test using it as "is this section present?" silently
   * becomes "is it expanded?".
   */
  'data-testid'?: string
  children: ReactNode
}

const FRAME_BORDER = 'border-gray-200 dark:border-gray-800'

/**
 * A titled, collapsible section: the header toggles the body, `chip`/`info`/
 * `action` sit OUTSIDE the toggle so their own tooltips and clicks don't fire
 * it, and open state comes from `useCollapsibleOpen` (not persisted; `openWhen`
 * auto-opens once).
 *
 * The `plain` variant is chrome-less — no border, no `bg-card`, no rounded
 * corners. Sections sit directly on the page background, so a page of them
 * reads as one surface with the list rows providing the only fill, rather than
 * cards inside cards. `grouped` sections stack as cards. Add variants here
 * rather than a second component.
 */
export function SlimCollapsibleSection({
  title, action, chip, info, description, defaultOpen = false, openWhen, size = 'default',
  toggleLabel, variant = 'plain', tone, disabled = false, children, 'data-testid': testId,
}: Props) {
  const { open: openState, toggle } = useCollapsibleOpen(defaultOpen, openWhen)
  const open = openState && !disabled
  const compact = size === 'compact'
  const grouped = variant === 'grouped'

  return (
    <section
      className={grouped
        ? cn(
          // Sides and a line on top: the line between two rows, or the top of
          // a card. `overflow-hidden` clips a coloured header to the corners.
          'overflow-hidden border-x border-t bg-card', FRAME_BORDER,
          'first:rounded-t-lg last:rounded-b-lg last:border-b',
          // Open, the section ends its card under the body, and the row after
          // it starts a new one.
          open && 'mb-3 rounded-b-lg border-b last:mb-0',
          '[[data-open]+&]:rounded-t-lg',
        )
        : compact ? 'mb-1' : 'mb-3'}
      data-open={grouped && open ? '' : undefined}
      data-testid={testId}
    >
      {/* The whole header toggles the section; the action (e.g. "Add") opts out
          via stopPropagation and only shows while open. The chevron trails at
          the far right, so the title is the leftmost thing on the row and
          section titles align with the page's left edge. */}
      {/* `-mx-3 px-3` lets the hover fill breathe 12px past the text on both sides
          without shifting the title — the negative margin cancels the padding,
          so the header stays flush with the rows below. */}
      <div
        className={cn(
          'flex items-center justify-between gap-2 px-3',
          compact ? 'py-1.5' : 'py-3',
          grouped
            // The group draws the lines between sections; an open header
            // draws the one above its body.
            ? cn(open && 'border-b', FRAME_BORDER)
            // `rounded-t-md` only: a bottom radius pulls the hairline's ends up
            // into little curves, which reads as a broken line rather than a
            // rule. The hairline gives the header a floor, so it reads as a row
            // of structure rather than text floating on the page.
            : cn('-mx-3 rounded-t-md border-b', FRAME_BORDER),
          // The header is tabbable (role=button + tabIndex), so it needs a
          // visible focus ring for keyboard users; inset inside a group, whose
          // frame clips anything outside it.
          !disabled && 'group cursor-pointer transition-colors outline-hidden focus-visible:ring-2 focus-visible:ring-ring/60',
          !disabled && grouped && 'focus-visible:ring-inset',
          tone ? [chipSurfaceClass(tone), !disabled && chipSurfaceHoverClass(tone)] : !disabled && 'hover:bg-muted/60',
        )}
        data-tone={tone}
        {...rowButtonProps(disabled ? undefined : toggle)}
        aria-expanded={disabled ? undefined : open}
      >
        <div className="flex items-center gap-2 min-w-0">
          <h2
            className={cn(
              'font-medium truncate',
              disabled ? 'text-gray-500 dark:text-gray-400' : 'text-gray-900 dark:text-gray-100',
              compact ? 'text-sm' : 'text-lg',
            )}
          >
            {title}
          </h2>
          {info}
          {chip}
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {open && action && (
            <span onClick={e => e.stopPropagation()}>{action}</span>
          )}
          {/* A trailing chevron points down while closed and up while open, like
              an accordion. */}
          {disabled ? (
            toggleLabel && <span className="text-xs text-gray-500 dark:text-gray-400">{toggleLabel}</span>
          ) : toggleLabel ? (
            // An outline button's look on a span: the header is already the
            // button, and a <button> inside it would nest one control in
            // another. It fills when the row is hovered, not just itself.
            <span
              className={cn(
                buttonVariants({ variant: 'outline', size: 'xs' }),
                '-my-0.5 group-hover:bg-muted group-hover:text-foreground dark:group-hover:bg-muted/50',
              )}
            >
              {open ? 'Hide' : 'Show'} {toggleLabel}
              <ChevronDown
                data-icon="inline-end"
                className={cn('size-3.5 transition-transform', open && 'rotate-180')}
                aria-hidden="true"
              />
            </span>
          ) : (
            // A small rounded box that fills on hover, so the row reads as a
            // control rather than a plain heading.
            <span
              className={cn(
                'flex items-center justify-center rounded-md p-0.5 transition-colors',
                'text-gray-500 dark:text-gray-400 group-hover:bg-muted group-hover:text-gray-900 dark:group-hover:text-gray-100',
              )}
              aria-hidden="true"
            >
              <ChevronDown className={cn('size-4 shrink-0 transition-transform', open && 'rotate-180')} />
            </span>
          )}
        </div>
      </div>
      {/* Body bleeds by the same `-mx-3` as the header's hover fill, so an open
          section's rows share their left/right edges with the highlight band —
          header and body read as one column. */}
      {open && (
        <div
          className={cn(
            !grouped && '-mx-3',
            // `pt-2` on top of the header's own `py-3`: without it the first
            // row sits right under the title and the two read as one block.
            !grouped && (compact ? 'pt-1.5 pb-3' : 'pt-2 pb-4'),
          )}
        >
          {description && (
            <p className={cn('text-sm text-gray-500 dark:text-gray-400', grouped ? 'px-3 py-2' : 'mb-3')}>{description}</p>
          )}
          {children}
        </div>
      )}
    </section>
  )
}
