// The AI section's shared shell: the page title and the Routers | Engines |
// Prompts tabs. The URL is the state (SubNav), so each tab is a
// route — /ai, /ai/engines, /ai/prompts — and deep links work.
import type { ReactNode } from 'react'
import { SectionPageFrame } from '@/components/layout/SectionPageFrame'

const AI_TABS = [
  { to: '/ai', label: 'Routers', end: true },
  { to: '/ai/engines', label: 'Engines' },
  { to: '/ai/prompts', label: 'Prompts' },
]

export function AIPageFrame({ children }: { children: ReactNode }) {
  return <SectionPageFrame title="AI" tabs={AI_TABS}>{children}</SectionPageFrame>
}
