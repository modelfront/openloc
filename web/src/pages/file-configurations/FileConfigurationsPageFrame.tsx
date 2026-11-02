// The File configurations section's shared shell: the page title and its
// tabs, one route each. /file-configurations opens the first tab.
import type { ReactNode } from 'react'
import { SectionPageFrame } from '@/components/layout/SectionPageFrame'
import { SEGMENTATION_PROFILES_PATH } from '../segmentation/segmentation-paths'

const FILE_CONFIGURATION_TABS = [
  { to: SEGMENTATION_PROFILES_PATH, label: 'Segmentation profiles' },
]

export function FileConfigurationsPageFrame({ actions, children }: {
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <SectionPageFrame title="File configurations" actions={actions} tabs={FILE_CONFIGURATION_TABS}>
      {children}
    </SectionPageFrame>
  )
}
