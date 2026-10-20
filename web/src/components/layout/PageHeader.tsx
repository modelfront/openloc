import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { usePageTitle } from '@/hooks/usePageTitle'

type HeadingLevel = 'h1' | 'h2' | 'h3'

type Props = {
  /** Displayed as the heading text. */
  title: ReactNode
  /**
   * Optional subtitle rendered below the title in smaller muted text.
   * Used on detail pages (e.g., project name → language pair summary).
   */
  subtitle?: ReactNode
  /**
   * Right-aligned action slot (buttons, links, dropdowns).
   * Callers keep full control over colors so brand accents (emerald TM, rose QA, violet terminology) stay opt-in.
   */
  actions?: ReactNode
  /** Extra classes merged with the base `flex items-center justify-between mb-6` shell. */
  className?: string
  /**
   * Heading level. Defaults to `h1` for page-level usage. Set to `h2` or `h3`
   * when the component is used inside a tabbed layout or sub-section so the
   * document outline stays semantically correct (and screen readers don't see
   * multiple `h1`s on the same page).
   */
  as?: HeadingLevel
  /**
   * Browser tab title. Defaults to `title` when it is a string on an `h1`;
   * set it when `title` is JSX. Sub-section headers (`h2`/`h3`) never set it.
   */
  documentTitle?: string
}

/**
 * Standard list/detail page header.
 *
 * Keeps page-heading typography consistent without changing each page's
 * vertical rhythm.
 */
export function PageHeader({ title, subtitle, actions, className, as: Heading = 'h1', documentTitle }: Props) {
  usePageTitle(Heading === 'h1' ? documentTitle ?? (typeof title === 'string' ? title : undefined) : undefined)
  return (
    <div className={cn('flex items-center justify-between mb-6', className)}>
      <div>
        <Heading className="font-display text-2xl font-normal text-gray-900 dark:text-gray-100">{title}</Heading>
        {subtitle && (
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{subtitle}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
