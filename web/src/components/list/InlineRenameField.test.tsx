import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { InlineRenameField } from './InlineRenameField'

describe('InlineRenameField', () => {
  it('renders the current value', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="My TM"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    expect(getByDisplayValue('My TM')).toBeInTheDocument()
  })

  it('calls onChange as the user types', () => {
    const onChange = vi.fn()
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="a"
        onChange={onChange}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    fireEvent.change(getByDisplayValue('a'), { target: { value: 'ab' } })
    expect(onChange).toHaveBeenCalledWith('ab')
  })

  it('calls onSubmit when Enter is pressed', () => {
    const onSubmit = vi.fn()
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="My Name"
        onChange={() => {}}
        onSubmit={onSubmit}
        onCancel={() => {}}
      />,
    )
    fireEvent.keyDown(getByDisplayValue('My Name'), { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('calls onCancel when Escape is pressed', () => {
    const onCancel = vi.fn()
    const onSubmit = vi.fn()
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="My Name"
        onChange={() => {}}
        onSubmit={onSubmit}
        onCancel={onCancel}
      />,
    )
    fireEvent.keyDown(getByDisplayValue('My Name'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('calls onSubmit on blur', () => {
    const onSubmit = vi.fn()
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="My Name"
        onChange={() => {}}
        onSubmit={onSubmit}
        onCancel={() => {}}
      />,
    )
    fireEvent.blur(getByDisplayValue('My Name'))
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('swallows clicks so the parent <Link> or card does not navigate', () => {
    const parentOnClick = vi.fn()
    const { getByDisplayValue } = render(
      <a href="/nav" onClick={parentOnClick}>
        <InlineRenameField
          value="My Name"
          onChange={() => {}}
          onSubmit={() => {}}
          onCancel={() => {}}
        />
      </a>,
    )
    fireEvent.click(getByDisplayValue('My Name'))
    // Parent click handler must not fire
    expect(parentOnClick).not.toHaveBeenCalled()
  })

  it('swallows clicks on the field frame and the clear X too, not just the input', () => {
    // Input draws a box that owns the padding and the clear X. Both are inside
    // the field as far as the user is concerned, so both have to be swallowed —
    // QAProfiles renames inside a row whose own onClick collapses it.
    const parentOnClick = vi.fn()
    const onChange = vi.fn()
    const { getByDisplayValue, getByRole } = render(
      <a href="/nav" onClick={parentOnClick}>
        <InlineRenameField
          value="My Name"
          onChange={onChange}
          onSubmit={() => {}}
          onCancel={() => {}}
        />
      </a>,
    )

    fireEvent.click(getByDisplayValue('My Name').closest('[data-slot="input"]')!)
    expect(parentOnClick).not.toHaveBeenCalled()

    fireEvent.click(getByRole('button', { name: 'Clear' }))
    expect(parentOnClick).not.toHaveBeenCalled()
    // And the X still does its own job — swallowing must not neuter it.
    expect(onChange).toHaveBeenCalledWith('')
  })

  it('ignores other keys (e.g., letters, arrows) without calling submit/cancel', () => {
    const onSubmit = vi.fn()
    const onCancel = vi.fn()
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="abc"
        onChange={() => {}}
        onSubmit={onSubmit}
        onCancel={onCancel}
      />,
    )
    fireEvent.keyDown(getByDisplayValue('abc'), { key: 'ArrowLeft' })
    fireEvent.keyDown(getByDisplayValue('abc'), { key: 'a' })
    expect(onSubmit).not.toHaveBeenCalled()
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('autoFocuses on mount so the user can type immediately', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="focused"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    expect(document.activeElement).toBe(getByDisplayValue('focused'))
  })

  it('passes the canonical compact-inline classes to the field box', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="x"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    // Input styles its BOX; the typography reaches the <input> by inheritance.
    const box = getByDisplayValue('x').closest('[data-slot="input"]')!
    expect(box.className).toContain('font-medium')
    expect(box.className).toContain('text-gray-900')
    expect(box.className).toContain('dark:text-gray-100')
    expect(box.className).toContain('h-7')
    expect(box.className).toContain('w-auto')
  })

  it('defaults maxLength to 255 to match typical varchar(255) DB columns', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="default"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    expect(getByDisplayValue('default')).toHaveAttribute('maxlength', '255')
  })

  it('allows callers to override maxLength', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="custom"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
        maxLength={64}
      />,
    )
    expect(getByDisplayValue('custom')).toHaveAttribute('maxlength', '64')
  })

  it('defaults aria-label to "Rename" so screen readers announce the field', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="aria default"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    )
    expect(getByDisplayValue('aria default')).toHaveAttribute('aria-label', 'Rename')
  })

  it('allows callers to override aria-label with entity-specific text', () => {
    const { getByDisplayValue } = render(
      <InlineRenameField
        value="custom aria"
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
        aria-label="Rename TM"
      />,
    )
    expect(getByDisplayValue('custom aria')).toHaveAttribute('aria-label', 'Rename TM')
  })

  it('forwards placeholder to the input (used by inline-edit cases like term base description)', () => {
    const { getByPlaceholderText } = render(
      <InlineRenameField
        value=""
        onChange={() => {}}
        onSubmit={() => {}}
        onCancel={() => {}}
        placeholder="Add a description..."
      />,
    )
    expect(getByPlaceholderText('Add a description...')).toBeInTheDocument()
  })
})
