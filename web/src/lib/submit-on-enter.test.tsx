import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { submitOnEnter } from './submit-on-enter'

function Fields({ submit, onParentKey }: { submit: () => void; onParentKey: () => void }) {
  return (
    <div onKeyDown={onParentKey}>
      <div onKeyDown={submitOnEnter(submit)}>
        <input data-testid="field" type="text" />
        <textarea data-testid="area" />
      </div>
    </div>
  )
}

describe('submitOnEnter', () => {
  it('submits on Enter in a field and stops the event there', () => {
    const submit = vi.fn()
    const parent = vi.fn()
    render(<Fields submit={submit} onParentKey={parent} />)
    const e = fireEvent.keyDown(screen.getByTestId('field'), { key: 'Enter' })
    expect(submit).toHaveBeenCalledOnce()
    expect(e).toBe(false) // default prevented
    expect(parent).not.toHaveBeenCalled()
  })

  it('leaves Enter in a text area and other keys alone', () => {
    const submit = vi.fn()
    const parent = vi.fn()
    render(<Fields submit={submit} onParentKey={parent} />)
    fireEvent.keyDown(screen.getByTestId('area'), { key: 'Enter' })
    fireEvent.keyDown(screen.getByTestId('field'), { key: 'a' })
    expect(submit).not.toHaveBeenCalled()
    expect(parent).toHaveBeenCalledTimes(2)
  })
})
