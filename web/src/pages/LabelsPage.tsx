// LabelsPage — where a tenant defines how its projects are classified.
// Replaces the Customers page: Customer is one of the
// labels now, alongside Domain and whatever the tenant adds.
//
// The list is LabelManager, shared with the Manage labels dialog on the New
// project form.

import { useQuery } from '@tanstack/react-query'
import { getMe } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { LabelManager } from '../components/label/LabelManager'

export default function LabelsPage() {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isManager = me === undefined ? undefined : isManagerRole(me?.role)

  return (
    <PageContainer>
      <PageHeader
        title="Labels"
      />
      <div data-guide="labels.list">
        <LabelManager
          canEdit={isManager === true}
          // Creating a label and its values is manager work; deleting one reaches
          // every project that carried it, so it is admin or above.
          canDelete={isAdminRole(me?.role)}
        />
      </div>
    </PageContainer>
  )
}
