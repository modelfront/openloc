import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getMe } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isAdminRole } from '../../lib/roles'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { SubNav, type SubNavItem } from '@/components/layout/SubNav'
import { ORG_MEMBERS_PATH, ORG_SETTINGS_PATH, ORG_VENDORS_PATH } from './org-paths'

const ADMIN_TABS: SubNavItem[] = [
  { to: ORG_MEMBERS_PATH, label: 'Members', end: true },
  { to: ORG_SETTINGS_PATH, label: 'Settings' },
  { to: ORG_VENDORS_PATH, label: 'Vendors' },
]

/**
 * The Organization page: a header and one tab per route. Owners and admins see
 * Members, Settings and Vendors; a project manager reaches only Vendors, so
 * with a single tab there is no tab row. The routes in App.tsx enforce the same
 * split (Members and Settings send everyone else to Vendors).
 */
export function OrgPageFrame({ children }: { children: ReactNode }) {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const admin = isAdminRole(me?.role)
  return (
    <PageContainer>
      <PageHeader title="Organization" />
      {admin && <SubNav className="mb-4" items={ADMIN_TABS} dataGuide="org.tabs" />}
      {children}
    </PageContainer>
  )
}
