import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldCheck, Building2, Check, X, Power, LogIn, Plus, Copy } from 'lucide-react'
import {
  listPlatformOrgs, setOrgStatus, loginAsOrg, platformCreateOrg,
  type PlatformOrg, type OrgStatus, type PlatformCreateOrgResponse,
} from '../api'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { EmptyState } from '@/components/layout/EmptyState'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import ConfirmDialog from '@/components/ConfirmDialog'
import { Chip, type ChipColor } from '@/components/ui/chip'

// Copy for the confirmation popup on the consequential status actions. Keyed by
// UI verb (each maps to the target `status` sent to the backend) so approve and
// re-enable — both → 'approved' — can carry distinct wording.
type ConfirmAction = 'approve' | 'reject' | 'disable' | 'enable'
const CONFIRM_COPY: Record<ConfirmAction, {
  title: string
  body: (org: string) => string
  label: string
  variant: 'destructive' | 'default'
  status: Exclude<OrgStatus, 'pending'>
}> = {
  approve: {
    title: 'Approve organization?',
    body: (org) => `${org} will become active and its members will be able to use it. The owner is notified by email.`,
    label: 'Approve',
    variant: 'default',
    status: 'approved',
  },
  reject: {
    title: 'Reject organization?',
    body: (org) => `${org}'s signup will be declined and gated out of the app. The owner is notified by email. You can re-enable it later.`,
    label: 'Reject',
    variant: 'destructive',
    status: 'rejected',
  },
  disable: {
    title: 'Disable organization?',
    body: (org) => `${org} will be blocked and its members locked out until you re-enable it. The owner is notified by email.`,
    label: 'Disable',
    variant: 'destructive',
    status: 'suspended',
  },
  enable: {
    title: 'Re-enable organization?',
    body: (org) => `${org} will become active again and its members regain access. The owner is notified by email.`,
    label: 'Enable',
    variant: 'default',
    status: 'approved',
  },
}

// Approved = live, pending = needs action, the rest are blocked (gated out of
// the app by auth.Middleware).
const STATUS_COLORS: Record<OrgStatus, ChipColor> = {
  approved: 'green',
  pending: 'yellow',
  suspended: 'grey',
  rejected: 'red',
}

