import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'
import { useState } from 'react'
import { DeadlinePicker } from './deadline-picker'
import { clearDeadline, clickDay, clickDone, clickPreset, openPicker, typeTime, zoneSelect, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'

// The zone picker becomes a native <select>; zoneSelect() finds it.
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

afterEach(() => cleanup())

function Harness({ initial = null, onChange, variant }: {
  initial?: string | null
  onChange: (v: string | null) => void
  variant?: 'chip' | 'field'
}) {
  const [value, setValue] = useState<string | null>(initial)
  return (
    <DeadlinePicker
      value={value}
      onChange={v => { onChange(v); setValue(v) }}
      variant={variant}
      aria-label="Deadline"
      data-testid="picker"
    />
  )
}

// Every expectation is on the UTC instant, so the suite does not depend on the
// zone the tests run in; where a zone matters the test picks it explicitly.
describe('DeadlinePicker', () => {
  it('says what clicking does when unset, and shows nothing stored', () => {
    render(<Harness onChange={() => {}} />)
    expect(screen.getByTestId('picker')).toHaveTextContent('Set due date')
    expect(screen.getByTestId('picker')).toHaveAttribute('data-value', '')
  })

  it('holds edits while open and commits one UTC value on close', () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    openPicker('picker')
    // Pick the zone explicitly: 10:30 Tokyo is 01:30Z whatever the runner's zone.
    fireEvent.change(zoneSelect(), { target: { value: 'Asia/Tokyo' } })
    clickDay('2027-03-15')
    typeTime('10:30')
    expect(onChange, 'saved before the popover closed').not.toHaveBeenCalled()

    clickDone()
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('2027-03-15T01:30:00.000Z')
  }, PICKER_FLOW_TIMEOUT)

  it('reopens in the viewer\'s zone — the zone picked for entry is not kept', () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    openPicker('picker')
    const zone = zoneSelect
    const browserZone = zone().value
    const other = browserZone === 'Asia/Tokyo' ? 'Europe/Berlin' : 'Asia/Tokyo'
    fireEvent.change(zone(), { target: { value: other } })
    clickDay('2027-03-15')
    clickDone()

    openPicker('picker')
    expect(zone().value).toBe(browserZone)
  }, PICKER_FLOW_TIMEOUT)

  it('does not call onChange when the popover closes with the value unchanged', () => {
    const onChange = vi.fn()
    render(<Harness initial="2027-03-15T17:00:00Z" onChange={onChange} />)
    openPicker('picker')
    clickDone()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('keeps the time already set when another day is picked', () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    openPicker('picker')
    fireEvent.change(zoneSelect(), { target: { value: 'Asia/Tokyo' } })
    clickDay('2027-03-15')
    // Not the 17:00 default, so a picker that falls back to it fails here.
    typeTime('10:30')
    clickDay('2027-03-20')
    clickDone()
    // 10:30 Tokyo on the new day, whatever zone the runner is in.
    expect(onChange).toHaveBeenCalledWith('2027-03-20T01:30:00.000Z')
  }, PICKER_FLOW_TIMEOUT)

  it('a zone picked after the day and time keeps that wall-clock time, in the new zone', () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    openPicker('picker')
    clickDay('2027-03-15')
    typeTime('17:00')
    fireEvent.change(zoneSelect(), { target: { value: 'America/New_York' } })
    clickDone()
    // 17:00 New York (EDT, GMT-4) — not the instant first picked in the runner's zone.
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('2027-03-15T21:00:00.000Z')
  }, PICKER_FLOW_TIMEOUT)

  it('a preset saves at once: 1 day from now, at 17:00 in the picked zone', () => {
    const onChange = vi.fn()
    render(<Harness onChange={onChange} />)
    openPicker('picker')
    fireEvent.change(zoneSelect(), { target: { value: 'Asia/Tokyo' } })
    clickPreset('1 day')

    expect(onChange).toHaveBeenCalledTimes(1)
    const saved = new Date(onChange.mock.calls[0][0])
    // 17:00 Tokyo is 08:00Z, on the Tokyo calendar day after today.
    expect(saved.toISOString()).toMatch(/T08:00:00\.000Z$/)
    const tokyoDay = (d: Date) => d.toLocaleDateString('en-CA', { timeZone: 'Asia/Tokyo' })
    const tomorrow = new Date(Date.now() + 24 * 3600 * 1000)
    expect(tokyoDay(saved)).toBe(tokyoDay(tomorrow))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('takes its own presets, or none', () => {
    const { unmount } = render(
      <DeadlinePicker value={null} onChange={() => {}} presets={[{ label: 'Next year', resolve: n => new Date(n.getTime() + 365 * 864e5) }]} aria-label="Deadline" data-testid="picker" />,
    )
    openPicker('picker')
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Next year' })).toBeInTheDocument()
    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: '1 day' })).toBeNull()
    unmount()

    render(<DeadlinePicker value={null} onChange={() => {}} presets={[]} aria-label="Deadline" data-testid="picker" />)
    openPicker('picker')
    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: '1 day' })).toBeNull()
  })

  it('Clear in the chip\'s panel commits null', () => {
    const onChange = vi.fn()
    render(<Harness initial="2027-03-15T17:00:00Z" onChange={onChange} />)
    clearDeadline('picker')
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith(null)
    expect(screen.getByTestId('picker')).toHaveTextContent('Set due date')
  })

  it('the field variant\'s X clears straight away, without opening', () => {
    const onChange = vi.fn()
    render(<Harness initial="2027-03-15T17:00:00Z" onChange={onChange} variant="field" />)
    fireEvent.click(screen.getByTestId('picker-clear'))
    expect(onChange).toHaveBeenCalledWith(null)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('cannot be opened when disabled', () => {
    render(
      <DeadlinePicker value="2027-03-15T17:00:00Z" onChange={() => {}} disabled aria-label="Deadline" data-testid="picker" />,
    )
    expect(screen.getByTestId('picker')).toBeDisabled()
    fireEvent.click(screen.getByTestId('picker'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
