import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Copy } from 'lucide-react'
import {
  listAPITokens,
  createAPIToken,
  revokeAPIToken,
  type APIToken,
  type CreatedAPIToken,
  type TokenCapability,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { SectionInfo } from '@/components/ui/section-info'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { Chip } from '@/components/ui/chip'
import { Alert } from '@/components/ui/alert'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'

// Personal access keys — the credential Claude Code / Claude Desktop
// and headless agents use. Lives on the AI settings page
// next to the provider API keys, since both answer "what can talk to this org's
// AI surface". Unlike those keys it is PER-USER and open to every role, which
// is why the page itself is not admin-gated.
//
// The whole UX turns on one constraint: the plaintext key exists only in the
// create response. If the user navigates away without copying it, it is gone and
// they must create another. So the reveal is a distinct, deliberately
// hard-to-miss state rather than a line in the table.

// Both pickers are the shared `Combobox`. No E2E drives them; the coverage is
// vitest, which handles the popover fine.
// If an E2E ever needs to set expiry/capability, prefer creating the key via
// the API over reverting these to selects.
const CAPABILITY_OPTIONS = [
  { value: 'read', label: 'Read only' },
  { value: 'write', label: 'Read and write' },
]

const EXPIRY_OPTIONS = [
  { days: 30, label: '30 days' },
  { days: 90, label: '90 days' },
  { days: 180, label: '180 days' },
  { days: 365, label: '1 year' },
]

export function APITokensSection() {
  const [name, setName] = useState('')
  const [expiryDays, setExpiryDays] = useState(90)
  const [capability, setCapability] = useState<TokenCapability>('read')
  const [revealed, setRevealed] = useState<CreatedAPIToken | null>(null)

  const { data: tokens, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.apiTokens.mine,
    queryFn: () => listAPITokens(),
  })

  // Both listing scopes are invalidated: a manager viewing the org-wide list
  // would otherwise still see a key they just revoked.
  const invalidateKeys = [queryKeys.apiTokens.mine, queryKeys.apiTokens.all]

  const createMut = useMutationWithToast({
    mutationFn: () =>
      createAPIToken({ name: name.trim(), expires_in_days: expiryDays, capability }),
    successMessage: 'Access key created',
    errorMessage: 'Could not create access key',
    invalidateKeys,
    onSuccess: (data) => {
      setRevealed(data)
      setName('')
    },
  })

  const revokeMut = useMutationWithToast({
    mutationFn: (id: number) => revokeAPIToken(id),
    successMessage: 'Access key revoked',
    errorMessage: 'Could not revoke access key',
    invalidateKeys,
  })

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    createMut.mutate()
  }

  return (
    <SlimCollapsibleSection
      title="Access keys"
      // Closed by default: connecting an agent is a once-in-a-while task, while
      // the provider grid above it is what people come to this page for.
      data-testid="api-tokens-section"
      info={
        <SectionInfo label="About access">
          For tools that cannot sign in, and for your own scripts: an access key connects them to OpenLoc via MCP or the API.
        </SectionInfo>
      }
    >
      {revealed && <RevealedToken created={revealed} onDismiss={() => setRevealed(null)} />}

      <form onSubmit={submit} className="flex flex-wrap items-end gap-3 mb-6">
        <div className="flex-1 min-w-[200px]">
          <FormField label="Name" required>
            <Input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="e.g. Claude Code"
              maxLength={100}
              data-testid="token-name-input"
            />
          </FormField>
        </div>
        <div>
          <FormField label="Expires">
            <Combobox
              value={String(expiryDays)}
              onChange={v => setExpiryDays(Number(v))}
              options={EXPIRY_OPTIONS.map(o => ({ value: String(o.days), label: o.label }))}
              className="w-32"
              data-testid="token-expiry-select"
            />
          </FormField>
        </div>
        <div>
          <FormField label="Access">
            <Combobox
              value={capability}
              onChange={v => setCapability(v as TokenCapability)}
              options={CAPABILITY_OPTIONS}
              className="w-40"
              data-testid="token-capability-select"
            />
          </FormField>
        </div>
        <Button type="submit" disabled={!name.trim() || createMut.isPending} data-testid="create-token-btn">
          {createMut.isPending ? 'Creating…' : 'Create access key'}
        </Button>
      </form>

      {isLoading ? (
        <LoadingPlaceholder size="sm" />
      ) : isError && !tokens ? (
        <LoadFailed what="access keys" error={error} onRetry={() => refetch()} />
      ) : !tokens?.length ? (
        <EmptyState message="No MCP/API access keys created yet." />
      ) : (
        <ul className="divide-y divide-gray-200 dark:divide-gray-800">
          {tokens.map(token => (
            <TokenRow
              key={token.id}
              token={token}
              onRevoke={() => revokeMut.mutate(token.id)}
              revoking={revokeMut.isPending}
            />
          ))}
        </ul>
      )}
    </SlimCollapsibleSection>
  )
}

/**
 * The one-time reveal. Styled as a prominent callout because the secret is
 * genuinely unrecoverable once dismissed.
 */
function RevealedToken({ created, onDismiss }: { created: CreatedAPIToken; onDismiss: () => void }) {
  return (
    <Alert
      tone="warning"
      data-testid="revealed-token"
      className="mb-6"
      title="Copy your access key now — it won't be shown again."
    >
      <div className="mt-2 flex items-center gap-2">
        <code className="flex-1 min-w-0 overflow-x-auto rounded border border-border bg-background px-3 py-2 text-xs text-foreground">
          {created.token}
        </code>
        <CopyLinkButton
          url={created.token}
          iconOnly
          icon={Copy}
          label="Copy token"
          toastMessage="Copied to clipboard"
          errorMessage="Could not copy — select the access key and copy it manually"
          data-testid="copy-token-btn"
        />
      </div>
      <div className="mt-3 flex justify-end">
        <Button variant="ghost" size="sm" onClick={onDismiss} data-testid="dismiss-token-btn">
          Done
        </Button>
      </div>
    </Alert>
  )
}

function TokenRow({
  token,
  onRevoke,
  revoking,
}: {
  token: APIToken
  onRevoke: () => void
  revoking: boolean
}) {
  const revoked = token.revoked_at !== null
  const expired = !revoked && new Date(token.expires_at) < new Date()

  return (
    <li className="flex items-center justify-between gap-4 py-3" data-testid={`token-row-${token.id}`}>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-medium text-sm text-gray-900 dark:text-gray-100 truncate">{token.name}</span>
          {revoked ? (
            <Chip>Revoked</Chip>
          ) : expired ? (
            <Chip color="yellow">Expired</Chip>
          ) : (
            <Chip color="green">Active</Chip>
          )}
          <Chip>{token.capability === 'write' ? 'Read/write' : 'Read only'}</Chip>
        </div>
        <p className="text-xs text-gray-400 dark:text-gray-500 mt-0.5">
          <code>{token.prefix}…</code>
          {token.user_email ? <> · {token.user_email}</> : null}
          {' · '}
          {token.last_used_at ? `last used ${formatDate(token.last_used_at)}` : 'never used'}
          {' · '}
          {revoked ? `revoked ${formatDate(token.revoked_at!)}` : `expires ${formatDate(token.expires_at)}`}
        </p>
      </div>
      {!revoked && (
        <DeleteButton
          onClick={onRevoke}
          disabled={revoking}
          data-testid={`revoke-token-${token.id}`}
          aria-label={`Revoke ${token.name}`}
        />
      )}
    </li>
  )
}

function formatDate(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleDateString()
}
