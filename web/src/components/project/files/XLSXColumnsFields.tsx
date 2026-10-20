// What to translate in an Excel workbook without language codes in row 1:
// one collapsed row per sheet with its choice as a summary; open, it has the
// columns to translate (none: every text cell of the sheet) and the header
// row. Shared by the upload dialog and the New project wizard's Excel columns.

import { useId, useMemo, useState } from 'react'

import type { XLSXInspection, XLSXSheetInfo } from '@/api'
import { columnLabel, sheetColumns, sheetSummary, type SheetChoice } from '@/lib/xlsx-columns'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { MultiCombobox, type ComboboxOption } from '@/components/ui/combobox'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { FormField } from '@/components/ui/form-field'
import { TextLink } from '@/components/ui/text-link'

interface Props {
  inspection: XLSXInspection
  choices: SheetChoice[]
  onChange: (choices: SheetChoice[]) => void
  /** Prefixes the rows' test ids, to tell several workbooks apart. */
  testId?: string
}

export function XLSXColumnsFields({ inspection, choices, onChange, testId = 'xlsx' }: Props) {
  const update = (name: string, patch: Partial<SheetChoice>) =>
    onChange(choices.map(c => (c.name === name ? { ...c, ...patch } : c)))
  return (
    <ul className="divide-y divide-gray-200 rounded-lg border border-gray-200 dark:divide-gray-800 dark:border-gray-800">
      {choices.map(c => (
        <SheetRow
          key={c.name}
          choice={c}
          sheet={inspection.sheets.find(s => s.name === c.name)}
          // A single sheet is always translated.
          canLeaveOut={choices.length > 1}
          onChange={patch => update(c.name, patch)}
          testId={`${testId}-sheet-${c.name}`}
        />
      ))}
    </ul>
  )
}

interface SheetRowProps {
  choice: SheetChoice
  sheet: XLSXSheetInfo | undefined
  canLeaveOut: boolean
  onChange: (patch: Partial<SheetChoice>) => void
  testId: string
}

function SheetRow({ choice: c, sheet, canLeaveOut, onChange, testId }: SheetRowProps) {
  const [open, setOpen] = useState(false)
  const bodyId = useId()
  const columns = useMemo(() => sheetColumns(sheet, c.columns), [sheet, c.columns])
  const options: ComboboxOption[] = useMemo(
    () => columns.map(col => ({ value: col.letter, label: columnLabel(col), shortLabel: col.letter })),
    [columns],
  )
  const count = columns.length
  // Select all picks every column the sheet offers; once all are picked, Clear
  // empties the field, which translates the whole sheet.
  const allPicked = count > 0 && c.columns.length >= count
  const selectAll = count > 1 && (
    <TextLink
      onClick={() => onChange({ columns: allPicked ? [] : columns.map(col => col.letter) })}
      data-testid={`${testId}-select-all`}
    >
      {allPicked ? 'Clear' : 'Select all'}
    </TextLink>
  )

  return (
    <li className="px-3 py-2" data-testid={testId}>
      <div className="flex items-center gap-2">
        {canLeaveOut && (
          <input
            type="checkbox"
            checked={c.included}
            onChange={e => onChange({ included: e.target.checked })}
            className="size-3.5 cursor-pointer accent-primary"
            aria-label={`Translate sheet ${c.name}`}
            data-testid={`${testId}-include`}
          />
        )}
        <DisclosureToggle
          variant="row"
          open={open && c.included}
          onToggle={() => setOpen(o => !o)}
          disabled={!c.included}
          controls={bodyId}
          summary={sheetSummary(c, count)}
          data-testid={`${testId}-toggle`}
        >
          {c.name}
        </DisclosureToggle>
      </div>
      {open && c.included && (
        <div id={bodyId} className="mt-2 space-y-2 pl-6">
          <FormField label="Columns to translate" action={selectAll || undefined}>
            <MultiCombobox
              options={options}
              className="w-full"
              value={c.columns}
              onChange={columns => onChange({ columns })}
              placeholder="Whole sheet"
              aria-label={`Columns to translate on sheet ${c.name}`}
              data-testid={`${testId}-columns`}
            />
          </FormField>
          <CheckboxField
            size="compact"
            checked={c.header_row}
            onChange={header_row => onChange({ header_row })}
            label="Row 1 is a header"
            description="Its cells are column titles, not text to translate."
          />
        </div>
      )}
    </li>
  )
}
