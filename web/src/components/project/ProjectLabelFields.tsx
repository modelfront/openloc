// ProjectLabelFields — the project's own label picks: one field per label the
// user has added, each a single-select over that label's allowed values.
//
// Renders grid CELLS, not a grid: the caller drops it into the same
// two-column grid that holds Domain and Customer, so a label lines up with
// those fields and inherits their gap instead of starting a second grid
// flush against the first. The Add control spans both columns, so it sits on
// its own row under the fields however many there are.
//
// A field exists before it has a value, so "add a label" and "choose its value"
// stay two steps; one with no value yet is simply not sent. Add label opens a
// dialog listing only labels that have no field yet, so the same label cannot
// appear twice — which is also what the database enforces.

import { useState } from 'react'
import { Plus, X } from 'lucide-react'
import { Combobox } from '@/components/ui/combobox'
import { Button } from '@/components/ui/button'
import { FormField } from '@/components/ui/form-field'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { listRowClass } from '@/components/list/list-row'
import { EmptyState } from '@/components/layout/EmptyState'
import { cn } from '@/lib/utils'
import type { Label } from '../../api'

/** One row: a label, and the value chosen for it (null until chosen). */
export interface LabelPick {
  definition_id: number
  value_id: number | null
}

interface Props {
  /** Every label the tenant has defined. */
  labels: Label[]
  value: LabelPick[]
  onChange: (next: LabelPick[]) => void
  disabled?: boolean
}

export function ProjectLabelFields({ labels, value, onChange, disabled = false }: Props) {
  const [picking, setPicking] = useState(false)

  const byId = new Map(labels.map(l => [l.id, l]))
  const picked = new Set(value.map(p => p.definition_id))
  // Customer and Domain are always on the form, answered or not — they read as
  // fields of a project, not as something you opt into. Every other label
  // appears once it has been added.
  const rows: LabelPick[] = [
    ...labels.filter(l => l.system && !picked.has(l.id)).map(l => ({ definition_id: l.id, value_id: null })),
    ...value,
  ].sort((a, b) => {
    const la = byId.get(a.definition_id)
    const lb = byId.get(b.definition_id)
    if (!la || !lb) return 0
    if (la.system !== lb.system) return la.system ? -1 : 1
    return la.name.localeCompare(lb.name)
  })
  const available = labels.filter(l => !l.system && !picked.has(l.id))

  const setValue = (definitionId: number, valueId: number | null) =>
    onChange(
      value.some(p => p.definition_id === definitionId)
        ? value.map(p => (p.definition_id === definitionId ? { ...p, value_id: valueId } : p))
        : [...value, { definition_id: definitionId, value_id: valueId }],
    )

  const removeRow = (definitionId: number) =>
    onChange(value.filter(p => p.definition_id !== definitionId))

  const addRow = (definitionId: number) => {
    setPicking(false)
    if (picked.has(definitionId)) return
    onChange([...value, { definition_id: definitionId, value_id: null }])
  }

  return (
    <>
      {rows.map(pick => {
        const label = byId.get(pick.definition_id)
        // A label retired while the form was open: the field would render
        // nameless and its value picker empty, so drop it rather than show a
        // field nobody can fill.
        if (!label) return null
        return (
          <FormField key={pick.definition_id} label={label.name} className="min-w-0">
            <div className="flex items-center gap-1">
              <Combobox
                className="min-w-0 flex-1"
                // Only the built-ins: their row is permanent, so clearing the
                // value is the only way to leave one unanswered. A user label
                // already has a × that removes the whole row, and two × in one
                // field reads as a choice where there is none.
                clearable={label.system}
                options={label.values.map(v => ({ value: String(v.id), label: v.value }))}
                value={pick.value_id == null ? '' : String(pick.value_id)}
                onChange={v => setValue(pick.definition_id, v ? Number(v) : null)}
                placeholder={`No ${label.name.toLowerCase()}`}
                emptyText="This label has no values yet"
                disabled={disabled}
                data-testid={`project-label-value-${pick.definition_id}`}
              />
              {/* A built-in label is part of every project, so there is
                  nothing to remove — clearing its value is the way to leave it
                  unanswered. */}
              {!disabled && !label.system && (
                <Button
                  variant="ghost"
                  size="icon-xs"
                  type="button"
                  color="muted"
                  hoverColor="red"
                  onClick={() => removeRow(pick.definition_id)}
                  aria-label={`Remove ${label.name}`}
                  data-testid={`project-label-remove-${pick.definition_id}`}
                >
                  <X className="w-3.5 h-3.5" />
                </Button>
              )}
            </div>
          </FormField>
        )
      })}

      {!disabled && available.length > 0 && (
        <div className="sm:col-span-2" data-testid="project-label-fields">
          <Button type="button" variant="outline" size="sm" onClick={() => setPicking(true)} data-testid="add-project-label">
            <Plus />
            Add label
          </Button>
        </div>
      )}

      <Dialog open={picking} onOpenChange={o => { if (!o) setPicking(false) }}>
        {/* Enter is handled here, not on a row: the form this sits inside would
            otherwise receive it and submit. */}
        <DialogContent
          className="sm:max-w-md"
          data-testid="project-label-picker"
          onKeyDown={e => { if (e.key === 'Enter') e.stopPropagation() }}
        >
          <DialogHeader>
            <DialogTitle>Add label</DialogTitle>
          </DialogHeader>

          {available.length === 0 ? (
            <EmptyState message="Every label is already on this project." />
          ) : (
            <div className="flex flex-col gap-1.5">
              {available.map(l => (
                <Button
                  key={l.id}
                  type="button"
                  variant="outline"
                  // listRowClass last so its justify/padding/height win the
                  // merge over the button's centred defaults — the row reads as
                  // a list row that happens to be clickable.
                  className={cn('w-full font-normal hover:border-primary transition-colors', listRowClass)}
                  onClick={() => addRow(l.id)}
                  data-testid={`project-label-option-${l.id}`}
                >
                  <span className="truncate font-medium">{l.name}</span>
                  <span className="shrink-0 text-xs text-gray-500 dark:text-gray-400">
                    {l.values.length === 1 ? '1 value' : `${l.values.length} values`}
                  </span>
                </Button>
              ))}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setPicking(false)} data-testid="project-label-picker-cancel">
              Cancel
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
