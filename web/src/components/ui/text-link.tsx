// An inline link: a colour and an underline on hover, nothing else. It takes
// the size and weight of the text it sits in, so a link in a caption stays
// caption-sized and one in body text stays body-sized. Three forms: `to` for
// a route (router Link), `href` for an outside URL or a download (anchor),
// `onClick` for an action that only looks like a link (button).
import type { AnchorHTMLAttributes, ButtonHTMLAttributes, ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router-dom'
import { cn } from '@/lib/utils'
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

type RouteProps = Common & { to: LinkProps['to']; href?: undefined } & Omit<LinkProps, 'to' | 'className' | 'children'>
type AnchorProps = Common & { href: string; to?: undefined } & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href' | 'className' | 'children'>
type ActionProps = Common & { to?: undefined; href?: undefined; onClick: () => void } &
  Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'children' | 'type' | 'onClick'>

export function TextLink(props: RouteProps | AnchorProps | ActionProps) {
  const { tone, underlined, className, ...rest } = props
  const cls = cn(textLinkClass({ tone, underlined }), className)
  if (rest.to !== undefined) {
    const { to, ...link } = rest as Omit<RouteProps, 'tone' | 'underlined' | 'className'>
    return <Link to={to} className={cls} {...link} />
  }
  if (rest.href !== undefined) {
    const { href, ...anchor } = rest as Omit<AnchorProps, 'tone' | 'underlined' | 'className'>
    return <a href={href} className={cls} {...anchor} />
  }
  const action = rest as Omit<ActionProps, 'tone' | 'underlined' | 'className' | 'to' | 'href'>
  // eslint-disable-next-line no-restricted-syntax -- the one sanctioned action styled as a link
  return <button type="button" className={cn('cursor-pointer', cls)} {...action} />
}
