import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { resetPassword, saveLoginResult, type AuthRequestError } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'
import { Alert } from '@/components/ui/alert'

// Redeem a password-reset link. On success the backend sets the
// password AND logs the user in (the email round-trip proved their identity),
// so this lands exactly like the login page.
export default function ResetPassword() {
  usePageTitle('Set a new password')
  const { token } = useParams<{ token: string }>()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (password !== confirm) {
      setError('Passwords do not match')
      return
    }
    setError('')
    setSubmitting(true)
    try {
      const result = await resetPassword(token!, password)
      saveLoginResult(result)
      navigate(result.needs_org_selection ? '/select-org' : '/')
    } catch (err) {
      const e = err as AuthRequestError
      setError(
        e.code === 'invalid_token'
          ? 'This reset link is invalid or has expired. Request a new one below.'
          : e.message || 'Could not reset the password',
      )
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="bg-card rounded-lg shadow-sm border border-gray-200 dark:border-gray-800 p-8 w-full max-w-sm">
        <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100 mb-1">Set a new password</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">
          Choose a new password for your account.
        </p>

        <form onSubmit={handleSubmit} className="space-y-3">
          {error && (
            <Alert tone="error">
              {error}{' '}
              {error.includes('expired') && (
                <TextLink to="/forgot-password">
                  Request a new link
                </TextLink>
              )}
            </Alert>
          )}
          <Input
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="New password (min 8 characters)"
            aria-label="New password"
          />
          <Input
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            placeholder="Confirm new password"
            aria-label="Confirm new password"
          />
          <Button type="submit" className="w-full" disabled={submitting || !password || !confirm}>
            {submitting ? 'Saving...' : 'Set password & sign in'}
          </Button>
        </form>
      </div>
    </div>
  )
}
