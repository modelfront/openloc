import type { ReactNode } from 'react'
import { TextLink } from '@/components/ui/text-link'

/**
 * The strip above the header while the session is not the user's own org:
 * an operator viewing an org, or a visit to the demo organization. One style
 * for both, so it always reads as "you are somewhere else".
 */
export function SessionBanner({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div
      role="status"
      data-testid="session-banner"
      className="shrink-0 flex items-center justify-center gap-3 px-4 py-1.5 text-xs font-medium bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200 border-b border-amber-200 dark:border-amber-800"
    >
      <span>{children}</span>
      {action}
    </div>
  )
}

/** The banner's action: a link, or a button styled as one. */
export function SessionBannerAction({ onClick, href, children }: { onClick?: () => void; href?: string; children: ReactNode }) {
  if (href) return <TextLink href={href} tone="inherit" underlined>{children}</TextLink>
  return <TextLink tone="inherit" underlined onClick={() => onClick?.()}>{children}</TextLink>
}
