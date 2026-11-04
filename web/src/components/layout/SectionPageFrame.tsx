import type { ReactNode } from 'react'
import { PageContainer } from './PageContainer'
import { PageHeader } from './PageHeader'
import { SubNav, type SubNavItem } from './SubNav'

/**
 * A section whose tabs are sibling routes (MT, AI, Organization, File
 * configurations): the page header, a SubNav with one tab per route, then the
 * current tab. Without `tabs` there is no tab row.
 */
export function SectionPageFrame({ title, subtitle, actions, tabs, dataGuide, children }: {
  title: string
  subtitle?: ReactNode
  actions?: ReactNode
  tabs?: SubNavItem[]
  dataGuide?: string
  children: ReactNode
}) {
  return (
    <PageContainer>
      <PageHeader title={title} subtitle={subtitle} actions={actions} />
      {tabs && <SubNav className="mb-4" items={tabs} dataGuide={dataGuide} />}
      {children}
    </PageContainer>
  )
}
