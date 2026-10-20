import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

type BreadcrumbItem = {
  label: ReactNode
  to?: string
  /** An ancestor whose label is a user-supplied name (an `EditableTitle`)
   *  may truncate too. Fixed ancestors like "Projects" keep their width. */
  shrink?: boolean
}

/** `trailing` sits after the current crumb — chips that qualify the page
 *  (the CAT editor: the target language, the current step and its deadline). `actions`
 *  sit at the right end of the same line, outside the <nav> — they are page
 *  controls, not crumbs. */
export function Breadcrumbs({ items, trailing, actions }: { items: BreadcrumbItem[]; trailing?: ReactNode; actions?: ReactNode }) {
  const nav = (
    <nav aria-label="Breadcrumb" className={`${actions ? 'min-w-0' : 'mb-4'} text-sm text-gray-500 dark:text-gray-400`}>
      <ol className="flex items-center gap-2 max-md:flex-wrap">
        {items.map((item, index) => {
          const current = index === items.length - 1
          return (
            // The trailing crumb (the entity's own name) and ancestors marked
            // `shrink` give up width; fixed ancestors keep theirs so a long
            // name can't squeeze them into each other.
            <li key={index} className={`flex items-center gap-2 ${current || item.shrink ? 'min-w-0' : 'shrink-0'}`}>
              {index > 0 && <span aria-hidden="true">/</span>}
              {item.to ? (
                // flex min-w-0: a truncating label inside the link is
                // constrained by the crumb, same as the span below.
                <Link to={item.to} className="flex min-w-0 items-center hover:text-primary">
                  {item.label}
                </Link>
              ) : (
                // flex, not inline: a label that truncates needs its wrapper to
                // be a flex container, or the inner box sizes to content and
                // spills out of the crumb.
                <span aria-current={current ? 'page' : undefined} className="flex min-w-0 items-center text-gray-900 dark:text-gray-100">
                  {item.label}
                </span>
              )}
            </li>
          )
        })}
        {trailing && <li className="flex shrink-0 items-center gap-1.5">{trailing}</li>}
      </ol>
    </nav>
  )
  if (!actions) return nav
  return (
    <div className="mb-4 flex items-center justify-between gap-3 max-md:flex-wrap">
      {nav}
      <div className="flex shrink-0 items-center gap-2">{actions}</div>
    </div>
  )
}
