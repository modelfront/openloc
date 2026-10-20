import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BlurCommitInput } from './blur-commit-input'

const field = () => screen.getByLabelText('Domain') as HTMLInputElement

function setup(value: string | null | undefined = '') {
  const onCommit = vi.fn()
  render(<BlurCommitInput value={value} onCommit={onCommit} aria-label="Domain" />)
  return { onCommit, user: userEvent.setup() }
}

describe('BlurCommitInput', () => {
  it('does not commit while typing — that is the point of buffering a draft', async () => {
    const { onCommit, user } = setup()
    await user.type(field(), 'Patents')
    expect(field().value).toBe('Patents')
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('commits the trimmed draft on blur', async () => {
    const { onCommit, user } = setup()
    await user.type(field(), '  Patents  ')
    await user.tab()
    expect(onCommit).toHaveBeenCalledExactlyOnceWith('Patents')
  })

  it('stays silent on blur when the text did not change', async () => {
    const { onCommit, user } = setup('Patents')
    await user.click(field())
    await user.tab()
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('treats a whitespace-only edit of an empty field as unchanged', async () => {
    const { onCommit, user } = setup('')
    await user.type(field(), '   ')
    await user.tab()
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('commits on Enter, so the keyboard path does not need a blur', async () => {
    const { onCommit, user } = setup()
    await user.type(field(), 'Patents{Enter}')
    expect(onCommit).toHaveBeenCalledExactlyOnceWith('Patents')
  })

  it('shows a later value from the server once the draft is released', async () => {
    const onCommit = vi.fn()
    const { rerender } = render(<BlurCommitInput value="Patents" onCommit={onCommit} aria-label="Domain" />)
    rerender(<BlurCommitInput value="Legal" onCommit={onCommit} aria-label="Domain" />)
    expect(field().value).toBe('Legal')
  })

  it('reads null as an empty field rather than the string "null"', () => {
    render(<BlurCommitInput value={null} onCommit={vi.fn()} aria-label="Domain" />)
    expect(field().value).toBe('')
  })
})
