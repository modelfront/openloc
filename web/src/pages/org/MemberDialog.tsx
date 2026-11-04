import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  listUserLanguagePairs,
  replaceUserLanguagePairs,
  enableMember,
  updateMemberRole,
  type LanguagePair,
  type Member,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isLinguistRole, roleLabel } from '../../lib/roles'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button, NOTHING_CHANGED_REASON } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { LoadFailed } from '@/components/layout/LoadFailed'
import ConfirmDialog from '@/components/ConfirmDialog'
import { Alert } from '@/components/ui/alert'
import { EnableAccessButton } from './EnableAccessButton'
import LanguagePairsEditor, {
  MAX_PAIRS,
  pairsFromRows,
  rowsFromPairs,
  type PairRow,
} from '../../components/userpair/LanguagePairsEditor'

const pairKey = (p: LanguagePair) => `${p.source_lang}|${p.target_lang}|${p.row_group ?? 0}`

/** Same pairs in the same rows, in any order. */
function samePairs(a: LanguagePair[], b: LanguagePair[]): boolean {
  if (a.length !== b.length) return false
  const keys = new Set(a.map(pairKey))
  return b.every(p => keys.has(pairKey(p)))
}

/** What one Save sends: null for the part that did not change. */
interface MemberChanges {
  role: string | null
  pairs: LanguagePair[] | null
}

/**
 * One member's role and language pairs, saved together. Owner/admin, like the
 * Members tab that opens it. `onSaved` reports each part as soon as it is
 * stored, so a pairs failure after a role change still updates the list.
 */
export default function MemberDialog({ member, roles, onClose, onSaved }: {
  /** disabled_at: a vendor org disabled the membership for disuse. */
  member: Member & { disabled_at?: string | null }
  // The roles the picker offers (lib/roles memberRoles).
  roles: string[]
  onClose: () => void
  onSaved: (saved: { role?: string; pairs?: LanguagePair[] }) => void
}) {
  const { data: serverPairs, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.userLanguagePairs.byUser(member.id),
    queryFn: () => listUserLanguagePairs(member.id),
  })

  const [role, setRole] = useState(member.role)
  // Cleared once Enable succeeds, while the dialog stays open.
  const [disabled, setDisabled] = useState(!!member.disabled_at)
  // null until the user edits a row: until then the rows follow the saved pairs.
  const [editedRows, setEditedRows] = useState<PairRow[] | null>(null)
  const rows = editedRows ?? (serverPairs ? rowsFromPairs(serverPairs) : null)
  const pairs = rows ? pairsFromRows(rows) : null

  const roleChanged = role !== member.role
  // The pairs replace the whole saved set, so they only count once that set
  // has loaded: before then the rows are a placeholder that would erase it.
  const pairsChanged = !!pairs && !!serverPairs && !samePairs(pairs, serverPairs)
  const changes: MemberChanges = { role: roleChanged ? role : null, pairs: pairsChanged ? pairs : null }

  const warnings = [
    // A member switched down to linguist loses the project-manager pages.
    roleChanged && isLinguistRole(role) && !isLinguistRole(member.role) &&
      `${member.name} will lose project-manager access and see only the tasks assigned to them.`,
    pairsChanged && pairs.length === 0 &&
      `All ${serverPairs.length} language pair${serverPairs.length === 1 ? '' : 's'} will be removed. ${member.name} will no longer appear in workflow-assignment dropdowns filtered by language pair until pairs are re-added.`,
  ].filter((w): w is string => !!w)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const saveMut = useMutationWithToast({
    mutationFn: async (c: MemberChanges) => {
      if (c.role) {
        await updateMemberRole(member.id, c.role)
        onSaved({ role: c.role })
      }
      if (c.pairs) {
        onSaved({ pairs: await replaceUserLanguagePairs(member.id, c.pairs) })
      }
    },
    successMessage: (_, c) => [
      c.role && `${member.name} is now ${roleLabel(c.role)}.`,
      c.pairs && 'Language pairs saved.',
    ].filter(Boolean).join(' '),
    errorMessage: 'Failed to save the member',
    invalidateKeys: [
      queryKeys.members.all,
      queryKeys.userLanguagePairs.byUser(member.id),
      // Qualified-member lists for any source/target combination may have changed.
      queryKeys.usersByPair.all,
    ],
    onSuccess: onClose,
  })

  const handleSave = () => {
    if (pairsChanged && pairs.length > MAX_PAIRS) {
      toast.error(`Max ${MAX_PAIRS} pairs per user — that's ${pairs.length}. Trim some rows.`)
      return
    }
    if (warnings.length > 0) {
      setConfirmOpen(true)
      return
    }
    saveMut.mutate(changes)
  }

  const onlyLinguistWarning = warnings.length === 1 && roleChanged && isLinguistRole(role)

  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }} keepOpen={saveMut.isPending}>
      <DialogContent className="sm:max-w-3xl" data-testid="member-dialog">
        <DialogHeader>
          <DialogTitle>{member.name}</DialogTitle>
          <DialogDescription className="break-all">{member.email}</DialogDescription>
        </DialogHeader>

        <div className="space-y-5">
          {disabled && (
            <Alert tone="warning" title="Access disabled" data-testid="member-access-disabled">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span>A member who does not sign in for 90 days is disabled.</span>
                <EnableAccessButton email={member.email} enable={() => enableMember(member.id)} onEnabled={() => setDisabled(false)} />
              </div>
            </Alert>
          )}
          <FormField label="Role" className="max-w-xs">
            <Combobox
              value={role}
              onChange={setRole}
              options={roles.map(value => ({ value, label: roleLabel(value) }))}
              searchable={false}
              className="w-full"
              data-testid="member-role"
            />
          </FormField>

          <FormField label="Language pairs">
            <div className="pt-2">
              {isLoading ? (
                <div className="py-6 text-sm text-gray-500">Loading…</div>
              ) : isError && !serverPairs ? (
                <LoadFailed what="language pairs" error={error} onRetry={() => refetch()} />
              ) : rows && (
                <LanguagePairsEditor rows={rows} onChange={setEditedRows} />
              )}
            </div>
          </FormField>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={saveMut.isPending}>
            Cancel
          </Button>
          <Button
            onClick={handleSave}
            disabled={!roleChanged && !pairsChanged}
            disabledReason={NOTHING_CHANGED_REASON}
            loading={saveMut.isPending}
            data-testid="member-save"
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={
          warnings.length > 1 ? 'Save these changes?'
            : onlyLinguistWarning ? 'Change role to Linguist?'
            : `Remove all language pairs for ${member.name}?`
        }
        description={warnings.join(' ')}
        actionLabel={warnings.length > 1 ? 'Save' : onlyLinguistWarning ? 'Change to linguist' : 'Remove all'}
        variant={onlyLinguistWarning ? 'default' : undefined}
        onConfirm={() => saveMut.mutate(changes)}
      />
    </Dialog>
  )
}
