import { type FormEventHandler, type ReactNode } from 'react'
import { X } from 'lucide-react'
import type { UseFormRegisterReturn } from 'react-hook-form'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { InlineNameField } from '@/components/list/InlineNameField'

/**
 * A shared edit/new form shell: a compact header row with an inline editable
 * name on the left and Save / Delete / Cancel(✕) on the right, a wall-to-wall
 * divider, then the form's detail fields below (children). Drop it around any
 * "edit or create one entity" form so the new/edit UX is identical everywhere.
 * First used by the AI engine, system-prompt, segment-prompt and provider forms.
 */
export function EditPanel({
  testId, name, register, namePlaceholder, canEditName = true, nameTrailing,
  onSubmit, onCancel, onDelete, saveLabel, saving = false, readOnly = false,
  extraActions, children,
}: {
  testId: string
  name: string
  register: UseFormRegisterReturn
  namePlaceholder?: string
  canEditName?: boolean
  /** Optional element shown right after the name (e.g. a mode chip). */
  nameTrailing?: ReactNode
  onSubmit: FormEventHandler<HTMLFormElement>
  onCancel: () => void
  /** When set (and not read-only) a trash button appears next to Save. */
  onDelete?: () => void
  saveLabel: string
  saving?: boolean
  /** Read-only view (e.g. a built-in default): no Save/Delete; ✕ just closes. */
  readOnly?: boolean
  /** Extra buttons before Save (e.g. Duplicate on a read-only default). */
  extraActions?: ReactNode
  children: ReactNode
}) {
  return (
    <form
      onSubmit={onSubmit}
      // Esc cancels while focus is anywhere in the form. An open combobox portals
      // its own Esc handling, so this only fires when none is open.
      onKeyDown={e => { if (e.key === 'Escape') { e.stopPropagation(); onCancel() } }}
      className="px-4 pb-4 bg-card rounded-lg border border-border mb-3 space-y-3"
      data-testid={testId}
    >
      <div className="flex items-center justify-between gap-3 -mx-4 px-4 py-1.5 border-b border-border">
        <div className="min-w-0 flex-1">
          <InlineNameField
            value={name}
            register={register}
            placeholder={namePlaceholder}
            canEdit={canEditName && !readOnly}
            trailing={nameTrailing}
          />
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {extraActions}
          {!readOnly && (
            <Button type="submit" disabled={saving}>{saving ? 'Saving…' : saveLabel}</Button>
          )}
          {onDelete && !readOnly && (
            <DeleteButton
              type="button"
              onClick={onDelete} aria-label="Delete" title="Delete"
            />
          )}
          <Button variant="ghost" size="icon"
            type="button" className="bg-muted/60"
            onClick={onCancel} aria-label="Cancel" title="Cancel"
          >
            <X />
          </Button>
        </div>
      </div>
      {children}
    </form>
  )
}
