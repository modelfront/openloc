import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Clock, LogOut } from 'lucide-react'
import { getMe, logout } from '../api'
import { queryKeys } from '../lib/query-keys'
import { Button } from '../components/ui/button'
import { TextLink } from '../components/ui/text-link'
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
  // A vendor org whose client org is not approved is closed with it.
  const clientInactive = me?.tenant_status === 'client_inactive'
  usePageTitle(me?.membership_disabled ? 'Access disabled' : clientInactive ? 'Not available' : 'Awaiting approval')

  // Approved (or the org was never gated) → drop into the app.
  const approved = !!me && !!me.tenant_id && !me.org_pending && !me.membership_disabled
  useEffect(() => {
    if (approved) window.location.assign('/')
  }, [approved])
  if (approved) return null

  // A disabled/declined org gets a different message than one under review.
  const disabled = me?.tenant_status === 'suspended' || me?.tenant_status === 'rejected'
  const orgName = me?.tenant_name ?? 'Your organization'
  // A vendor org disabled this person's access after 90 days without use.
  const memberDisabled = !!me?.membership_disabled
  // A person blocked in one org may still work in another.
  const otherOrgs = (
    <p className="text-sm mt-4">
      <TextLink to="/select-org">Open another organization</TextLink>
    </p>
  )

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-md text-center">
        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-8">
          <span className="flex items-center justify-center w-12 h-12 mx-auto rounded-full bg-amber-100 dark:bg-amber-900/30 text-amber-600 dark:text-amber-400 mb-4">
            <Clock className="w-6 h-6" />
          </span>
          {memberDisabled ? (
            <>
              <h1 className="text-xl font-normal text-gray-900 dark:text-gray-100">
                Your access to {orgName} is disabled
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
                Access ends after 90 days without use. Ask an admin of {orgName} to
                enable it. This page updates once they do.
              </p>
              {otherOrgs}
            </>
          ) : clientInactive ? (
            <>
              <h1 className="text-xl font-normal text-gray-900 dark:text-gray-100">
                {orgName} is not available
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
                The organization this vendor works for is not active right now.
                This page updates once it is.
              </p>
              {otherOrgs}
            </>
          ) : disabled ? (
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
