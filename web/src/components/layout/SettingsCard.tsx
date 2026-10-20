import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { SectionHeader } from './SectionHeader'

/**
 * One titled card on a settings page (personal Settings, the Organization
 * pages): the card surface, a `SectionHeader`, an optional muted description
 * line, then the body. Renders a `<section>`, so a test can find the card by
 * its heading. The header slots (`icon`, `info`, `action`) mean the same as on
 * `SectionHeader`.
 */
export function SettingsCard({
  title, icon, info, action, description, className, 'data-testid': testId, 'data-guide': guide, children,
}: {
  title: string
  /** Leading icon left of the title (a lucide icon, sized by the header). */
  icon?: ReactNode
  /** Element right of the title (a SectionInfo tooltip, a count). */
  info?: ReactNode
  /** Right-aligned header action. */
  action?: ReactNode
  /** Muted line under the title saying what the card controls. */
  description?: ReactNode
  /** Spacing against the neighbouring cards (`mb-6`, `mt-6`). Layout classes only. */
  className?: string
  'data-testid'?: string
  /** Anchor for the in-app guide tour. */
  'data-guide'?: string
  children: ReactNode
}) {
  return (
    <section
      className={cn('bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-6', className)}
      data-testid={testId}
      data-guide={guide}
    >
      <SectionHeader
        title={title}
        icon={icon}
        info={info}
        action={action}
        className={description != null ? 'mb-1' : 'mb-4'}
      />
      {description != null && (
        <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">{description}</p>
      )}
      {children}
    </section>
  )
}
