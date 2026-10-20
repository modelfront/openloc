// LabelManager — the tenant's labels and the values each one allows. Shared by
// the Labels page and the Manage labels dialog on the New project form, so the
// two can never drift.
//
// One row per label: its name, and a MultiCombobox holding every value the
// label offers. The picker is in `onCreateOption` mode, so typing a value that
// does not exist yet offers to add it — the values ARE free text, so a create
// dialog would be a form around a single string. Ticking and unticking in that
// picker is what adds and deletes a value: everything listed is selected, and
// clearing a chip deletes it. A value projects use asks first, because those
// projects lose it; an unused one goes at once.
//
// A label can be deleted once none of its values is in use. Until then the bin
// is disabled and its tooltip names the values holding it.
//
// Customer and Domain are built in (`system`) and listed first. Their values
// work like any other label's; only the label itself is protected, so the
// absence of a bin is what marks them.
//
// A manager adds labels and values; removing either is admin-or-above, so below
// that the chips carry no × and the bins are not rendered.

import { useState } from 'react'
import { Plus } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import {
  listLabels,
  createLabel,
  deleteLabel,
  createLabelValue,
  deleteLabelValue,
  type Label,
  type LabelValue,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../ConfirmDialog'
import { labelDeleteBlocker, projectsCount } from './label-delete'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { MultiCombobox } from '@/components/ui/combobox'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { EditableTitle } from '@/components/list/EditableTitle'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'

interface Props {
  /** False hides every editing control — the list stays readable. */
  canEdit?: boolean
  /**
   * Removing anything — a value or a whole label — is admin-or-above
   * server-side, so the bin and the values' × follow. Showing either to a
   * manager would offer an action the API refuses.
   */
  canDelete?: boolean
  /** Skip the query until the surface holding this is actually visible. */
  enabled?: boolean
}

export function LabelManager({ canEdit = true, canDelete = false, enabled = true }: Props) {
  const { data: labels, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.labels.all,
    queryFn: listLabels,
    enabled,
  })

  const [newLabelName, setNewLabelName] = useState('')
  const [adding, setAdding] = useState(false)
  const [pendingRemoval, setPendingRemoval] = useState<{ labelId: number; value: LabelValue } | null>(null)

  const invalidate = [queryKeys.labels.all]

  const createMut = useMutationWithToast({
    mutationFn: (name: string) => createLabel(name),
    successMessage: 'Label created',
    errorMessage: 'Failed to create label',
    invalidateKeys: invalidate,
    onSuccess: () => {
      setNewLabelName('')
      setAdding(false)
    },
  })

  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => deleteLabel(id),
    successMessage: 'Label deleted',
    errorMessage: 'Failed to delete label',
    invalidateKeys: invalidate,
  })

  const addValueMut = useMutationWithToast({
    mutationFn: ({ labelId, value }: { labelId: number; value: string }) => createLabelValue(labelId, value),
    successMessage: 'Value added',
    errorMessage: 'Failed to add value',
    invalidateKeys: invalidate,
  })

  const removeValueMut = useMutationWithToast({
    mutationFn: ({ labelId, valueId }: { labelId: number; valueId: number }) => deleteLabelValue(labelId, valueId),
    successMessage: 'Value deleted',
    errorMessage: 'Failed to delete value',
    // A deleted value is unset on every project and template that picked it,
    // so their caches go stale with the label list.
    invalidateKeys: [
      queryKeys.labels.all, queryKeys.projects.all, queryKeys.templates.all,
      queryKeys.projectLabels.all, queryKeys.templateLabels.all,
    ],
  })

  // The picker reports the values that should remain. Anything missing from
  // that list was unticked, so it is the one to delete — comparing against the
  // label's current values is what turns a selection into an edit.
  const applyValueSelection = (label: Label, next: string[]) => {
    const keep = new Set(next)
    for (const v of label.values) {
      if (keep.has(String(v.id))) continue
      if (v.in_use) {
        setPendingRemoval({ labelId: label.id, value: v })
      } else {
        removeValueMut.mutate({ labelId: label.id, valueId: v.id })
      }
    }
  }

  const submitNewLabel = () => {
    const name = newLabelName.trim()
    if (name) createMut.mutate(name)
  }

  if (isLoading) return <LoadingPlaceholder />
  if (isError && !labels) return <LoadFailed what="labels" error={error} onRetry={() => refetch()} />

  return (
    <TooltipProvider delay={200}>
    <div className="space-y-3" data-testid="label-manager">
      {(labels ?? []).length === 0 && !adding && (
        <EmptyState message="No labels yet. Add one to start classifying projects." />
      )}

      {/* One grid for the whole list, not one per row: the bin column is `auto`,
          so a per-row grid sizes it to that row's own content and the value
          fields of rows without a bin sit ~11px left of the ones with. The row
          wrapper is `contents` so it keeps its test id without becoming a grid
          item itself. */}
      <div className="grid grid-cols-1 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto] items-center gap-x-2 gap-y-3">
      {(labels ?? []).map(label => (
        <div key={label.id} className="contents" data-testid={`label-row-${label.id}`}>
          {/* A label's name is fixed once it exists: projects already carry
              it, so renaming would rewrite what they were classified as.
              Delete the label and add another instead. */}
          <EditableTitle
            name={label.name}
            canEdit={false}
            className="text-sm"
            data-testid={`label-name-${label.id}`}
          />
          <MultiCombobox
            // Deleting a value is admin-or-above server-side, so below that
            // the rows are fixed rather than an action that fails.
            options={label.values.map(v => ({
              value: String(v.id),
              label: v.value,
              disabled: !canDelete,
              chip: v.in_use ? 'In use' : undefined,
            }))}
            value={label.values.map(v => String(v.id))}
            onChange={next => applyValueSelection(label, next)}
            onCreateOption={value => addValueMut.mutate({ labelId: label.id, value })}
            createOptionLabel='Add "{}"'
            placeholder="No values"
            searchPlaceholder="Type to add a new option"
            // The search box directly above already says what to do, so the
            // empty state states the fact instead of repeating it.
            emptyText="No values yet"
            disabled={!canEdit}
            className="w-full"
            data-testid={`label-values-${label.id}`}
          />
          {/* Built-in labels have no bin at all: the server refuses to delete
              them, so offering one would be an action that always fails. The
              empty span still occupies the cell — with `contents` rows, a row
              that contributes two items instead of three shifts every row
              after it one column along. */}
          {canDelete && !label.system ? (
            <LabelDeleteButton label={label} onDelete={() => deleteMut.mutate(label.id)} />
          ) : (
            <span aria-hidden />
          )}
        </div>
      ))}
      </div>

      {/* Enter submits from the wrapper, not the field: no nested <form>, and
          the keypress must not reach a form this is rendered inside
          (as in CreateCustomerDialog). */}
      {adding && (
        <div
          className="flex items-center gap-2"
          onKeyDown={e => {
            if (e.key === 'Enter') {
              e.preventDefault()
              e.stopPropagation()
              submitNewLabel()
            }
          }}
        >
          <Input
            value={newLabelName}
            onChange={e => setNewLabelName(e.target.value)}
            placeholder="Label name (e.g. Country)"
            maxLength={200}
            autoFocus
            data-testid="new-label-name"
          />
          {/* `sm` is the stock size nearest the 30px field beside them: 2px
              shorter reads as inset, where the default size is 2px taller and
              sticks out of the row. */}
          <Button type="button" size="sm" onClick={submitNewLabel} disabled={createMut.isPending} data-testid="new-label-submit">
            {createMut.isPending ? 'Adding…' : 'Add'}
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => { setAdding(false); setNewLabelName('') }}>
            Cancel
          </Button>
        </div>
      )}

      {canEdit && !adding && (
        <Button type="button" variant="outline" size="sm" className="justify-self-start" onClick={() => setAdding(true)} data-testid="add-label-button">
          <Plus />
          Add label
        </Button>
      )}
      <ConfirmDialog
        open={pendingRemoval !== null}
        onOpenChange={open => { if (!open) setPendingRemoval(null) }}
        title="Delete value?"
        description={pendingRemoval
          ? `${pendingRemoval.value.value} is set on ${projectsCount(pendingRemoval.value.project_count)}. Deleting it removes it from them.`
          : ''}
        onConfirm={() => {
          if (pendingRemoval) removeValueMut.mutate({ labelId: pendingRemoval.labelId, valueId: pendingRemoval.value.id })
        }}
      />
    </div>
    </TooltipProvider>
  )
}

/** The label's bin. Disabled while a value is in use, with the reason in the tooltip. */
function LabelDeleteButton({ label, onDelete }: { label: Label; onDelete: () => void }) {
  const blocker = labelDeleteBlocker(label)
  return (
    <Tooltip>
      {/* The span, not the button, is the trigger: a disabled button fires no
          pointer events, so a tooltip on it would never open — which is
          exactly when the user needs to be told why. */}
      <TooltipTrigger render={<span className="inline-flex" />}>
        <DeleteButton
          onClick={onDelete}
          disabled={blocker !== null}
          aria-label={`Delete label ${label.name}`}
          data-testid={`label-delete-${label.id}`}
        />
      </TooltipTrigger>
      <TooltipContent data-testid={`label-delete-tooltip-${label.id}`}>
        {blocker ?? `Delete ${label.name}`}
      </TooltipContent>
    </Tooltip>
  )
}
