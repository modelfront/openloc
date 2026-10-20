// The MT section's shared shell: the page title and the Routers | Engines |
// Prompts tabs, mirroring the AI page. The URL is the state (SubNav), so each
// tab is a route — /mt, /mt/engines, /mt/prompts.
import type { ReactNode } from 'react'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { SubNav } from '@/components/layout/SubNav'

const MT_TABS = [
  { to: '/mt', label: 'Routers', end: true },
  { to: '/mt/engines', label: 'Engines' },
  { to: '/mt/prompts', label: 'Prompts' },
]

export function MTPageFrame({ children }: { children: ReactNode }) {
  return (
    <PageContainer>
      <PageHeader title="MT" />
      <SubNav className="mb-4" items={MT_TABS} />
      {children}
    </PageContainer>
  )
}
