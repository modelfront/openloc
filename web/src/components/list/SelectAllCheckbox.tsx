// The header checkbox of a selectable list: checked when every row shown is
// selected, a dash (indeterminate) when only some are. The Files table, the
// Tasks matrix and the CAT editor grid share it.

interface Props {
  checked: boolean
  /** Some, not all, of the rows shown are selected. */
  indeterminate: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  'aria-label': string
  'data-testid'?: string
}

export function SelectAllCheckbox({ checked, indeterminate, onChange, disabled, 'aria-label': ariaLabel, 'data-testid': testId }: Props) {
  return (
    <input
      type="checkbox"
      checked={checked}
      // indeterminate is a DOM property with no HTML attribute.
      ref={el => { if (el) el.indeterminate = indeterminate && !checked }}
      onChange={e => onChange(e.target.checked)}
      disabled={disabled}
      className="size-3.5 cursor-pointer accent-primary"
      aria-label={ariaLabel}
      data-testid={testId}
    />
  )
}
