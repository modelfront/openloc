import { useId, type ReactNode } from 'react'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { cn } from '@/lib/utils'

interface Props {
  /** The section's name in the header. */
  title: ReactNode
  open: boolean
  onToggle: () => void
  /** Muted trailing text inside the toggle, pushed right: a count, an engine
   *  name. Falsy renders nothing, so a zero count stays out of the header. */
  meta?: ReactNode
  /** A control after the toggle, at the header's right edge. It sits outside
   *  the toggle, so clicking it does not open or close the section. */
  action?: ReactNode
  /** Extra classes for the body, merged over its default padding. */
  bodyClassName?: string
  /** Marks the section, open or closed. */
  'data-testid': string
  children: ReactNode
}

/**
 * One collapsible section of the CAT editor's Resources sidebar: a full-width
 * disclosure header and a body rendered only while open. The header carries
 * `aria-expanded` and points `aria-controls` at the body. Sections carry no
 * border of their own — the sidebar's `divide-y` draws the separators.
 */
export function EditorSidebarSection({ title, open, onToggle, meta, action, bodyClassName, 'data-testid': testId, children }: Props) {
  const bodyId = useId()
  return (
    <div data-testid={testId}>
      <div className="flex items-center hover:bg-muted">
        <DisclosureToggle open={open} onToggle={onToggle} controls={bodyId} className="flex-1 min-w-0 px-3 py-1.5">
          {title}
          {meta ? <span className="ml-auto min-w-0 truncate text-gray-400">{meta}</span> : null}
        </DisclosureToggle>
        {action}
      </div>
      {open && (
        <div id={bodyId} className={cn('px-2 pt-1 pb-2', bodyClassName)}>
          {children}
        </div>
      )}
    </div>
  )
}
