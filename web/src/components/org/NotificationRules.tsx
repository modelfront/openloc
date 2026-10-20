import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Mail } from 'lucide-react'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { QueryBoundary } from '@/components/layout/QueryBoundary'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import {
  getOrgNotificationRules,
  updateOrgNotificationRules,
  type OrgNotificationRules,
} from '../../api'

const RULE_EVENTS = [
  { key: 'notify_assignment' as const, label: 'Task assignment', description: 'Email translators/reviewers when assigned to a workflow step' },
  { key: 'notify_rejection' as const, label: 'Step rejection', description: 'Email translators when their work is rejected and sent back' },
  { key: 'notify_completion' as const, label: 'Workflow completion', description: 'Email when all workflow steps are completed for a job' },
  { key: 'notify_deadline' as const, label: 'Deadline approaching', description: 'Email assigned users when a job deadline is within 24 hours' },
]

// The PUT replaces all four rules, so the toggles render only from rules the
// server returned: a failed load shows Retry, never editable defaults.
function OrgNotificationRulesSection() {
  const queryClient = useQueryClient()
  const rulesQuery = useQuery({
    queryKey: queryKeys.orgSettings.notificationRules,
    queryFn: getOrgNotificationRules,
  })
  const save = useMutationWithToast({
    mutationFn: updateOrgNotificationRules,
    successMessage: 'Notification rules saved',
    errorMessage: 'Failed to save notification rules',
    invalidateKeys: [queryKeys.orgSettings.notificationRules],
    onSuccess: (saved) => queryClient.setQueryData(queryKeys.orgSettings.notificationRules, saved),
  })

  const toggle = (rules: OrgNotificationRules, key: keyof OrgNotificationRules) =>
    save.mutate({ ...rules, [key]: !rules[key] })

  return (
    <SettingsCard
      title="Notification rules"
      icon={<Mail />}
      className="mt-6"
      description={<>
        Master switches for email notifications. When disabled, no emails are sent for this event type — even if individual users have it enabled.
        Users can still mute enabled notifications from their personal Settings.
      </>}
    >
      <QueryBoundary query={rulesQuery} what="notification rules">{saved => {
        // While a save is in flight the toggles show the rules being saved; a
        // failed save falls back to the server's rules.
        const rules = (save.isPending ? save.variables : undefined) ?? saved
        return (
          <div className="space-y-3">
            {RULE_EVENTS.map(evt => (
              <CheckboxField
                key={evt.key}
                checked={rules[evt.key]}
                onChange={() => toggle(rules, evt.key)}
                disabled={save.isPending}
                label={evt.label}
                description={evt.description}
              />
            ))}
          </div>
        )
      }}</QueryBoundary>
    </SettingsCard>
  )
}

export default OrgNotificationRulesSection
