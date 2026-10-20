import { changeIssueKind, type IssueDetail, type IssueKind } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { useSavedFlash } from '@/hooks/useSavedFlash'
import { ISSUE_KIND_OPTIONS } from '@/lib/issue-labels'
import { Combobox } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { SaveStatus } from '@/components/ui/save-status'

/** The kind picker, the same on both sides: a pick saves at once. */
export function IssueKindSelect({ value, onChange, disabled }: {
  value: IssueKind
  onChange: (kind: IssueKind) => void
  disabled?: boolean
}) {
  return (
    <Combobox value={value} options={ISSUE_KIND_OPTIONS} searchable={false}
      disabled={disabled} className="w-full" aria-label="Kind" data-testid="issue-kind-select"
      onChange={v => onChange(v as IssueKind)} />
  )
}

/**
 * The reporter's side (the reporter, or a manager who sees the issue) changes
 * its kind; the support team does it in their triage fields. A bug starts at
 * Major; any other kind has no severity. Saved at once, with "Saved ✓".
 */
export function IssueKindForm({ issueId, kind, onUpdated }: {
  issueId: number
  kind: IssueKind
  onUpdated: (d: IssueDetail) => void
}) {
  const [saved, flashSaved] = useSavedFlash()
  const mut = useMutationWithToast({
    mutationFn: (next: IssueKind) => changeIssueKind(issueId, { kind: next }),
    successMessage: () => null,
    onSuccess: d => { onUpdated(d); flashSaved() },
  })
  return (
    <section className="flex flex-wrap items-end gap-2" data-testid="issue-kind-form">
      <FormField label="Kind" className="w-56">
        <IssueKindSelect value={kind} disabled={mut.isPending} onChange={next => { if (next !== kind) mut.mutate(next) }} />
      </FormField>
      <SaveStatus saving={mut.isPending} saved={saved} testId="issue-kind-save-status" />
    </section>
  )
}
