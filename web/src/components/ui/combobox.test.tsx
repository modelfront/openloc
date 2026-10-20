// Combobox — the standardized picker. The tests pin the four things the
// component promises and that a call site can't re-implement: search is there
// unless a short fixed list opts out, a row is label + optional chip + check, the current selection sits
// pinned above the rest, and multi-select toggles without the list re-sorting
// under the pointer.

import { useState } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Combobox, MultiCombobox, createAction, type ComboboxOption } from './combobox'

const options: ComboboxOption[] = [
  { value: 'deepl', label: 'DeepL' },
  { value: 'google', label: 'Google Translate' },
  { value: 'libre', label: 'LibreTranslate' },
]

// jsdom reports 0 for every layout measurement. This gives the measuring row
// something to read; the returned function undoes it. jsdom defines
// `clientWidth` on Element.prototype and `offsetWidth` on HTMLElement.prototype,
// so the stub is an own property on HTMLElement either way — restore the
// original descriptor when there was one, otherwise delete the shadow.
function stubSize(prop: 'clientWidth' | 'offsetWidth', px: number) {
  const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, prop)
  Object.defineProperty(HTMLElement.prototype, prop, { configurable: true, get: () => px })
  return () => {
    if (original) Object.defineProperty(HTMLElement.prototype, prop, original)
    else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[prop]
  }
}

const after = (a: Element, b: Element) =>
  !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

