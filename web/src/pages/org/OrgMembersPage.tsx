import { errorText } from '@/lib/error-text'
import { useState, useEffect } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Users, Mail, Send, Languages } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { hoverActionClass } from '@/components/list/hover-action'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { SettingsCard } from '@/components/layout/SettingsCard'
import ConfirmDialog from '@/components/ConfirmDialog'
import { isLinguistRole, roleLabel } from '../../lib/roles'
import { useScrollToError } from '../../hooks/useScrollToError'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { queryKeys } from '../../lib/query-keys'
import { toast } from 'sonner'
import { OrgPageFrame } from './OrgPageFrame'
import {
  listMembers,
  listInvites,
  createInvite,
  deleteInvite,
  resendInvite,
  updateMemberRole,
  listUserLanguagePairs,
  type Member,
  type Invite,
  type LanguagePair,
} from '../../api'
import LanguagePairsEditor, { rowsFromPairs, PairRowChip } from '../../components/userpair/LanguagePairsEditor'
import { Chip, type ChipColor } from '@/components/ui/chip'

// Owner is yellow because it's the role you should notice before changing
// anything; admin blue; the rest neutral. Labels live in lib/roles,
// so only the colour assignment is local.
const ROLE_COLORS: Record<string, ChipColor> = {
  owner: 'yellow',
  admin: 'blue',
}

/** Combobox options for a subset of roles, labelled from lib/roles. */
const roleOptions = (roles: string[]) =>
  roles.map(value => ({ value, label: roleLabel(value) }))

