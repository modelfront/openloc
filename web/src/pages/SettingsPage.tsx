import { useState, useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { useScrollToError } from '../hooks/useScrollToError'
import { getMe, updateMe, getNotificationPreferences, updateNotificationPreferences, type NotificationPreferences } from '../api'
import { AppearanceSection } from '../components/settings/AppearanceSection'
import { ConnectedAppsSection } from '../components/settings/ConnectedAppsSection'
import { toast } from 'sonner'
import { errorText } from '@/lib/error-text'

const profileSchema = z.object({
  name: z.string().min(1, 'Name is required'),
})
type ProfileFormValues = z.infer<typeof profileSchema>

export default function SettingsPage() {
  const [loading, setLoading] = useState(true)
  // Shown, never edited: invitations and sign-in trust this address.
  const [email, setEmail] = useState('')
  // An operator logged in as an org sees the org owner's profile, which isn't
  // theirs to save (the server refuses it too).
  const [impersonating, setImpersonating] = useState(false)

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<ProfileFormValues>({
    resolver: zodResolver(profileSchema),
    defaultValues: { name: '' },
  })
  useScrollToError(errors)

  useEffect(() => {
    getMe().then(user => {
      reset({ name: user.name })
      setEmail(user.email)
      setImpersonating(user.impersonating === true)
      setLoading(false)
    }).catch(() => {
      toast.error('Failed to load profile')
      setLoading(false)
    })
  }, [reset])

  const onSubmit = async (data: ProfileFormValues) => {
    try {
      const updated = await updateMe({ name: data.name })
      reset({ name: updated.name })
      toast.success('Profile updated')
    } catch (err) {
      toast.error(errorText(err, 'Failed to update profile'))
    }
  }

  if (loading) {
    return <LoadingPlaceholder />
  }

  return (
    <PageContainer>
      <PageHeader title="Settings" />

      {/* Profile */}
      <SettingsCard title="Profile" className="mb-6" data-guide="settings.profile">
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <FormField label="Name" error={errors.name?.message} required>
            <Input {...register('name')} placeholder="Your name" readOnly={impersonating} maxLength={200} />
          </FormField>
          <FormField label="Email">
            <Input readOnly value={email} type="email" />
          </FormField>
          <Button
            type="submit"
            disabled={isSubmitting || impersonating}
            disabledReason={impersonating ? "The profile can't be changed while you are logged in as an organization." : undefined}
          >
            {isSubmitting ? 'Saving...' : 'Save changes'}
          </Button>
        </form>
      </SettingsCard>

      {/* Notifications */}
      <NotificationPreferencesSection />

      {/* Appearance */}
      <AppearanceSection />

      <div data-guide="settings.connected_apps">
        <ConnectedAppsSection />
      </div>

    </PageContainer>
  )
}

const NOTIF_EVENTS = [
  { key: 'email_assignment' as const, label: 'Task assignment', description: 'When you are assigned to a workflow step' },
  { key: 'email_rejection' as const, label: 'Step rejection', description: 'When your work is rejected and sent back' },
  { key: 'email_completion' as const, label: 'Workflow completion', description: 'When all workflow steps are completed' },
  { key: 'email_deadline' as const, label: 'Deadline approaching', description: 'When a job deadline is within 24 hours' },
  { key: 'email_issue_updates' as const, label: 'Issue updates', description: 'When the support team replies to or updates an issue you reported' },
]

function NotificationPreferencesSection() {
  const [prefs, setPrefs] = useState<NotificationPreferences | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    getNotificationPreferences().then(setPrefs).catch(() => {
      // No preferences = defaults (all true)
      setPrefs({ email_assignment: true, email_rejection: true, email_completion: true, email_deadline: true, email_issue_updates: true })
    })
  }, [])

  const toggle = async (key: keyof NotificationPreferences) => {
    if (!prefs) return
    const updated = { ...prefs, [key]: !prefs[key] }
    setPrefs(updated)
    setSaving(true)
    try {
      await updateNotificationPreferences(updated)
      toast.success('Notification preferences saved')
    } catch {
      setPrefs(prefs) // revert
      toast.error('Failed to save preferences')
    } finally {
      setSaving(false)
    }
  }

  if (!prefs) return null

  return (
    <SettingsCard
      title="Email notifications"
      className="mb-6"
      data-guide="settings.notifications"
      description="Opt out of email notifications for specific events. Your organization admin controls which notifications are enabled — you can only mute events they've turned on."
    >
      <div className="space-y-3">
        {NOTIF_EVENTS.map(evt => (
          <CheckboxField
            key={evt.key}
            checked={prefs[evt.key]}
            onChange={() => toggle(evt.key)}
            disabled={saving}
            label={evt.label}
            description={evt.description}
          />
        ))}
      </div>
    </SettingsCard>
  )
}
