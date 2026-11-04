import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { SectionPageFrame } from '@/components/layout/SectionPageFrame'
import type { SubNavItem } from '@/components/layout/SubNav'
import { getMe } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { isVendorOrg } from '@/lib/roles'
import { ORG_MEMBERS_PATH, ORG_SETTINGS_PATH } from './org-paths'

const TABS: SubNavItem[] = [
  { to: ORG_MEMBERS_PATH, label: 'Members', end: true },
  { to: ORG_SETTINGS_PATH, label: 'Settings' },
]

/**
 * The Organization page: a header and one tab per route. Owner/admin only:
 * the routes in App.tsx send everyone else away. A vendor org has only
 * Members, so it gets no tab row, and the header names its client.
 */
export function OrgPageFrame({ children }: { children: ReactNode }) {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const vendor = isVendorOrg(me)
  return (
    <SectionPageFrame
      title="Organization"
      subtitle={vendor && me?.client_org_name ? `Vendor for ${me.client_org_name}` : undefined}
      tabs={vendor ? undefined : TABS}
      dataGuide="org.tabs"
    >
      {/* The tab's sections, a fixed gap apart. */}
      <div className="space-y-5">{children}</div>
    </SectionPageFrame>
  )
}
