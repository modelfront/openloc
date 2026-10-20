// The AI section's shared shell: the page title and the Routers | Engines |
// Prompts tabs. The URL is the state (SubNav), so each tab is a
// route — /ai, /ai/engines, /ai/prompts — and deep links work.
import type { ReactNode } from 'react'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { SubNav } from '@/components/layout/SubNav'

const AI_TABS = [
  { to: '/ai', label: 'Routers', end: true },
  { to: '/ai/engines', label: 'Engines' },
  { to: '/ai/prompts', label: 'Prompts' },
]

export function AIPageFrame({ children }: { children: ReactNode }) {
  return (
    <PageContainer>
      <PageHeader title="AI" />
      <SubNav className="mb-4" items={AI_TABS} />
      {children}
    </PageContainer>
  )
}