/** The Organization page's Members tab: members, their roles, and invites. Owner/admin. */
export default function OrgMembersPage() {
  const [members, setMembers] = useState<Member[]>([])
  const [invites, setInvites] = useState<Invite[]>([])
  const [pairsByUser, setPairsByUser] = useState<Record<number, LanguagePair[]>>({})
  const [loading, setLoading] = useState(true)
  // Which member's language-pair editor is open. null = closed.
  const [pairsEditorMember, setPairsEditorMember] = useState<Member | null>(null)
  // A pending downgrade-to-linguist awaiting confirmation. null = no dialog.
  const [linguistDowngrade, setLinguistDowngrade] = useState<{ member: Member; newRole: string } | null>(null)

  const applyRoleChange = async (member: Member, newRole: string) => {
    try {
      await updateMemberRole(member.id, newRole)
      setMembers(prev => prev.map(u => u.id === member.id ? { ...u, role: newRole } : u))
      toast.success(`${member.name} is now ${roleLabel(newRole)}.`)
    } catch (err) {
      toast.error(errorText(err, 'Failed to update role'))
    }
  }

  // Switching an existing member down to linguist adds them to the Vendors
  // list, so confirm first. Any other role change applies immediately.
  const requestRoleChange = (member: Member, newRole: string) => {
    if (newRole === member.role) return
    if (isLinguistRole(newRole) && !isLinguistRole(member.role)) {
      setLinguistDowngrade({ member, newRole })
      return
    }
    void applyRoleChange(member, newRole)
  }
  const inviteSchema = z.object({
    email: z.string().email('Valid email is required'),
    // Team invites. Vendors (linguists) are invited from the Vendors tab.
    role: z.enum(['admin', 'project_manager']),
  })
  type InviteFormValues = z.infer<typeof inviteSchema>
  const {
    register: registerInvite,
    control: controlInvite,
    handleSubmit: handleInviteSubmit,
    reset: resetInvite,
    formState: { errors: inviteErrors, isSubmitting: inviteSending },
  } = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { email: '', role: 'admin' },
  })
  useScrollToError(inviteErrors)

  const load = async () => {
    try {
      const [m, i] = await Promise.all([listMembers(), listInvites()])
      setMembers(m)
      setInvites(i)
      // Fetch each member's language pairs in parallel (member lists are small)
      // so we can show the qualifications inline without a bulk endpoint.
      const entries = await Promise.all(
        m.map(async mem => [mem.id, await listUserLanguagePairs(mem.id).catch(() => [])] as const),
      )
      setPairsByUser(Object.fromEntries(entries))
    } catch (err) {
      toast.error(errorText(err, 'Failed to load organization data'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // Refresh one member's pairs after their editor closes so the inline display
  // reflects any edits (the editor manages its own react-query cache).
  const refreshMemberPairs = async (userId: number) => {
    try {
      const p = await listUserLanguagePairs(userId)
      setPairsByUser(prev => ({ ...prev, [userId]: p }))
    } catch {
      /* non-fatal — inline display just stays stale until next load */
    }
  }

  const handleInvite = async (data: InviteFormValues) => {
    try {
      await createInvite(data.email.trim(), data.role)
      toast.success(`Invite sent to ${data.email.trim()}`)
      resetInvite()
      load()
    } catch (err) {
      toast.error(errorText(err, 'Failed to create invite'))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteInvite(id)
      toast.success('Invite revoked')
      load()
    } catch (err) {
      toast.error(errorText(err, 'Failed to revoke invite'))
    }
  }

  // One resend at a time: every Resend button waits while one is sending.
  const resendMut = useMutationWithToast({
    mutationFn: (inv: Invite) => resendInvite(inv.id),
    successMessage: (inv: Invite) => `Invite sent again to ${inv.email}`,
    errorMessage: 'Failed to resend invite',
    invalidateKeys: [queryKeys.invites.all],
    onSuccess: () => { load() },
  })

  if (loading) {
    return <OrgPageFrame><LoadingPlaceholder /></OrgPageFrame>
  }

  const pendingInvites = invites.filter(i => !i.accepted_at)
  const acceptedInvites = invites.filter(i => i.accepted_at)

  return (
    <OrgPageFrame>

      {/* Members */}
      <SettingsCard
        title="Members"
        icon={<Users />}
        info={<span className="text-sm text-gray-500">({members.length})</span>}
        className="mb-6"
        data-guide="org.members"
      >
        <div className="divide-y divide-gray-100 dark:divide-gray-800">
          {members.map(m => (
            <div key={m.id} data-testid={`member-row-${m.id}`} className="flex max-md:flex-wrap items-center justify-between gap-y-2 py-3">
              <div>
                <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{m.name}</span>
                <span className="text-sm text-gray-500 dark:text-gray-400 ml-2">{m.email}</span>
              </div>
              <div className="flex max-md:flex-wrap items-center gap-3">
                {(pairsByUser[m.id]?.length ?? 0) > 0 && (
                  <div className="flex flex-wrap gap-1 justify-end max-w-[360px]" data-testid={`pairs-display-${m.id}`}>
                    {/* One chip per row (grouped by source), matching the editor. */}
                    {rowsFromPairs(pairsByUser[m.id]).map((row, i) => <PairRowChip key={i} row={row} />)}
                  </div>
                )}
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setPairsEditorMember(m)}
                  data-testid={`pairs-button-${m.id}`}
                  className="text-xs text-gray-500 hover:text-primary"
                >
                  <Languages className="w-3 h-3 mr-1" />
                  {(pairsByUser[m.id]?.length ?? 0) > 0 ? 'Edit' : 'Language pairs'}
                </Button>
                <Combobox
                  value={m.role}
                  onChange={(newRole) => requestRoleChange(m, newRole)}
                  options={roleOptions(['owner', 'admin', 'project_manager', 'linguist'])}
                  className="h-7 px-2 text-xs font-medium"
                  data-testid={`member-role-${m.id}`}
                />
                <span className="text-xs text-gray-400">
                  Joined {new Date(m.created_at).toLocaleDateString()}
                </span>
              </div>
            </div>
          ))}
        </div>
      </SettingsCard>

      {/* Invite Form */}
      <SettingsCard title="Invite member" icon={<Mail />} className="mb-6" data-guide="org.invite">
        <form onSubmit={handleInviteSubmit(handleInvite)} className="flex max-md:flex-wrap gap-3 items-end">
          <FormField label="Email" error={inviteErrors.email?.message} required className="flex-1">
            <Input
              {...registerInvite('email')}
              type="email"
              placeholder="colleague@company.com"
            />
          </FormField>
          <FormField label="Role" error={inviteErrors.role?.message}>
            <Controller
              control={controlInvite}
              name="role"
              render={({ field }) => (
                <Combobox
                  value={field.value}
                  onChange={field.onChange}
                  options={roleOptions(['admin', 'project_manager'])}
                  className="h-9"
                />
              )}
            />
          </FormField>
          <Button type="submit" disabled={inviteSending}>
            {inviteSending ? 'Sending...' : 'Send invite'}
          </Button>
        </form>
      </SettingsCard>

      {/* Pending invites */}
      {pendingInvites.length > 0 && (
        <SettingsCard title={`Pending invites (${pendingInvites.length})`} className="mb-6">
          <div className="divide-y divide-gray-100 dark:divide-gray-800">
            {pendingInvites.map(inv => (
              <div key={inv.id} className="flex max-md:flex-wrap items-center justify-between gap-y-2 py-3">
                <div>
                  <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{inv.email}</span>
                  <span className="ml-2"><Chip color={ROLE_COLORS[inv.role] ?? 'grey'}>{roleLabel(inv.role)}</Chip></span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-gray-400">
                    Expires {new Date(inv.expires_at).toLocaleDateString()}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => resendMut.mutate(inv)}
                    disabled={resendMut.isPending}
                    loading={resendMut.isPending && resendMut.variables?.id === inv.id}
                    className={hoverActionClass('muted')}
                    aria-label="Resend invite"
                    title="Resend invite"
                    data-testid={`org-invite-resend-${inv.id}`}
                  >
                    <Send />
                  </Button>
                  <DeleteButton
                    onClick={() => handleDelete(inv.id)}
                    aria-label="Revoke invite"
                  />
                </div>
              </div>
            ))}
          </div>
        </SettingsCard>
      )}

      {/* Accepted invites (history) */}
      {acceptedInvites.length > 0 && (
        <SettingsCard title={`Accepted invites (${acceptedInvites.length})`}>
          <div className="divide-y divide-gray-100 dark:divide-gray-800">
            {acceptedInvites.map(inv => (
              <div key={inv.id} className="flex max-md:flex-wrap items-center justify-between gap-y-2 py-3">
                <div>
                  <span className="text-sm text-gray-600 dark:text-gray-400">{inv.email}</span>
                  <span className="ml-2"><Chip color={ROLE_COLORS[inv.role] ?? 'grey'}>{roleLabel(inv.role)}</Chip></span>
                </div>
                <span className="text-xs text-gray-400">
                  Accepted {new Date(inv.accepted_at!).toLocaleDateString()}
                </span>
              </div>
            ))}
          </div>
        </SettingsCard>
      )}
      {/* Language pairs editor modal — opened from a member row's "Language pairs" button.
          Conditional mount + key={id} forces a clean remount per member, sidestepping
          the stale-local-state problem when admin switches from one user's dialog to
          another without closing first. The component's local pairs[] state and its
          server-pairs sync effect both get fresh init on remount. */}
      {pairsEditorMember && (
        <LanguagePairsEditor
          key={pairsEditorMember.id}
          userId={pairsEditorMember.id}
          userName={pairsEditorMember.name}
          open={true}
          onOpenChange={open => {
            if (!open) {
              const id = pairsEditorMember.id
              setPairsEditorMember(null)
              void refreshMemberPairs(id)
            }
          }}
        />
      )}

      {linguistDowngrade && (
        <ConfirmDialog
          open={true}
          onOpenChange={open => { if (!open) setLinguistDowngrade(null) }}
          title="Change role to Linguist?"
          description={`${linguistDowngrade.member.name} will lose project-manager access and be added to the Vendors list as a linguist.`}
          actionLabel="Change to linguist"
          variant="default"
          onConfirm={() => applyRoleChange(linguistDowngrade.member, linguistDowngrade.newRole)}
        />
      )}
    </OrgPageFrame>
  )
}