describe('Combobox single select', () => {
  it('shows the placeholder until something is selected', () => {
    const { rerender } = render(
      <Combobox value="" onChange={() => {}} options={options} placeholder="Select engine" data-testid="e" />,
    )
    expect(screen.getByTestId('e')).toHaveTextContent('Select engine')

    rerender(<Combobox value="google" onChange={() => {}} options={options} placeholder="Select engine" data-testid="e" />)
    expect(screen.getByTestId('e')).toHaveTextContent('Google Translate')
  })

  it('treats an empty-string value as selected when an option carries it', async () => {
    // "None" rows use value '' — the field must read "None", not the placeholder.
    const user = userEvent.setup()
    const withNone = [{ value: '', label: 'None' }, ...options]
    render(<Combobox value="" onChange={() => {}} options={withNone} placeholder="Select…" data-testid="e" />)
    expect(screen.getByTestId('e')).toHaveTextContent('None')
    expect(screen.getByTestId('e')).not.toHaveTextContent('Select…')

    await user.click(screen.getByTestId('e'))
    expect(screen.getByTestId('e-option-')).toHaveAttribute('data-checked', 'true')
  })

  // cmdk keys the highlight off the row's value string and reads an empty one
  // as "nothing highlighted". A "None" row carries value '', so without a stand-in
  // it stayed unlit under the pointer while still selecting normally.
  it('highlights a row whose value is the empty string', async () => {
    const user = userEvent.setup()
    const withNone: ComboboxOption[] = [{ value: '', label: 'No engine' }, ...options]
    render(<Combobox value="google" onChange={() => {}} options={withNone} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    const none = screen.getByRole('option', { name: 'No engine' })
    await user.hover(none)
    expect(none).toHaveAttribute('data-selected', 'true')
  })

  it('still selects the empty-string row and can search for it', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const withNone: ComboboxOption[] = [{ value: '', label: 'No engine' }, ...options]
    render(<Combobox value="google" onChange={onChange} options={withNone} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    // The stand-in must not leak into the search: typing the label still finds it.
    await user.type(screen.getByPlaceholderText('Search…'), 'No eng')
    await user.click(screen.getByRole('option', { name: 'No engine' }))
    expect(onChange).toHaveBeenCalledWith('')
  })

  it('clearable: shows an X only while a value is picked, and the X clears without opening', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { rerender } = render(<Combobox value="" onChange={onChange} options={options} clearable data-testid="e" />)
    expect(screen.queryByTestId('e-clear')).not.toBeInTheDocument()

    rerender(<Combobox value="google" onChange={onChange} options={options} clearable data-testid="e" />)
    await user.click(screen.getByTestId('e-clear'))
    expect(onChange).toHaveBeenCalledWith('')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()

    // Without the prop nothing changes for existing pickers.
    rerender(<Combobox value="google" onChange={onChange} options={options} data-testid="e" />)
    expect(screen.queryByTestId('e-clear')).not.toBeInTheDocument()
  })

  it('renders the search field by default', async () => {
    const user = userEvent.setup()
    render(<Combobox value="" onChange={() => {}} options={options} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    expect(screen.getByPlaceholderText('Search…')).toBeInTheDocument()
  })

  it('searchable={false}: no search field, and the keyboard still picks a row', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Combobox value="deepl" onChange={onChange} options={options} searchable={false} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    expect(screen.queryByPlaceholderText('Search…')).toBeNull()
    expect(screen.queryByRole('combobox')).toBeNull()
    expect(screen.getByRole('option', { name: /Google Translate/ })).toBeInTheDocument()
    await user.keyboard('{ArrowDown}{Enter}')
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).not.toBe('deepl')
  })

  it('searchable={false}: a click still picks a row', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Combobox value="deepl" onChange={onChange} options={options} searchable={false} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    await user.click(screen.getByRole('option', { name: /LibreTranslate/ }))
    expect(onChange).toHaveBeenCalledWith('libre')
  })

  it('filters on label and on keywords', async () => {
    const user = userEvent.setup()
    render(
      <Combobox
        value=""
        onChange={() => {}}
        options={[{ value: 'de', label: 'German', keywords: 'de deutsch' }, ...options]}
        data-testid="e"
      />,
    )
    await user.click(screen.getByTestId('e'))
    await user.type(screen.getByPlaceholderText('Search…'), 'deutsch')

    expect(screen.getByTestId('e-option-de')).toBeInTheDocument()
    expect(screen.queryByTestId('e-option-google')).not.toBeInTheDocument()
  })

  it('onSearchChange: hands over what is typed and shows the given options unfiltered', async () => {
    const user = userEvent.setup()
    const onSearchChange = vi.fn()
    render(<Combobox value="" onChange={() => {}} options={options} onSearchChange={onSearchChange} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    await user.type(screen.getByPlaceholderText('Search…'), 'zz')

    expect(onSearchChange).toHaveBeenLastCalledWith('zz')
    expect(screen.getByTestId('e-option-google')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(onSearchChange).toHaveBeenLastCalledWith('')
  })

  it('onSearchChange: a pick that closes the panel lets the caller keep the text in its own state', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    function Caller() {
      const [value, setValue] = useState('')
      const [search, setSearch] = useState('')
      return (
        <>
          <Combobox value={value} onChange={setValue} options={options} onSearchChange={setSearch} data-testid="e" />
          <output data-testid="search">{search}</output>
        </>
      )
    }
    const user = userEvent.setup()
    render(<Caller />)
    await user.click(screen.getByTestId('e'))
    await user.type(screen.getByPlaceholderText('Search…'), 'goo')
    expect(screen.getByTestId('search')).toHaveTextContent('goo')
    await user.click(screen.getByTestId('e-option-google'))

    expect(screen.getByTestId('search')).toHaveTextContent('')
    expect(errors).not.toHaveBeenCalled()
    errors.mockRestore()
  })

  it('selects a value and closes', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Combobox value="" onChange={onChange} options={options} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    await user.click(screen.getByTestId('e-option-google'))

    expect(onChange).toHaveBeenCalledWith('google')
    expect(screen.queryByTestId('e-option-google')).not.toBeInTheDocument()
  })

  it('marks only the selected row as checked', async () => {
    const user = userEvent.setup()
    render(<Combobox value="google" onChange={() => {}} options={options} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    expect(screen.getByTestId('e-option-google')).toHaveAttribute('data-checked', 'true')
    expect(screen.getByTestId('e-option-deepl')).toHaveAttribute('data-checked', 'false')
  })

  it('pins the selected row above the rest', async () => {
    const user = userEvent.setup()
    render(<Combobox value="libre" onChange={() => {}} options={options} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    // LibreTranslate is last in `options` but selected, so it leads the list.
    expect(after(screen.getByTestId('e-option-libre'), screen.getByTestId('e-option-deepl'))).toBe(true)
  })
})

