import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Search } from 'lucide-react'
import { Input } from './input'

/** Controlled field, the shape most of the app uses. */
function Controlled({ initial = '', onValue }: { initial?: string; onValue?: (v: string) => void }) {
  const [value, setValue] = useState(initial)
  return (
    <Input
      value={value}
      onChange={e => { setValue(e.target.value); onValue?.(e.target.value) }}
      aria-label="Field"
    />
  )
}

/** Uncontrolled field bound with react-hook-form — no `value` prop at all. */
function Registered({ onValue }: { onValue: (v: string) => void }) {
  const { register, control } = useForm({ defaultValues: { name: 'Acme' } })
  onValue(useWatch({ control, name: 'name' }))
  return <Input {...register('name')} aria-label="Field" />
}

describe('Input', () => {
  it('shows the clear X only once there is text', async () => {
    const user = userEvent.setup()
    render(<Controlled />)

    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()
    await user.type(screen.getByRole('textbox'), 'hello')
    expect(screen.getByRole('button', { name: 'Clear' })).toBeInTheDocument()
  })

  it('clears a controlled field and reports the empty value to onChange', async () => {
    const user = userEvent.setup()
    const onValue = vi.fn()
    render(<Controlled initial="hello" onValue={onValue} />)

    // The caller's own onChange has to fire, not just the DOM value — a
    // controlled field that only changed in the DOM would snap back on render.
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    expect(onValue).toHaveBeenCalledWith('')
    expect(screen.getByRole('textbox')).toHaveValue('')
  })

  it('clears an uncontrolled react-hook-form field', async () => {
    const user = userEvent.setup()
    let latest = ''
    render(<Registered onValue={v => { latest = v }} />)

    expect(latest).toBe('Acme')
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    expect(screen.getByRole('textbox')).toHaveValue('')
    // The form state must follow the DOM, otherwise submitting would still
    // send "Acme" after the user visibly emptied the field.
    expect(latest).toBe('')
  })

  it('hides the X for password, number, disabled and readOnly fields', () => {
    const { rerender } = render(<Input type="password" value="secret" onChange={() => {}} aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()

    rerender(<Input type="number" value="42" onChange={() => {}} aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()

    rerender(<Input value="text" onChange={() => {}} disabled aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()

    rerender(<Input value="text" onChange={() => {}} readOnly aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()
  })

  it('hides the X on a seamless field, which has no box to anchor it', () => {
    // Seamless fields stretch to their container, so the X would float far
    // from the text — next to a panel's own Save/Delete buttons, in the case
    // of inline rename.
    const { rerender } = render(<Input value="text" onChange={() => {}} variant="seamless" aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()

    rerender(<Input value="text" onChange={() => {}} variant="seamless" clearable aria-label="Field" />)
    expect(screen.getByRole('button', { name: 'Clear' })).toBeInTheDocument()
  })

  it('honours an explicit clearable flag in both directions', () => {
    const { rerender } = render(<Input value="text" onChange={() => {}} clearable={false} aria-label="Field" />)
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()

    rerender(<Input type="password" value="secret" onChange={() => {}} clearable aria-label="Field" />)
    expect(screen.getByRole('button', { name: 'Clear' })).toBeInTheDocument()
  })

  it('calls onClear instead of emptying the field itself', async () => {
    const user = userEvent.setup()
    const onClear = vi.fn()
    render(<Input value="hello" onChange={() => {}} onClear={onClear} aria-label="Field" />)

    await user.click(screen.getByRole('button', { name: 'Clear' }))
    expect(onClear).toHaveBeenCalledOnce()
    // The caller owns the reset, so the DOM value is untouched by the component.
    expect(screen.getByRole('textbox')).toHaveValue('hello')
  })

  it('renders icon and action, and derives the clear testid from the field testid', () => {
    render(
      <Input
        value="hello"
        onChange={() => {}}
        aria-label="Field"
        data-testid="search"
        icon={<Search data-testid="icon" />}
        action={<button type="button">Go</button>}
      />,
    )

    expect(screen.getByTestId('icon')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Go' })).toBeInTheDocument()
    expect(screen.getByTestId('search-clear')).toBeInTheDocument()
  })

  it('does not re-attach a callback ref on every render', async () => {
    // Callers pass `ref={focusOnMount}` (TerminologyBrowser, ConceptTermsGrid).
    // A ref that re-attaches per render re-fires focus() on every keystroke and
    // yanks the caret out of whatever the user moved to.
    const user = userEvent.setup()
    // Stable identity, exactly like the real `focusOnMount`. An inline arrow
    // would re-attach every render on its own and prove nothing.
    let attachments = 0
    const stableRef = () => { attachments++ }

    function Field() {
      const [value, setValue] = useState('')
      return <Input value={value} onChange={e => setValue(e.target.value)} ref={stableRef} aria-label="Field" />
    }
    render(<Field />)
    const afterMount = attachments

    await user.type(screen.getByRole('textbox'), 'abcde')
    expect(attachments).toBe(afterMount)
  })

  it('forwards ref and arbitrary props to the inner input', () => {
    let node: HTMLInputElement | null = null
    render(<Input ref={el => { node = el }} placeholder="Type here" maxLength={5} aria-label="Field" />)

    expect(node).toBeInstanceOf(HTMLInputElement)
    expect(screen.getByPlaceholderText('Type here')).toHaveAttribute('maxlength', '5')
  })

  it('keeps focus in the field when the X is clicked', async () => {
    const user = userEvent.setup()
    render(<Controlled initial="hello" />)

    await user.click(screen.getByRole('button', { name: 'Clear' }))
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  it('clears without firing onBlur, so a commit-on-blur field never saves the old value', async () => {
    // The X swallows mousedown for exactly this: SegmentFilterBar, the project
    // domain and the TM profile description all commit on blur, and a blur
    // landing before the clear would save the text the user just discarded.
    const user = userEvent.setup()
    const onBlur = vi.fn()
    function Field() {
      const [value, setValue] = useState('hello')
      return <Input value={value} onChange={e => setValue(e.target.value)} onBlur={onBlur} aria-label="Field" />
    }
    render(<Field />)

    await user.click(screen.getByRole('textbox'))
    await user.click(screen.getByRole('button', { name: 'Clear' }))

    expect(onBlur).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox')).toHaveValue('')
  })

  it('focuses the field when the frame or the icon is clicked', async () => {
    const user = userEvent.setup()
    render(<Input value="" onChange={() => {}} aria-label="Field" icon={<Search data-testid="icon" />} />)

    // The box owns the padding, so clicking the frame or the icon gutter has to
    // focus the element the way clicking the text does.
    await user.click(screen.getByTestId('icon'))
    expect(screen.getByRole('textbox')).toHaveFocus()

    screen.getByRole('textbox').blur()
    await user.click(screen.getByRole('textbox').closest('[data-slot="input"]')!)
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  // The frame test above has no ancestors, so it passes either way. This one
  // pins the `contains` half of the guard: `closest()` walks past the box, so
  // without it an enclosing row link counts as "the thing being clicked" and
  // swallows the focus.
  it('still focuses the field when the box sits inside a link', async () => {
    const user = userEvent.setup()
    render(
      <a href="#row">
        <Input value="42" onChange={() => {}} aria-label="Field" />
      </a>,
    )

    await user.click(screen.getByRole('textbox').closest('[data-slot="input"]')!)
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  it('leaves a static action clickable as part of the field', async () => {
    // A `%` suffix or a spinner is decoration, not a control — clicking it
    // places the caret.
    const user = userEvent.setup()
    render(<Input value="42" onChange={() => {}} aria-label="Field" action={<span data-testid="suffix">%</span>} />)

    await user.click(screen.getByTestId('suffix'))
    expect(screen.getByRole('textbox')).toHaveFocus()
  })
})
