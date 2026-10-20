import { Link } from 'react-router-dom'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { PageHeader } from '@/components/layout/PageHeader'
import { Markdown } from '@/components/markdown/Markdown'
import type { DocPageMeta, DocSection } from '@/types/productdocs'
import { docsCardClassName } from './docs-card'
import { useDocsSet } from './docs-set'
import { usePageTitle } from '@/hooks/usePageTitle'

export type DocsCard = { to: string; title: string; summary: string; soon?: boolean }

// A grid of link cards: docs pages on a section or home page, endpoint groups
// on the API docs home.
export function DocsCards({ cards }: { cards: DocsCard[] }) {
  return (
    <ul className="grid gap-3 sm:grid-cols-2">
      {cards.map(card => (
        <li key={card.to}>
          <Link to={card.to} className={docsCardClassName}>
            <span className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-100">
              {card.title}
              {card.soon && (
                <span className="text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-600">Soon</span>
              )}
            </span>
            <span className="mt-1 block text-sm text-gray-500 dark:text-gray-400">{card.summary}</span>
          </Link>
        </li>
      ))}
    </ul>
  )
}

function PageCards({ pages, routeBase }: { pages: DocPageMeta[]; routeBase: string }) {
  return (
    <DocsCards
      cards={pages.map(p => ({ to: `${routeBase}/${p.slug}`, title: p.title, summary: p.summary, soon: p.stub }))}
    />
  )
}

// Landing page for one section: the breadcrumb names it (no h1 — that is the
// convention for a titled entity), then its intro and page cards.
export function DocsSectionIndex({ section, routeBase }: { section: DocSection; routeBase: string }) {
  const { label } = useDocsSet()
  usePageTitle(section.title, label)
  return (
    <div className="max-w-3xl" data-testid="docs-section">
      <Breadcrumbs items={[{ label, to: routeBase || '/' }, { label: section.title }]} />
      <p className="text-base text-gray-500 dark:text-gray-400">{section.summary}</p>
      {section.body && (
        <Markdown variant="docs" className="mt-2">
          {section.body}
        </Markdown>
      )}
      <div className="mt-6">
        <PageCards pages={section.pages} routeBase={routeBase} />
      </div>
    </div>
  )
}

/** The id of a section heading on the docs home, the target of "On this page". */
export const docsSectionId = (slug: string) => `docs-section-${slug}`

// A set's root: every section the caller may see, with its pages.
export function DocsHome({ tree, routeBase }: { tree: DocSection[]; routeBase: string }) {
  const { homeHeader } = useDocsSet()
  return (
    <div data-testid="docs-home">
      <PageHeader title={homeHeader.title} subtitle={homeHeader.subtitle} />
      <div className="space-y-10">
        {tree.map(section => (
          <section key={section.slug} aria-labelledby={docsSectionId(section.slug)}>
            <div className="mb-3 flex items-baseline justify-between gap-4">
              <h2 id={docsSectionId(section.slug)} className="text-lg font-medium text-gray-900 dark:text-gray-100">
                <Link to={`${routeBase}/${section.slug}`} className="hover:underline">{section.title}</Link>
              </h2>
              <span className="text-sm text-gray-500 dark:text-gray-400">{section.summary}</span>
            </div>
            <PageCards pages={section.pages} routeBase={routeBase} />
          </section>
        ))}
      </div>
    </div>
  )
}
