// The edit menu on a dashboard title and on each chart card: rename, and the
// list of filters. The filter bars carry only the dropdowns themselves.
//
// Filters are CHECKBOXES rather than separate "add" and "remove" items: one
// checked list says which filters are on. Ticking adds; unticking removes
// through a confirmation, because removing FREEZES the current value rather
// than widening the chart. A bar that does not freeze (a pinned dashboard,
// whose charts are not its own) removes at once.
import { useState } from 'react'
import { Pencil } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { hoverActionClass } from '@/components/list/hover-action'
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuCheckboxItem, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import ConfirmDialog from '@/components/ConfirmDialog'
import { CONTROL_LABELS, controlValue } from './DashboardFilterBar'
import type { DashboardFilterValues } from '@/api'

export function FilterEditMenu({
  label, onRename, available, active, values, inheritedControls, onAdd, onRemove, testId, freezes = true,
}: {
  /** What this menu edits — "dashboard" or the chart's name — for the copy. */
  label: string
  onRename: () => void
  /** Controls this bar could offer, from the server. */
  available: string[]
  /** Controls currently on the bar. */
  active: string[]
  /** Current selections, so removal can name the value it will freeze. */
  values: DashboardFilterValues
  /** Dashboard controls, when this menu edits a CHART: unticking one the
   *  dashboard also offers hands the chart back rather than freezing. */
  inheritedControls?: string[]
  onAdd: (control: string) => void
  onRemove: (control: string, value: string) => void
  testId: string
  /** False when removing a control leaves the charts untouched. */
  freezes?: boolean
}) {
  const [confirming, setConfirming] = useState<string | null>(null)
  const inherits = !!confirming && !!inheritedControls?.includes(confirming)
  const frozenValue = confirming ? controlValue(confirming, values) : ''
  const confirmingLabel = confirming ? (CONTROL_LABELS[confirming] ?? confirming) : ''

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              className={hoverActionClass('muted')}
              title={`Edit ${label}`}
              aria-label={`Edit ${label}`}
              data-testid={testId}
            />
          }
        >
          <Pencil className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem data-testid={`${testId}-rename`} onClick={onRename}>
            Rename
          </DropdownMenuItem>
          {/* Labelled, because the menu mixes kinds: without it the ticks read
              as options for "Rename" rather than as this bar's filters. */}
          {available.length > 0 && <DropdownMenuSeparator />}
          {available.length > 0 && (
            <DropdownMenuGroup>
              <DropdownMenuLabel>Filters</DropdownMenuLabel>
              {available.map(control => {
                const on = active.includes(control)
                return (
                  <DropdownMenuCheckboxItem
                    key={control}
                    checked={on}
                    data-testid={`${testId}-filter-${control.replace(/_/g, '-')}`}
                    onClick={() => (!on ? onAdd(control) : freezes ? setConfirming(control) : onRemove(control, ''))}
                  >
                    {CONTROL_LABELS[control] ?? control}
                  </DropdownMenuCheckboxItem>
                )
              })}
            </DropdownMenuGroup>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={open => !open && setConfirming(null)}
        title={`Remove the ${confirmingLabel} filter?`}
        description={
          inherits
            ? `This chart will follow the dashboard's ${confirmingLabel} filter instead.`
            : frozenValue
              ? `The dropdown goes away and it stays fixed at "${frozenValue}". Charts keep showing exactly what they show now — they just can't be switched from here.`
              : 'The dropdown goes away. Nothing is currently selected, so no chart changes.'
        }
        actionLabel="Remove"
        testId={`${testId}-remove-confirm`}
        onConfirm={() => onRemove(confirming!, frozenValue)}
      />
    </>
  )
}
