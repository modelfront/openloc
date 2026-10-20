import { describe, it, expect } from 'vitest'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Textarea } from './textarea'

function Controlled({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  return <Textarea value={value} onChange={e => setValue(e.target.value)} aria-label="Notes" />
}

function Registered({ onValue }: { onValue: (v: string) => void }) {
  const { register, control } = useForm({ defaultValues: { notes: 'Draft' } })
  onValue(useWatch({ control, name: 'notes' }))
  return <Textarea {...register('notes')} aria-label="Notes" />
}

describe('Textarea', () => {
  it('never shows a clear X, unlike Input', async () => {
    const user = userEvent.setup()
    render(<Controlled />)

    await user.type(screen.getByRole('textbox'), 'hello')
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull()
  })

  it('edits a controlled value', async () => {
    const user = userEvent.setup()
    render(<Controlled initial="hello" />)

    await user.type(screen.getByRole('textbox'), ' world')
    expect(screen.getByRole('textbox')).toHaveValue('hello world')
  })

  it('binds to an uncontrolled react-hook-form field', async () => {
    const user = userEvent.setup()
    let latest = ''
    render(<Registered onValue={v => { latest = v }} />)

    expect(screen.getByRole('textbox')).toHaveValue('Draft')
    await user.type(screen.getByRole('textbox'), '!')
    expect(latest).toBe('Draft!')
  })

  it('puts the minimum height on the element, so the drag handle has a floor', () => {
    // The browser clamps a resize-handle drag against the ELEMENT's own
    // min-height. With the minimum on the box instead, the handle shrinks the
    // typing area to nothing inside a full-height border.
    render(<Textarea aria-label="Notes" />)
    const el = screen.getByRole('textbox')

    expect(el.className).toContain('min-h-12')
    expect(el.closest('[data-slot="textarea"]')!.className).not.toMatch(/(^|\s)min-h-/)
  })

  it('lets a caller lower that floor through inputClassName', () => {
    render(<Textarea aria-label="Notes" inputClassName="min-h-0" />)
    expect(screen.getByRole('textbox').className).toContain('min-h-0')
    expect(screen.getByRole('textbox').className).not.toContain('min-h-12')
  })

  it('keeps the action row inside the box', () => {
    // jsdom has no layout, so this pins containment rather than geometry: the
    // action row is a child of the box, and the box is free to grow for it.
    render(<Textarea aria-label="Notes" action={<button type="button">Send</button>} />)
    const box = screen.getByRole('textbox').closest('[data-slot="textarea"]')!

    expect(box.querySelector('[data-slot="textarea-action"]')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Send' }).closest('[data-slot="textarea"]')).toBe(box)
    // No fixed height: the box has to grow for the text AND the action row.
    expect(box.className).toContain('h-auto')
  })

  it('focuses the element when the box or its inner row is clicked', async () => {
    // Textarea nests a row div between the box and the element, so a click can
    // land on either — both have to reach the textarea.
    const user = userEvent.setup()
    render(<Textarea aria-label="Notes" />)
    const el = screen.getByRole('textbox')

    await user.click(el.closest('[data-slot="textarea"]')!)
    expect(el).toHaveFocus()

    el.blur()
    await user.click(el.parentElement!)
    expect(el).toHaveFocus()
  })

  it('renders the action row only when there is an action', () => {
    const { rerender } = render(<Textarea aria-label="Notes" defaultValue="text" />)
    expect(document.querySelector('[data-slot="textarea-action"]')).toBeNull()

    rerender(<Textarea aria-label="Notes" defaultValue="text" action={<button type="button">Send</button>} />)
    expect(document.querySelector('[data-slot="textarea-action"]')).not.toBeNull()
  })

  it('forwards ref and props to the textarea', () => {
    let node: HTMLTextAreaElement | null = null
    render(<Textarea ref={el => { node = el }} aria-label="Notes" rows={4} />)

    expect(node).toBeInstanceOf(HTMLTextAreaElement)
    expect(screen.getByRole('textbox')).toHaveAttribute('rows', '4')
  })

  it('routes className to the box and inputClassName to the element', () => {
    render(<Textarea aria-label="Notes" className="w-64" inputClassName="resize-none" />)
    const el = screen.getByRole('textbox')

    expect(el.closest('[data-slot="textarea"]')!.className).toContain('w-64')
    expect(el.className).toContain('resize-none')
  })
})