describe('Combobox chips', () => {
  const withChip: ComboboxOption[] = [
    { value: 'deepl', label: 'DeepL' },
    { value: 'ai', label: 'Default AI MT engine', chip: 'gpt-4o' },
  ]

  it('renders a row chip right of the label', async () => {
    const user = userEvent.setup()
    render(<Combobox value="" onChange={() => {}} options={withChip} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    const row = screen.getByTestId('e-option-ai')
    expect(row).toHaveTextContent('gpt-4o')
    expect(row.querySelector('[data-slot="combobox-chip"]')).not.toBeNull()
    // A string chip uses the standard Badge rather than bare text.
    expect(row.querySelector('[data-slot="combobox-chip"] [data-slot="badge"]')).not.toBeNull()
  })

  it('shows the chip in the trigger once its option is selected', () => {
    const { rerender } = render(<Combobox value="" onChange={() => {}} options={withChip} data-testid="b" />)
    expect(screen.getByTestId('b').querySelector('[data-slot="combobox-selected-chip"]')).toBeNull()

    rerender(<Combobox value="ai" onChange={() => {}} options={withChip} data-testid="b" />)
    const trigger = screen.getByTestId('b')
    expect(trigger).toHaveTextContent('Default AI MT engine')
    expect(trigger).toHaveTextContent('gpt-4o')
  })

  it('renders nothing extra when the selected option has no chip', () => {
    render(<Combobox value="deepl" onChange={() => {}} options={withChip} data-testid="c" />)
    expect(screen.getByTestId('c').querySelector('[data-slot="combobox-selected-chip"]')).toBeNull()
  })

  describe('triggerDisplay="chip"', () => {
    it('shows the chip instead of the label', () => {
      render(<Combobox value="ai" onChange={() => {}} options={withChip} triggerDisplay="chip" data-testid="d" />)
      const trigger = screen.getByTestId('d')
      expect(trigger).toHaveTextContent('gpt-4o')
      expect(trigger).not.toHaveTextContent('Default AI MT engine')
    })

    it('leaves the list rows alone', async () => {
      const user = userEvent.setup()
      render(<Combobox value="ai" onChange={() => {}} options={withChip} triggerDisplay="chip" data-testid="e" />)
      await user.click(screen.getByTestId('e'))
      expect(screen.getByTestId('e-option-ai')).toHaveTextContent('Default AI MT engine')
    })

    it('falls back to the label when the pick has no chip', () => {
      render(<Combobox value="deepl" onChange={() => {}} options={withChip} triggerDisplay="chip" data-testid="f" />)
      expect(screen.getByTestId('f')).toHaveTextContent('DeepL')
    })

    it('falls back to the placeholder when nothing is picked', () => {
      render(
        <Combobox value="" onChange={() => {}} options={withChip} triggerDisplay="chip" placeholder="Pick one" data-testid="g" />,
      )
      expect(screen.getByTestId('g')).toHaveTextContent('Pick one')
    })
  })
})

describe('Combobox actions', () => {
  function setup(actions?: { key: string; label: string; onSelect: () => void }[]) {
    const onChange = vi.fn()
    render(<Combobox value="" onChange={onChange} options={options} actions={actions} data-testid="e" />)
    return { onChange }
  }

  it('renders no action rows when none are passed', async () => {
    const user = userEvent.setup()
    setup()
    await user.click(screen.getByTestId('e'))

    expect(screen.getByTestId('e-option-deepl')).toBeInTheDocument()
    expect(screen.queryByTestId('e-action-new-mt-engine')).not.toBeInTheDocument()
  })

  it('renders action rows under the search, above the options, with a divider', async () => {
    const user = userEvent.setup()
    setup([{ key: 'new-mt-engine', label: 'New MT engine', onSelect: vi.fn() }])
    await user.click(screen.getByTestId('e'))

    const action = screen.getByTestId('e-action-new-mt-engine')
    expect(after(screen.getByPlaceholderText('Search…'), action)).toBe(true)
    expect(after(action, screen.getByTestId('e-option-deepl'))).toBe(true)
    expect(document.querySelectorAll('[data-slot="command-separator"]').length).toBeGreaterThan(0)
  })

  it('keeps actions and their divider visible when the search matches no option', async () => {
    const user = userEvent.setup()
    setup([{ key: 'new-mt-engine', label: 'New MT engine', onSelect: vi.fn() }])
    await user.click(screen.getByTestId('e'))
    await user.type(screen.getByPlaceholderText('Search…'), 'zzzz')

    expect(screen.queryByTestId('e-option-deepl')).not.toBeInTheDocument()
    expect(screen.getByTestId('e-action-new-mt-engine')).toBeInTheDocument()
    expect(document.querySelectorAll('[data-slot="command-separator"]').length).toBeGreaterThan(0)
  })

  it('drops the divider under the actions when there are no options at all', async () => {
    const user = userEvent.setup()
    render(<Combobox value="" onChange={vi.fn()} options={[]} actions={[{ key: 'create', label: 'Create new customer...', onSelect: vi.fn() }]} data-testid="e" />)
    await user.click(screen.getByTestId('e'))

    // A divider is never the last thing in the panel.
    expect(screen.getByTestId('e-action-create')).toBeInTheDocument()
    expect(document.querySelectorAll('[data-slot="command-separator"]')).toHaveLength(0)
  })

  it('fires onSelect without selecting a value', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const { onChange } = setup([{ key: 'new-mt-engine', label: 'New MT engine', onSelect }])
    await user.click(screen.getByTestId('e'))
    await user.click(screen.getByTestId('e-action-new-mt-engine'))

    expect(onSelect).toHaveBeenCalledTimes(1)
    // An action is not a selection — the picker's value must not move.
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.queryByTestId('e-option-deepl')).not.toBeInTheDocument()
  })
})

