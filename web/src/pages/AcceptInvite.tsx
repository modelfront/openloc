import { useState, useEffect } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  acceptInvite,
  getAuthConfig,
  lookupInvite,
  saveLoginResult,
  type AuthConfig,
  type InviteLookup,
} from '../api'
import { GoogleSignInButton, MicrosoftSignInButton } from '@/components/auth/SsoButtons'
import { usePageTitle } from '../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'
import { Alert } from '@/components/ui/alert'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { errorText } from '@/lib/error-text'

// Accepting an invite: the token is validated server-side on load,
// then the user picks a method — Google/Microsoft SSO (token rides the OAuth
// state) or, for a brand-new identity, setting a password. An existing account
// is pointed at normal sign-in instead: any sign-in materializes the invite,
// and the accept endpoint deliberately never touches existing accounts.
export default function AcceptInvite() {
  usePageTitle('Join organization')
  const { token } = useParams<{ token: string }>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [config, setConfig] = useState<AuthConfig | null>(null)
  const [invite, setInvite] = useState<InviteLookup | null>(null)
  const [invalid, setInvalid] = useState(false)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState('')

  // The OAuth callback bounces back here when the signed-in account doesn't
  // match the invited address.
  const mismatch = searchParams.get('error') === 'email_mismatch'

  useEffect(() => {
    getAuthConfig()
      .then(setConfig)
      .catch(() => setConfig({ google_enabled: false, dev_login_enabled: false }))
    if (!token) {
      setInvalid(true)
      return
    }
    lookupInvite(token)
      .then(setInvite)
      .catch(() => setInvalid(true))
  }, [token])

  const handleAccept = async (e: React.FormEvent) => {
    e.preventDefault()
    if (password !== confirm) {
      setFormError('Passwords do not match')
      return
    }
    setFormError('')
    setSubmitting(true)
    try {
      const result = await acceptInvite(token!, password, name.trim())
      saveLoginResult(result)
      navigate(result.needs_org_selection ? '/select-org' : '/')
    } catch (err) {
      setFormError(errorText(err, 'Could not accept the invitation'))
    } finally {
      setSubmitting(false)
    }
  }

  const loading = config === null || (invite === null && !invalid)

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100">Join organization</h1>
          {invite && (
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-2">
              You've been invited to <span className="font-medium text-gray-700 dark:text-gray-300">{invite.org_name}</span> as{' '}
              <span className="font-medium text-gray-700 dark:text-gray-300">{invite.email}</span>.
            </p>
          )}
        </div>

        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-6">
          {loading ? (
            <LoadingPlaceholder size="sm" />
          ) : invalid ? (
            <div className="text-center space-y-3">
              <p className="text-sm text-gray-700 dark:text-gray-300">
                This invitation link is invalid or has expired.
              </p>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                Ask your administrator for a new invite. If you already have an account,{' '}
                <TextLink to="/login">sign in</TextLink> instead.
              </p>
            </div>
          ) : (
            <div className="space-y-4">
              {mismatch && (
                <Alert tone="error">
                  This invitation is for {invite!.email}, but you signed in with a different account.
                  Sign in with the invited address to accept.
                </Alert>
              )}

              {config?.google_enabled && <GoogleSignInButton inviteToken={token} />}
              {config?.microsoft_enabled && <MicrosoftSignInButton inviteToken={token} />}

              {invite!.user_exists ? (
                <p className="text-sm text-gray-500 dark:text-gray-400 text-center pt-2 border-t border-gray-100 dark:border-gray-800">
                  You already have an account.{' '}
                  <TextLink to="/login">Sign in</TextLink>{' '}
                  {invite!.has_password ? 'with your password or SSO' : 'with SSO, or use the buttons above'} — the
                  invitation is accepted automatically.
                </p>
              ) : (
                <form onSubmit={handleAccept} className="space-y-3">
                  {(config?.google_enabled || config?.microsoft_enabled) && (
                    <div className="relative py-1">
                      <div className="absolute inset-0 flex items-center">
                        <div className="w-full border-t border-gray-200 dark:border-gray-700" />
                      </div>
                      <div className="relative flex justify-center">
                        <span className="bg-card px-2 text-xs text-gray-400 uppercase">or set a password</span>
                      </div>
                    </div>
                  )}
                  {formError && (
                    <Alert tone="error">{formError}</Alert>
                  )}
                  <Input
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Your name"
                    autoComplete="name"
                    aria-label="Name"
                    maxLength={200}
                  />
                  <Input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="Password (min 8 characters)"
                    autoComplete="new-password"
                    aria-label="Password"
                  />
                  <Input
                    type="password"
                    value={confirm}
                    onChange={(e) => setConfirm(e.target.value)}
                    placeholder="Confirm password"
                    autoComplete="new-password"
                    aria-label="Confirm password"
                  />
                  <Button type="submit" className="w-full" disabled={submitting || !password || !confirm}>
                    {submitting ? 'Creating account...' : 'Create account & join'}
                  </Button>
                </form>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
