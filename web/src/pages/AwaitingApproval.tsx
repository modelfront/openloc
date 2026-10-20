import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Clock, LogOut } from 'lucide-react'
import { getMe, logout } from '../api'
import { queryKeys } from '../lib/query-keys'
import { Button } from '../components/ui/button'
import { usePageTitle } from '../hooks/usePageTitle'

// Shown while a self-serve org signup is awaiting operator approval.
// The owner holds a valid tenant token but the pending-gate blocks the app;
// /auth/me still works (identity-scoped) and reports org_pending. We refetch
// periodically so the screen advances to the app the moment an operator
// approves — no manual reload or re-login needed.
export default function AwaitingApproval() {
  const { data: me } = useQuery({
    queryKey: queryKeys.auth.me,
    queryFn: getMe,
    refetchInterval: 15000,
  })
  usePageTitle('Awaiting approval')

  // Approved (or the org was never gated) → drop into the app.
  const approved = !!me && !!me.tenant_id && !me.org_pending
  useEffect(() => {
    if (approved) window.location.assign('/')
  }, [approved])
  if (approved) return null

  // A disabled/declined org gets a different message than one under review.
  const disabled = me?.tenant_status === 'suspended' || me?.tenant_status === 'rejected'
  const orgName = me?.tenant_name ?? 'Your organization'

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-md text-center">
        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-8">
          <span className="flex items-center justify-center w-12 h-12 mx-auto rounded-full bg-amber-100 dark:bg-amber-900/30 text-amber-600 dark:text-amber-400 mb-4">
            <Clock className="w-6 h-6" />
          </span>
          {disabled ? (
            <>
              <h1 className="text-xl font-normal text-gray-900 dark:text-gray-100">
                {orgName} is not active
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
                This organization isn&rsquo;t currently available. If you think this is a
                mistake, contact our team and we&rsquo;ll take a look.
              </p>
            </>
          ) : (
            <>
              <h1 className="text-xl font-normal text-gray-900 dark:text-gray-100">
                Your organization is under review
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
                <strong>{orgName}</strong> has been created. Our team reviews new
                organizations before activation. You&rsquo;ll get an email as soon as it&rsquo;s
                approved — then you can invite your team and get started.
              </p>
              <p className="text-xs text-gray-400 dark:text-gray-500 mt-4">
                This page updates automatically once you&rsquo;re approved.
              </p>
            </>
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
