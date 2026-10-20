import { NavLink } from 'react-router-dom'
import { Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { isMac } from '@/lib/platform'
import type { DocSection } from '@/types/productdocs'
import { useDocsSet } from './docs-set'
import { docsNavLinkClassName, docsNavSectionClassName } from './docs-card'


type Props = {
  tree: DocSection[]
  routeBase: string
  onOpenSearch: () => void
  /** More groups after the sections. */
  after?: React.ReactNode
}

// Left column of the docs page: a search trigger, then every section the
// caller's role may see with its pages. The tree is already role-filtered by
// the server, so nothing is hidden client-side.
export function DocsSidebar({ tree, routeBase, onOpenSearch, after }: Props) {
  const { copy } = useDocsSet()
  return (
    <div className="flex flex-col gap-5">
      {/* eslint-disable-next-line no-restricted-syntax -- search launcher (opens the palette), not an action button */}
      <button
        type="button"
        onClick={onOpenSearch}
        className="flex items-center gap-2 rounded-md border border-gray-200 bg-white px-2.5 py-1.5 text-sm text-gray-500 transition-colors hover:border-gray-300 hover:text-gray-700 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400 dark:hover:border-gray-700 dark:hover:text-gray-200"
        data-testid="docs-search-trigger"
      >
        <Search className="h-4 w-4 shrink-0" />
        <span className="flex-1 text-left">{copy.searchTitle}</span>
        <kbd className="rounded border border-gray-200 px-1 font-sans text-[10px] text-gray-400 dark:border-gray-700 dark:text-gray-500">
          {isMac ? '⌘K' : 'Ctrl+K'}
        </kbd>
      </button>
      {tree.map(section => (
        <div key={section.slug}>
          <NavLink
            to={`${routeBase}/${section.slug}`}
            end
            className={({ isActive }) => docsNavSectionClassName(isActive)}
          >
            {section.title}
          </NavLink>
          <ul className="space-y-0.5">
            {section.pages.map(page => (
              <li key={page.slug}>
                <NavLink
                  to={`${routeBase}/${page.slug}`}
                  end
                  className={({ isActive }) => cn(docsNavLinkClassName(isActive), 'flex items-center gap-2')}
                >
                  <span className="truncate">{page.title}</span>
                  {page.stub && (
                    <span className="ml-auto shrink-0 text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-600">Soon</span>
                  )}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {after}
    </div>
  )
}
