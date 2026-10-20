import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Switch } from '@/components/ui/switch'
import { QueryBoundary } from '@/components/layout/QueryBoundary'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import {
  getOrgSettings,
  updateOrgSettings,
} from '../../api'

function RegistrationSettingsSection() {
  const queryClient = useQueryClient()
  const settingsQuery = useQuery({
    queryKey: queryKeys.orgSettings.registration,
    queryFn: getOrgSettings,
  })
  const save = useMutationWithToast({
    mutationFn: (openRegistration: boolean) => updateOrgSettings({ open_registration: openRegistration }),
    successMessage: (updated) => updated.open_registration ? 'Open registration enabled' : 'Switched to invite-only',
    errorMessage: 'Failed to update registration settings',
    invalidateKeys: [queryKeys.orgSettings.registration],
    onSuccess: (updated) => queryClient.setQueryData(queryKeys.orgSettings.registration, updated),
  })

  return (
    <SettingsCard
      title="Registration"
      className="mb-6"
      description={<>
        Control how new members join your organization.
        {settingsQuery.data?.domain && <span className="ml-1">Domain: <span className="font-mono text-gray-700 dark:text-gray-300">@{settingsQuery.data.domain}</span></span>}
      </>}
    >
      <QueryBoundary query={settingsQuery} what="registration settings">{settings => (
        <label className="flex items-center gap-3 cursor-pointer group">
          <Switch
            checked={settings.open_registration}
            onCheckedChange={() => save.mutate(!settings.open_registration)}
            disabled={save.isPending}
            aria-label="Open registration"
          />
          <div>
            <span className="text-sm font-medium text-gray-900 dark:text-gray-100">
              {settings.open_registration ? 'Anyone with organization email can join' : 'Invite only'}
            </span>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {settings.open_registration
                ? 'Anyone with a matching email domain can sign in via Google SSO and automatically join as a Project manager. People already on the Vendors list join as linguists instead.'
                : 'New members must be invited by an admin. They receive an invite link to join.'}
            </p>
          </div>
        </label>
      )}</QueryBoundary>
    </SettingsCard>
  )
}

export default RegistrationSettingsSection