function MultiHarness({ initial = [] as string[], wrap = false }) {
  const [value, setValue] = useState<string[]>(initial)
  return <MultiCombobox value={value} onChange={setValue} options={options} placeholder="Add engine" data-testid="e" wrap={wrap} />
}

describe('MultiCombobox', () => {
  it('toggles values and stays open', async () => {
    const user = userEvent.setup()
    render(<MultiHarness />)
    await user.click(screen.getByTestId('e'))

    await user.click(screen.getByTestId('e-option-deepl'))
    expect(screen.getByTestId('e-option-deepl')).toHaveAttribute('data-checked', 'true')

    await user.click(screen.getByTestId('e-option-google'))
    expect(screen.getByTestId('e-option-google')).toHaveAttribute('data-checked', 'true')

    await user.click(screen.getByTestId('e-option-deepl'))
    expect(screen.getByTestId('e-option-deepl')).toHaveAttribute('data-checked', 'false')
  })

  it('shows the selection as chips inside the control', async () => {
    const user = userEvent.setup()
    render(<MultiHarness />)
    const trigger = screen.getByTestId('e')
    expect(trigger).toHaveTextContent('Add engine')

    await user.click(trigger)
    await user.click(screen.getByTestId('e-option-deepl'))
    await user.click(screen.getByTestId('e-option-google'))

    // Two chips in the visible row, two in the hidden measuring mirror.
    const chips = trigger.querySelectorAll('[data-slot="combobox-value-chip"]')
    expect(chips).toHaveLength(4)
    expect(trigger).toHaveTextContent('DeepL')
    expect(trigger).toHaveTextContent('Google Translate')
  })

  it('removes a value from its chip without opening the popover', async () => {
    const user = userEvent.setup()
    render(<MultiHarness initial={['deepl', 'google']} />)
    await user.click(screen.getAllByLabelText('Remove DeepL')[0])

    const trigger = screen.getByTestId('e')
    expect(trigger).not.toHaveTextContent('DeepL')
    expect(trigger).toHaveTextContent('Google Translate')
    // The click must not fall through to the trigger.
    expect(screen.queryByTestId('e-option-deepl')).not.toBeInTheDocument()
  })

  it('collapses chips that do not fit into +N', () => {
    // jsdom has no layout, so the widths the measuring row reads are stubbed:
    // a 100px field and 30px chips fit exactly one chip plus the "+N" badge.
    const widths = [
      stubSize('clientWidth', 100),
      stubSize('offsetWidth', 30),
    ]
    try {
      render(<MultiHarness initial={['deepl', 'google', 'libre']} />)
      const trigger = screen.getByTestId('e')
      expect(trigger.querySelector('[data-slot="combobox-overflow"]')).toHaveTextContent('+2')
    } finally {
      widths.forEach((restore) => restore())
    }
  })

  // Anti-tautology RED proof: drop the `wrap` branch from the measuring
  // effect → the same stubbed widths collapse two chips and this fails.
  it('wrap: shows every chip and grows instead of collapsing into +N', () => {
    const widths = [
      stubSize('clientWidth', 100),
      stubSize('offsetWidth', 30),
    ]
    try {
      render(<MultiHarness initial={['deepl', 'google', 'libre']} wrap />)
      const trigger = screen.getByTestId('e')
      expect(trigger.querySelector('[data-slot="combobox-overflow"]')).toBeNull()
      // All three chips, and no hidden measuring mirror.
      expect(trigger.querySelectorAll('[data-slot="combobox-value-chip"]')).toHaveLength(3)
      expect(trigger.querySelector('[data-wrap="true"]')).not.toBeNull()
      expect(trigger.className).toContain('min-h-input')
      expect(trigger.className).not.toMatch(/(^|\s)h-input(\s|$)/)
    } finally {
      widths.forEach((restore) => restore())
    }
  })

  it('does not re-sort the list while it is open', async () => {
    const user = userEvent.setup()
    render(<MultiHarness />)
    await user.click(screen.getByTestId('e'))

    // Selecting the last row must not move it to the pinned block under the
    // pointer — the order is snapshotted when the popover opens.
    await user.click(screen.getByTestId('e-option-libre'))
    expect(after(screen.getByTestId('e-option-deepl'), screen.getByTestId('e-option-libre'))).toBe(true)
  })

  it('drops the divider when every option is selected', async () => {
    const user = userEvent.setup()
    render(<MultiHarness initial={['deepl', 'google', 'libre']} />)
    await user.click(screen.getByTestId('e'))

    // Nothing sits below the divider once the rest of the list is empty, so it
    // would hang at the bottom of the panel.
    expect(document.querySelectorAll('[data-slot="command-separator"]')).toHaveLength(0)
  })

  it('keeps the divider while options remain', async () => {
    const user = userEvent.setup()
    render(<MultiHarness initial={['deepl']} />)
    await user.click(screen.getByTestId('e'))

    expect(document.querySelectorAll('[data-slot="command-separator"]')).toHaveLength(1)
  })

  it('pins the selection on the next open', async () => {
    const user = userEvent.setup()
    render(<MultiHarness initial={['libre']} />)
    await user.click(screen.getByTestId('e'))

    expect(after(screen.getByTestId('e-option-libre'), screen.getByTestId('e-option-deepl'))).toBe(true)
  })
})

