import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { forgotPassword } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'
import { Alert } from '@/components/ui/alert'

// Request a password-reset link. The confirmation copy is identical
// whether or not the account exists — the backend never reveals which. This is
// also how an OAuth-only account adds a password.
export default function ForgotPassword() {
  usePageTitle('Reset password')
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      await forgotPassword(email.trim())
      setSent(true)
    } catch {
      setError('Something went wrong. Please try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="bg-card rounded-lg shadow-sm border border-gray-200 dark:border-gray-800 p-8 w-full max-w-sm">
        <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100 mb-1">Reset password</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">
          Enter your email and we'll send you a reset link.
        </p>

        {sent ? (
          <div className="space-y-4">
            <div className="p-3 rounded-md bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800">
              <p className="text-sm text-green-700 dark:text-green-400">
                If an account exists for {email.trim()}, a reset link is on its way. The link expires in 1 hour.
              </p>
            </div>
            <p className="text-center">
              <TextLink to="/login" className="text-sm">
                Back to sign in
              </TextLink>
            </p>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-3">
            {error && (
              <Alert tone="error">{error}</Alert>
            )}
            <Input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@company.com"
              aria-label="Email"
            />
            <Button type="submit" className="w-full" disabled={submitting || !email.trim()}>
              {submitting ? 'Sending...' : 'Send reset link'}
            </Button>
            <p className="text-center">
              <TextLink tone="muted" to="/login" className="text-xs">
                Back to sign in
              </TextLink>
            </p>
          </form>
        )}
      </div>
    </div>
  )
}
