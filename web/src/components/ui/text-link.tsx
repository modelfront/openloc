// An inline link: a colour and an underline on hover, nothing else. It takes
// the size and weight of the text it sits in, so a link in a caption stays
// caption-sized and one in body text stays body-sized. Three forms: `to` for
// a route (router Link), `href` for an outside URL or a download (anchor),
// `onClick` for an action that only looks like a link (button). `newTab`
// opens a route or URL in a new tab and says so with an icon, unless
// `newTabIcon={false}` (a name that is plainly a link to its own page).
import type { AnchorHTMLAttributes, ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router-dom'
import { ExternalLink } from 'lucide-react'
import { cn } from '@/lib/utils'
import { NEW_TAB_ATTRS } from '@/lib/new-tab'
import { chipTextClass, type ChipAnyColor } from '@/components/ui/chip'

/** `primary` is the link colour. `muted` is grey, for a link that must not
 *  compete with the page ("Forgot password?"). `inherit` keeps the colour of
 *  the text around it (a link inside the session banner). A chip colour is
 *  for an annotation that belongs to a chip, such as a step's router. */
export type TextLinkTone = 'primary' | 'muted' | 'inherit' | ChipAnyColor

interface Look {
  tone?: TextLinkTone
  /** Underlined at rest, for a link inside running text that must read as a
   *  link by shape (rendered markdown, the session banner). */
  underlined?: boolean
}

const BASE =
  'underline-offset-4 rounded-sm focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50'

function toneClass(tone: TextLinkTone = 'primary'): string {
  if (tone === 'primary') return 'text-primary'
  if (tone === 'muted') return 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
  if (tone === 'inherit') return 'text-current'
  return chipTextClass(tone)
}

/** The look as a class string, for the one renderer that cannot use the
 *  component (Markdown). */
export function textLinkClass({ tone, underlined }: Look = {}): string {
  return cn(BASE, toneClass(tone), underlined ? 'underline' : 'hover:underline')
}

interface Common extends Look {
  className?: string
  children: ReactNode
}

/** Opens in a new tab, with the external-link icon after the text. The text
 *  truncates before the icon does, so the link fits a narrow table cell. For
 *  a link out of a form that holds unsaved input. `newTabIcon={false}` drops
 *  the icon, for a name in a table row, where icons would crowd the column;
 *  screen readers still hear "(opens in a new tab)". */
interface NewTab {
  newTab?: boolean
  newTabIcon?: boolean
}

type RouteProps = Common & NewTab & { to: LinkProps['to']; href?: undefined } & Omit<LinkProps, 'to' | 'className' | 'children'>
type AnchorProps = Common & NewTab & { href: string; to?: undefined } & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href' | 'className' | 'children'>
type ActionProps = Common & { to?: undefined; href?: undefined; newTab?: undefined; newTabIcon?: undefined; onClick: () => void } &
  Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'children' | 'type' | 'onClick'>

function newTabChildren(children: ReactNode, icon: boolean): ReactNode {
  return (
    <>
      <span className="min-w-0 truncate">{children}</span>
      {icon && <ExternalLink className="size-3 shrink-0" aria-hidden />}
      {/* A flex container drops this space from layout; the accessible name keeps it. */}
      {' '}
      <span className="sr-only">(opens in a new tab)</span>
    </>
  )
}

export function TextLink(props: RouteProps | AnchorProps | ActionProps) {
  const { tone, underlined, className, newTab, newTabIcon = true, children, ...rest } = props
  const cls = cn(textLinkClass({ tone, underlined }), newTab && 'inline-flex max-w-full items-center gap-1', className)
  const body = newTab ? newTabChildren(children, newTabIcon) : children
  if (rest.to !== undefined) {
    const { to, ...link } = rest as Omit<RouteProps, 'tone' | 'underlined' | 'className' | 'newTab' | 'newTabIcon' | 'children'>
    return <Link to={to} className={cls} {...(newTab ? NEW_TAB_ATTRS : {})} {...link}>{body}</Link>
  }
  if (rest.href !== undefined) {
    const { href, ...anchor } = rest as Omit<AnchorProps, 'tone' | 'underlined' | 'className' | 'newTab' | 'newTabIcon' | 'children'>
    return <a href={href} className={cls} {...(newTab ? NEW_TAB_ATTRS : {})} {...anchor}>{body}</a>
  }
  const action = rest as Omit<ActionProps, 'tone' | 'underlined' | 'className' | 'to' | 'href' | 'newTab' | 'newTabIcon' | 'children'>
  // eslint-disable-next-line no-restricted-syntax -- the one sanctioned action styled as a link
  return <button type="button" className={cn('cursor-pointer', cls)} {...action}>{body}</button>
}
