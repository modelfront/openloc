// Native stand-ins for the cmdk pickers, which are hostile in jsdom.
// vi.mock factories are hoisted, so import this
// dynamically inside the factory:
//
//   vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)
//
// `comboboxSelectMock` renders Combobox as a <select> (drive it with
// `selectOptions`; each action is an option valued `__action__:<key>` that
// runs the action when picked) and MultiCombobox as a comma-joined <input>.
// `comboboxInputMock` renders both as <input>s for forms that are driven by
// typing a code.
import type { ComboboxAction, ComboboxProps, MultiComboboxProps } from '@/components/ui/combobox'

const createAction = (label: string, run: () => Promise<number | null>, onCreated: (id: number) => void): ComboboxAction => ({
  key: 'create',
  label,
  onSelect: async () => {
    const id = await run()
    if (id != null) onCreated(id)
  },
})

function SelectCombobox({ value, onChange, options, actions, disabled, className, placeholder, 'data-testid': testId }: ComboboxProps) {
  const hasEmptyOption = options.some((o) => o.value === '')
  return (
    <select
      data-testid={testId}
      aria-label={placeholder}
      value={value}
      disabled={disabled}
      className={className}
      onChange={(e) => {
        const v = e.target.value
        const action = actions?.find((a) => `__action__:${a.key}` === v)
        if (action) action.onSelect()
        else if (v !== '' || hasEmptyOption) onChange(v)
      }}
    >
      {/* A real Combobox shows the placeholder when nothing is picked; a native
          select would silently pick the first option instead. */}
      {!hasEmptyOption && <option value="">{placeholder ?? 'Select…'}</option>}
      {options.map((o) => (
        <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>
      ))}
      {actions?.map((a) => (
        <option key={a.key} value={`__action__:${a.key}`}>{a.label}</option>
      ))}
    </select>
  )
}

function JoinedInput({ value, onChange, placeholder, 'data-testid': testId }: MultiComboboxProps) {
  return (
    <input
      aria-label={placeholder}
      data-testid={testId}
      value={(value ?? []).join(',')}
      onChange={(e) => onChange(e.target.value ? e.target.value.split(',') : [])}
    />
  )
}

function TextInput({ value, onChange, placeholder, 'data-testid': testId }: ComboboxProps) {
  return <input aria-label={placeholder} data-testid={testId} value={value} onChange={(e) => onChange(e.target.value)} />
}

export const comboboxSelectMock = { Combobox: SelectCombobox, MultiCombobox: JoinedInput, createAction }
export const comboboxInputMock = { Combobox: TextInput, MultiCombobox: JoinedInput, createAction }
