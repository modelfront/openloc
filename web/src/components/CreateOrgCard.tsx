import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Building2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { createOrg, ApiError } from '../api'

// Personal/free email providers can't create an org (mirrors the backend
// blocklist in internal/auth/freemail.go). Client-side check is UX-only — the
// backend is authoritative — so we keep just the common ones to steer the copy.
const FREE_PROVIDERS = new Set([
  'gmail.com', 'googlemail.com', 'outlook.com', 'hotmail.com', 'live.com',
  'yahoo.com', 'icloud.com', 'me.com', 'aol.com', 'proton.me', 'protonmail.com',
  'gmx.com', 'mail.com', 'yandex.com', 'qq.com',
])

function domainOf(email?: string): string {
  if (!email) return ''
  const parts = email.toLowerCase().split('@')
  return parts.length === 2 ? parts[1] : ''
}

function orgNameFromDomain(domain: string): string {
  const label = (domain.split('.')[0] || '').trim()
  if (!label) return ''
  return label.charAt(0).toUpperCase() + label.slice(1)
}

// Empty-state card on the org picker: an identity with no org creates
// one derived from their work-email domain and becomes its owner (pending
// operator approval). Free-email accounts can't — they must be invited.
export function CreateOrgCard({ email }: { email?: string }) {
  const domain = domainOf(email)
  const defaultName = orgNameFromDomain(domain)
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const isFree = domain !== '' && FREE_PROVIDERS.has(domain)

  const create = useMutation({
    mutationFn: () => createOrg(name.trim() || undefined),
    onSuccess: () => {
      // Full reload — the returned token is for the new (pending) org; the app
      // bootstrap then routes to the awaiting-approval screen.
      window.location.href = '/awaiting-approval'
    },
    onError: (err: unknown) => {
      const code = err instanceof ApiError ? (err.body as { error?: { code?: unknown } } | undefined)?.error?.code : undefined
      const msg = err instanceof ApiError ? err.message : 'Could not create organization'
      setError(code === 'free_email'
        ? 'Personal email addresses can’t create an organization. Use your work email, or ask an admin to invite you.'
        : msg)
    },
  })

  if (isFree) {
    return (
      <div className="text-center py-6">
        <Building2 className="w-8 h-8 mx-auto text-gray-300 dark:text-gray-600 mb-3" />
        <p className="text-sm text-gray-600 dark:text-gray-300 font-medium">
          You&rsquo;re not a member of any organization yet
        </p>
        <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">
          Personal email accounts can&rsquo;t create an organization. Ask your administrator for an invite.
        </p>
      </div>
    )
  }

  return (
    <div className="py-2">
      <div className="text-center mb-4">
        <Building2 className="w-8 h-8 mx-auto text-gray-300 dark:text-gray-600 mb-3" />
        <p className="text-sm text-gray-600 dark:text-gray-300 font-medium">
          You&rsquo;re not a member of any organization yet
        </p>
        {domain && (
          <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">
            Create the organization for <span className="font-medium">{domain}</span> and become its owner.
          </p>
        )}
      </div>

      <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
        Organization name
      </label>
      <Input
        type="text"
        value={name}
        onChange={e => { setName(e.target.value); setError('') }}
        placeholder={defaultName || 'Organization name'}
        className="mb-3"
      />

      {error && (
        <p className="text-sm text-red-600 dark:text-red-400 mb-3">{error}</p>
      )}

      <Button
        onClick={() => { setError(''); create.mutate() }}
        disabled={create.isPending}
        className="w-full"
      >
        {create.isPending ? 'Creating…' : 'Create organization'}
      </Button>
      <p className="text-xs text-gray-400 dark:text-gray-500 mt-2 text-center">
        New organizations are reviewed by our team before activation.
      </p>
    </div>
  )
}
