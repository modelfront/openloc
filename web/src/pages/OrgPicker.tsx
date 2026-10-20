import { useQuery } from '@tanstack/react-query'
import { Building2, LogOut } from 'lucide-react'
import { listOrgs, getMe, selectOrg, logout, type Org } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { CreateOrgCard } from '../components/CreateOrgCard'
import { Button } from '../components/ui/button'
import { usePageTitle } from '../hooks/usePageTitle'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'

// The org picker. Rendered after
// authentication when the identity has 0 or >1 orgs. Selecting an org exchanges
// the identity token for a tenant-scoped token and drops into the app. When the
// identity has NO org, it can create one (self-serve signup).
export default function OrgPicker() {
  usePageTitle('Choose an organization')
  const { data: orgs, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.auth.orgs,
    queryFn: listOrgs,
  })
  // Only needed for the empty-state signup copy (the user's email domain).
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })

  const enter = useMutationWithToast({
    mutationFn: (tenantId: number) => selectOrg(tenantId),
    successMessage: 'Organization selected',
    errorMessage: 'Could not enter organization',
    // Full reload so every cached query (me, projects, …) refetches under the
    // newly-selected org's token rather than the previous org's.
    onSuccess: () => { window.location.href = '/' },
  })

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100">Choose an organization</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">Select which workspace to open.</p>
        </div>

        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4">
          {isLoading ? (
            <LoadingPlaceholder size="sm" />
          ) : isError ? (
            <LoadFailed what="organizations" error={error} onRetry={() => { void refetch() }} />
          ) : !orgs || orgs.length === 0 ? (
            <CreateOrgCard email={me?.email} />
          ) : (
            <ul className="space-y-2">
              {orgs.map((org: Org) => (
                <li key={org.tenant_id}>
                  {/* eslint-disable-next-line no-restricted-syntax -- whole-card org selection target, not an action button */}
                  <button
                    type="button"
                    disabled={enter.isPending}
                    onClick={() => enter.mutate(org.tenant_id)}
                    className="w-full flex items-center gap-3 px-3 py-3 rounded-md border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 text-left hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors disabled:opacity-50 cursor-pointer"
                  >
                    <span className="flex items-center justify-center w-9 h-9 rounded-md bg-gray-100 dark:bg-gray-700 text-gray-500 dark:text-gray-300 shrink-0">
                      <Building2 className="w-5 h-5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-sm font-medium text-gray-900 dark:text-gray-100 truncate">
                        {org.tenant_name}
                      </span>
                      <span className="block text-xs text-gray-500 dark:text-gray-400 capitalize">
                        {org.role.replace('_', ' ')}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="mt-4 flex justify-center">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => logout()}
            startIcon={<LogOut />}
            className="text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
          >
            Sign out
          </Button>
        </div>
      </div>
    </div>
  )
}
