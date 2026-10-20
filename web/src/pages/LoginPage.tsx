import { useState, useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Wordmark } from '@/components/layout/Wordmark'
import { devLogin, getAuthConfig, passwordLogin, saveLoginResult, type AuthConfig, type AuthResult } from '../api'
import { GoogleSignInButton, MicrosoftSignInButton } from '@/components/auth/SsoButtons'
import { isSameOriginPath } from '../lib/safe-redirect'
import { usePageTitle } from '../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'
import { Alert } from '@/components/ui/alert'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { errorText } from '@/lib/error-text'

// Sign-in: Google/Microsoft SSO or email+password. After
// authenticating, the backend decides where to land: straight into the app
// (single org) or the org picker (0 or multiple orgs).
export default function LoginPage() {
  usePageTitle('Sign in')
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [config, setConfig] = useState<AuthConfig | null>(null)
  const [serverError, setServerError] = useState('')
  const [loading, setLoading] = useState(false)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  // Dev-only "log in as…" — exercise multi-org / signup flows without the console.
  const [devEmail, setDevEmail] = useState('')

  useEffect(() => {
    if (searchParams.get('error')) {
      setServerError('Sign in failed. Please try again.')
    }

    getAuthConfig()
      .then(setConfig)
      .catch(() => setConfig({ google_enabled: false, microsoft_enabled: false, dev_login_enabled: false }))
  }, [searchParams])

  // Auto-login in dev mode — but NOT right after an explicit sign-out
  // (?signedout=1), so signing out actually lands (and stays) on the login page.
  const [autoLoginAttempted, setAutoLoginAttempted] = useState(false)
  useEffect(() => {
    const justSignedOut = searchParams.get('signedout') === '1'
    if (config?.dev_login_enabled && !autoLoginAttempted && !serverError && !justSignedOut) {
      setAutoLoginAttempted(true)
      handleDevLogin()
    }
  }, [config]) // eslint-disable-line react-hooks/exhaustive-deps

  // Shared landing: 0 or >1 orgs → pick one; exactly 1 → the backend already
  // minted a tenant token and we can drop straight into the app.
  const finishLogin = (result: AuthResult) => {
    saveLoginResult(result)
    // OAuth consent return: ?next= carries the
    // /oauth/authorize URL. It is a BACKEND-rendered page, so this must be a
    // full navigation, and only ever a same-origin path (open-redirect guard).
    // The consent page has its own org selector, so it works with an
    // identity-scoped session too — no detour through the org picker.
    const next = searchParams.get('next')
    if (next && isSameOriginPath(next)) {
      window.location.assign(next)
      return
    }
    navigate(result.needs_org_selection ? '/select-org' : '/')
  }

  const handleDevLogin = async (email?: string) => {
    setServerError('')
    setLoading(true)
    try {
      finishLogin(await devLogin(email))
    } catch (err) {
      setServerError(errorText(err, 'Dev login failed'))
    } finally {
      setLoading(false)
    }
  }

  const handlePasswordLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setServerError('')
    setLoading(true)
    try {
      finishLogin(await passwordLogin(email.trim(), password))
    } catch (err) {
      setServerError(errorText(err, 'Sign in failed'))
    } finally {
      setLoading(false)
    }
  }

  const showGoogle = config?.google_enabled
  const showMicrosoft = config?.microsoft_enabled
  const showDevLogin = config?.dev_login_enabled
  const showPassword = config?.password_enabled

  return (
    <div className="min-h-screen bg-background flex items-center justify-center">
      <div className="bg-card rounded-lg shadow-sm border border-gray-200 dark:border-gray-800 p-8 w-full max-w-sm">
        <h1 className="mb-1"><Wordmark size="large" /></h1>
        <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">Sign in to your account</p>

        {serverError && (
          <Alert tone="error" className="mb-4">{serverError}</Alert>
        )}

        {config === null ? (
          <LoadingPlaceholder size="sm" />
        ) : (
          <div className="space-y-4">
            {showGoogle && <GoogleSignInButton />}

            {showMicrosoft && <MicrosoftSignInButton />}

            {showPassword && (
              <form onSubmit={handlePasswordLogin} className="space-y-3">
                {(showGoogle || showMicrosoft) && (
                  <div className="relative py-1">
                    <div className="absolute inset-0 flex items-center">
                      <div className="w-full border-t border-gray-200 dark:border-gray-700" />
                    </div>
                    <div className="relative flex justify-center">
                      <span className="bg-card px-2 text-xs text-gray-400 uppercase">or</span>
                    </div>
                  </div>
                )}
                <Input
                  type="email"
                  autoComplete="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@company.com"
                  aria-label="Email"
                />
                <Input
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Password"
                  aria-label="Password"
                />
                <Button type="submit" className="w-full" disabled={loading || !email.trim() || !password}>
                  {loading ? 'Signing in...' : 'Sign in'}
                </Button>
                <p className="text-center">
                  <TextLink tone="muted" to="/forgot-password" className="text-xs">
                    Forgot password?
                  </TextLink>
                </p>
              </form>
            )}

            {showDevLogin && (
              <>
                <Button
                  onClick={() => handleDevLogin()}
                  disabled={loading}
                  variant="outline"
                  className="w-full"
                >
                  {loading ? 'Signing in...' : 'Dev Login'}
                </Button>

                {/* Dev-only: log in as any identity (created on the fly). Lets
                    you exercise multi-org / self-serve signup flows without the
                    console. Hidden in prod (dev-login is disabled there). */}
                <form
                  onSubmit={(e) => { e.preventDefault(); if (devEmail.trim()) handleDevLogin(devEmail.trim()) }}
                  className="flex gap-2 pt-2 border-t border-gray-100 dark:border-gray-800"
                >
                  <Input
                    type="email"
                    value={devEmail}
                    onChange={(e) => setDevEmail(e.target.value)}
                    placeholder="you@newco.io"
                    className="flex-1"
                  />
                  <Button type="submit" variant="outline" disabled={loading || !devEmail.trim()}>
                    Log in as
                  </Button>
                </form>
              </>
            )}

            {!showGoogle && !showMicrosoft && !showDevLogin && !showPassword && (
              <p className="text-sm text-gray-500 text-center">
                Contact your administrator for access.
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