// Platform operator console: every organization with its lifecycle
// status, plus per-status actions. Access is gated in App by is_platform_admin;
// the backend independently enforces PLATFORM_ADMIN_EMAILS.
export default function PlatformOrgs() {
  const qc = useQueryClient()
  const { data: orgs, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.platform.orgs,
    queryFn: listPlatformOrgs,
  })

  const invalidate = () => qc.invalidateQueries({ queryKey: queryKeys.platform.orgs })

  const setStatus = useMutationWithToast({
    mutationFn: ({ tenantId, status }: { tenantId: number; status: Exclude<OrgStatus, 'pending'> }) =>
      setOrgStatus(tenantId, status),
    successMessage: 'Organization updated',
    errorMessage: 'Could not update organization',
    onSuccess: invalidate,
  })

  const openOrg = useMutationWithToast({
    mutationFn: (tenantId: number) => loginAsOrg(tenantId),
    successMessage: 'Opening organization…',
    errorMessage: 'Could not open organization',
    // Full reload so every cached query refetches under the target org's token.
    onSuccess: () => { window.location.href = '/' },
  })
  const busy = setStatus.isPending || openOrg.isPending

  // Pending confirmation for a consequential status change (approve/reject/disable).
  const [confirm, setConfirm] = useState<{ tenantId: number; org: string; action: ConfirmAction } | null>(null)
  const askConfirm = (o: PlatformOrg, action: ConfirmAction) =>
    setConfirm({ tenantId: o.tenant_id, org: o.name, action })

  // Create-org dialog: provision an org for a customer and invite
  // its owner. On success the dialog switches to showing the invite link.
  const [createOpen, setCreateOpen] = useState(false)
  const [orgName, setOrgName] = useState('')
  const [ownerEmail, setOwnerEmail] = useState('')
  const [orgDomain, setOrgDomain] = useState('')
  const [created, setCreated] = useState<PlatformCreateOrgResponse | null>(null)
  const createOrg = useMutationWithToast({
    mutationFn: () => platformCreateOrg({
      name: orgName.trim(),
      owner_email: ownerEmail.trim(),
      ...(orgDomain.trim() ? { domain: orgDomain.trim() } : {}),
    }),
    successMessage: 'Organization created — owner invited',
    // The backend's domain_taken / free_email messages are already
    // human-readable; the hook appends err.message to this prefix.
    errorMessage: 'Could not create organization',
    onSuccess: (resp: PlatformCreateOrgResponse) => {
      setCreated(resp)
      invalidate()
    },
  })
  const closeCreate = () => {
    setCreateOpen(false)
    setCreated(null)
    setOrgName(''); setOwnerEmail(''); setOrgDomain('')
  }

  return (
    <PageContainer width="default">
      <PageHeader
        documentTitle="Organizations"
        title={<span className="flex items-center gap-2"><ShieldCheck className="w-5 h-5 text-gray-500 dark:text-gray-400" />Organizations</span>}
        actions={(
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="w-4 h-4" /> Create organization
          </Button>
        )}
      />

      {isLoading ? (
        <LoadingPlaceholder />
      ) : isError ? (
        <LoadFailed what="organizations" error={error} onRetry={() => { void refetch() }} />
      ) : !orgs || orgs.length === 0 ? (
        <EmptyState message="No organizations yet." />
      ) : (
        <ul className="space-y-2">
          {orgs.map((o: PlatformOrg) => (
            <li
              key={o.tenant_id}
              className="flex items-center gap-3 p-4 rounded-lg border border-gray-200 dark:border-gray-800 bg-card"
            >
              <span className="flex items-center justify-center w-9 h-9 rounded-md bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-300 shrink-0">
                <Building2 className="w-5 h-5" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-gray-900 dark:text-gray-100 truncate">
                  {o.name} {o.domain && <span className="text-gray-400 font-normal">· {o.domain}</span>}
                </p>
                <p className="text-xs text-gray-500 dark:text-gray-400 truncate">
                  {o.owner_name ? `${o.owner_name} (${o.owner_email})` : o.owner_email ?? 'no owner'}
                </p>
              </div>
              <Chip color={STATUS_COLORS[o.status]} className="shrink-0 capitalize">
                {o.status}
              </Chip>
              <div className="flex items-center gap-2 shrink-0">
                {o.is_self ? (
                  // Your own org: no Open (use the switcher) and no Disable
                  // (self-lockout). Backend enforces both with 409 own_org.
                  <span className="text-xs text-gray-400 dark:text-gray-500 italic">Your organization</span>
                ) : (
                  <>
                    {o.status === 'pending' && (
                      <>
                        <Button size="sm" variant="outline" disabled={busy}
                          onClick={() => askConfirm(o, 'reject')}>
                          <X className="w-4 h-4" /> Reject
                        </Button>
                        <Button size="sm" disabled={busy}
                          onClick={() => askConfirm(o, 'approve')}>
                          <Check className="w-4 h-4" /> Approve
                        </Button>
                      </>
                    )}
                    {o.status === 'approved' && (
                      <>
                        <Button size="sm" variant="outline" disabled={busy}
                          onClick={() => openOrg.mutate(o.tenant_id)}>
                          <LogIn className="w-4 h-4" /> Log in as
                        </Button>
                        <Button size="sm" variant="outline" disabled={busy}
                          onClick={() => askConfirm(o, 'disable')}>
                          <Power className="w-4 h-4" /> Disable
                        </Button>
                      </>
                    )}
                    {(o.status === 'suspended' || o.status === 'rejected') && (
                      <Button size="sm" disabled={busy}
                        onClick={() => askConfirm(o, 'enable')}>
                        <Power className="w-4 h-4" /> Enable
                      </Button>
                    )}
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      <Dialog open={createOpen} onOpenChange={(o) => { if (!o) closeCreate() }}>
        <DialogContent>
          {created ? (
            <>
              <DialogHeader>
                <DialogTitle>Organization created</DialogTitle>
                <DialogDescription>
                  {created.tenant.name} is active. An owner invitation was emailed to {created.invite.email} —
                  they can set a password or sign in with Google/Microsoft. You can also share the link directly:
                </DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-2">
                <Input readOnly value={created.invite.link} className="text-xs" aria-label="Invite link" />
                <CopyLinkButton
                  url={created.invite.link}
                  label="Copy"
                  icon={Copy}
                  toastMessage="Invite link copied"
                  errorMessage="Could not copy the invite link — select it and copy it by hand"
                />
              </div>
              <div className="flex justify-end">
                <Button onClick={closeCreate}>Done</Button>
              </div>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>Create organization</DialogTitle>
                <DialogDescription>
                  The organization starts active, and the owner receives an invitation email —
                  they can join with a password or Google/Microsoft.
                </DialogDescription>
              </DialogHeader>
              <form
                className="space-y-3"
                onSubmit={(e) => { e.preventDefault(); createOrg.mutate() }}
              >
                <FormField label="Organization name" required>
                  <Input value={orgName} onChange={(e) => setOrgName(e.target.value)} placeholder="Acme Translations" />
                </FormField>
                <FormField label="Owner email" required>
                  <Input type="email" value={ownerEmail} onChange={(e) => setOwnerEmail(e.target.value)} placeholder="owner@acme.com" />
                </FormField>
                <FormField label="Email domain (optional — enables same-domain auto-join later)">
                  <Input value={orgDomain} onChange={(e) => setOrgDomain(e.target.value)} placeholder="acme.com" />
                </FormField>
                <div className="flex justify-end gap-2 pt-1">
                  <Button type="button" variant="outline" onClick={closeCreate}>Cancel</Button>
                  <Button type="submit" disabled={createOrg.isPending || !orgName.trim() || !ownerEmail.trim()}>
                    {createOrg.isPending ? 'Creating…' : 'Create & invite owner'}
                  </Button>
                </div>
              </form>
            </>
          )}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(o) => { if (!o) setConfirm(null) }}
        title={confirm ? CONFIRM_COPY[confirm.action].title : ''}
        description={confirm ? CONFIRM_COPY[confirm.action].body(confirm.org) : ''}
        actionLabel={confirm ? CONFIRM_COPY[confirm.action].label : ''}
        variant={confirm ? CONFIRM_COPY[confirm.action].variant : 'destructive'}
        onConfirm={() => { if (confirm) setStatus.mutate({ tenantId: confirm.tenantId, status: CONFIRM_COPY[confirm.action].status }) }}
      />
    </PageContainer>
  )
}
