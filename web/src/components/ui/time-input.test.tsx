import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { TimeInput } from './time-input'

afterEach(() => cleanup())

function Harness({ initial = '', onChange }: { initial?: string; onChange: (t: string) => void }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <TimeInput value={value} onChange={t => { onChange(t); setValue(t) }} />
      {/* Somewhere for Tab to move focus to, so the field blurs. */}
      <button type="button">elsewhere</button>
    </>
  )
}

const options = () => screen.queryAllByRole('option').map(o => o.textContent)

describe('TimeInput', () => {
  it('opens on click with every 15 minutes, not filtered by its own value', async () => {
    render(<Harness initial="17:00" onChange={() => {}} />)
    await userEvent.click(screen.getByLabelText('Time'))
    expect(options()).toHaveLength(96)
    expect(options().slice(0, 3)).toEqual(['00:00', '00:15', '00:30'])
  })

  it('typing replaces the time and filters the list', async () => {
    render(<Harness initial="21:00" onChange={() => {}} />)
    const input = screen.getByLabelText('Time')
    await userEvent.click(input)
    await userEvent.keyboard('17')
    expect(input).toHaveValue('17')
    expect(options()).toEqual(['17:00', '17:15', '17:30', '17:45'])
  })

  it('a single digit finds the zero-padded hour', async () => {
    render(<Harness onChange={() => {}} />)
    await userEvent.click(screen.getByLabelText('Time'))
    await userEvent.keyboard('9')
    expect(options()).toEqual(['09:00', '09:15', '09:30', '09:45'])
  })

  it('picking an option commits it', async () => {
    const onChange = vi.fn()
    render(<Harness initial="21:00" onChange={onChange} />)
    await userEvent.click(screen.getByLabelText('Time'))
    await userEvent.keyboard('17')
    await userEvent.click(screen.getByRole('option', { name: '17:30' }))
    expect(onChange).toHaveBeenLastCalledWith('17:30')
    expect(screen.getByLabelText('Time')).toHaveValue('17:30')
  })

  it('a complete typed time commits as it is typed, even off the list', async () => {
    const onChange = vi.fn()
    render(<Harness initial="21:00" onChange={onChange} />)
    await userEvent.click(screen.getByLabelText('Time'))
    await userEvent.keyboard('17:40')
    expect(onChange).toHaveBeenLastCalledWith('17:40')
  })

  it('a short form commits on blur, normalised', async () => {
    const onChange = vi.fn()
    render(<Harness initial="21:00" onChange={onChange} />)
    await userEvent.click(screen.getByLabelText('Time'))
    await userEvent.keyboard('930')
    expect(onChange).not.toHaveBeenCalled()
    await userEvent.tab()
    expect(onChange).toHaveBeenLastCalledWith('09:30')
    expect(screen.getByLabelText('Time')).toHaveValue('09:30')
  })

  it('text that is not a time reverts on blur and changes nothing', async () => {
    const onChange = vi.fn()
    render(<Harness initial="21:00" onChange={onChange} />)
    await userEvent.click(screen.getByLabelText('Time'))
    await userEvent.keyboard('99')
    await userEvent.tab()
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Time')).toHaveValue('21:00')
  })
})