// jsdom renders no CSS, so the red frame is pinned through what drives it: the
// `aria-invalid` attribute on the trigger plus the variant classes keyed on it.
describe('aria-invalid', () => {
  it('Combobox: sets aria-invalid on the trigger only while invalid', () => {
    const { rerender } = render(<Combobox value="" onChange={() => {}} options={options} data-testid="e" aria-invalid />)
    const trigger = screen.getByTestId('e')
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    expect(trigger.className).toContain('aria-invalid:border-destructive')

    rerender(<Combobox value="" onChange={() => {}} options={options} data-testid="e" aria-invalid={false} />)
    expect(screen.getByTestId('e')).not.toHaveAttribute('aria-invalid')
  })

  it('MultiCombobox: sets aria-invalid on the trigger', () => {
    render(<MultiCombobox value={[]} onChange={() => {}} options={options} data-testid="e" aria-invalid />)
    const trigger = screen.getByTestId('e')
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    expect(trigger.className).toContain('aria-invalid:border-destructive')
  })

  it('is absent by default', () => {
    render(<Combobox value="" onChange={() => {}} options={options} data-testid="e" />)
    expect(screen.getByTestId('e')).not.toHaveAttribute('aria-invalid')
  })
})

// createAction is the "+ Create new…" row for resource pickers: the create flow
// resolves to an id and the caller selects it, or resolves null and nothing moves.
describe('createAction', () => {
  it('selects the created id once the flow resolves', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const action = createAction('Create new TM…', async () => 42, onChange)
    render(<Combobox value="" onChange={vi.fn()} options={options} actions={[action]} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    await user.click(screen.getByTestId('e-action-create'))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith(42))
  })

  it('selects nothing when the flow is cancelled', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const action = createAction('Create new TM…', async () => null, onChange)
    render(<Combobox value="" onChange={vi.fn()} options={options} actions={[action]} data-testid="e" />)
    await user.click(screen.getByTestId('e'))
    await user.click(screen.getByTestId('e-action-create'))

    await new Promise((r) => setTimeout(r, 10))
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('onCreateOption', () => {
  it('offers the typed text when it matches no option, and hands it back raw', async () => {
    const user = userEvent.setup()
    const onCreateOption = vi.fn()
    render(
      <MultiCombobox
        options={options}
        value={[]}
        onChange={vi.fn()}
        onCreateOption={onCreateOption}
        data-testid="picker"
      />,
    )
    await user.click(screen.getByTestId('picker'))
    await user.type(screen.getByPlaceholderText('Search…'), '  Papiamento  ')
    await user.click(screen.getByTestId('picker-action-create-option'))
    // Trimmed, but otherwise exactly what was typed.
    expect(onCreateOption).toHaveBeenCalledWith('Papiamento')
  })

  it('does not offer to create something the list already has', async () => {
    const user = userEvent.setup()
    render(
      <MultiCombobox
        options={options}
        value={[]}
        onChange={vi.fn()}
        onCreateOption={vi.fn()}
        data-testid="picker"
      />,
    )
    await user.click(screen.getByTestId('picker'))
    // Case-insensitive: "deepl" is DeepL, not a second option.
    await user.type(screen.getByPlaceholderText('Search…'), 'deepl')
    expect(screen.queryByTestId('picker-action-create-option')).not.toBeInTheDocument()
  })

  it('stays silent without the prop, so an ordinary picker gains nothing', async () => {
    const user = userEvent.setup()
    render(<MultiCombobox options={options} value={[]} onChange={vi.fn()} data-testid="picker" />)
    await user.click(screen.getByTestId('picker'))
    await user.type(screen.getByPlaceholderText('Search…'), 'Papiamento')
    expect(screen.queryByTestId('picker-action-create-option')).not.toBeInTheDocument()
  })
})

describe('a disabled option in multi-select', () => {
  const withLocked: ComboboxOption[] = [
    { value: 'deepl', label: 'DeepL', disabled: true },
    { value: 'google', label: 'Google Translate' },
  ]

  it('keeps its chip but drops the × that would remove it', () => {
    render(<MultiCombobox options={withLocked} value={['deepl', 'google']} onChange={vi.fn()} wrap data-testid="picker" />)
    // Both are selected, so both are chips…
    expect(screen.getByTestId('picker')).toHaveTextContent('DeepL')
    // …but only the one that may change carries a remove control. Otherwise the
    // list row is unclickable while the chip quietly removes it anyway.
    expect(screen.queryByTestId('picker-remove-deepl')).not.toBeInTheDocument()
    expect(screen.getByTestId('picker-remove-google')).toBeInTheDocument()
  })

  it('cannot be toggled off from the list either', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<MultiCombobox options={withLocked} value={['deepl']} onChange={onChange} wrap data-testid="picker" />)
    await user.click(screen.getByTestId('picker'))
    await user.click(screen.getByTestId('picker-option-deepl'))
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('the create-option divider', () => {
  const sep = () => document.querySelectorAll('[data-slot="command-separator"]')

  it('is absent when the create row is the only thing in the panel', async () => {
    const user = userEvent.setup()
    // No options at all: typing leaves "Add …" alone, so a divider under it
    // would hang at the bottom of the panel.
    render(<MultiCombobox options={[]} value={[]} onChange={vi.fn()} onCreateOption={vi.fn()} data-testid="picker" />)
    await user.click(screen.getByTestId('picker'))
    await user.type(screen.getByPlaceholderText('Search…'), 'Papiamento')
    expect(screen.getByTestId('picker-action-create-option')).toBeInTheDocument()
    expect(sep()).toHaveLength(0)
  })

  it('is absent when the typed text filters every option away', async () => {
    const user = userEvent.setup()
    // The usual way to reach the create row: options exist, but none match, so
    // the list under it is empty even though `options` is not.
    render(<MultiCombobox options={options} value={[]} onChange={vi.fn()} onCreateOption={vi.fn()} data-testid="picker" />)
    await user.click(screen.getByTestId('picker'))
    await user.type(screen.getByPlaceholderText('Search…'), 'Papiamento')
    expect(screen.getByTestId('picker-action-create-option')).toBeInTheDocument()
    expect(sep()).toHaveLength(0)
  })

  it('is there when options survive the filter below it', async () => {
    const user = userEvent.setup()
    render(<MultiCombobox options={options} value={[]} onChange={vi.fn()} onCreateOption={vi.fn()} data-testid="picker" />)
    await user.click(screen.getByTestId('picker'))
    // The filter matches the search as a substring of the label, so "Deep"
    // keeps DeepL below while still being a new value worth offering to add.
    await user.type(screen.getByPlaceholderText('Search…'), 'Deep')
    expect(sep().length).toBeGreaterThan(0)
  })
})
