import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react'
import { BulkDeadlineButton } from './BulkDeadlineButton'
import { clearDeadline, clickDay, clickDone, clickPreset, openPicker, typeTime, zoneSelect, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'

// The zone picker becomes a native <select>; zoneSelect() finds it.
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

afterEach(() => cleanup())

const setup = () => {
  const onApply = vi.fn()
  render(<BulkDeadlineButton onApply={onApply} pending={false} disabled={false} />)
  return onApply
}

// It writes to every selected job, so only Done and Clear may act.
describe('BulkDeadlineButton', () => {
  it('Done applies the picked instant, as UTC, once', () => {
    const onApply = setup()
    openPicker('bulk-deadline-btn')
    fireEvent.change(zoneSelect(), { target: { value: 'Asia/Tokyo' } })
    clickDay('2027-03-15')
    typeTime('10:30')
    expect(onApply, 'applied before Done').not.toHaveBeenCalled()

    clickDone()
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onApply).toHaveBeenCalledWith('2027-03-15T01:30:00.000Z')
  }, PICKER_FLOW_TIMEOUT)

  it('a preset applies at once, like Done', () => {
    const onApply = setup()
    openPicker('bulk-deadline-btn')
    clickPreset('2 days')
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onApply.mock.calls[0][0]).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00\.000Z$/)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('closing without Done applies nothing, and the next open starts empty', () => {
    const onApply = setup()
    openPicker('bulk-deadline-btn')
    clickDay('2027-03-15')
    act(() => { fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' }) })
    expect(onApply).not.toHaveBeenCalled()

    openPicker('bulk-deadline-btn')
    expect(within(screen.getByRole('dialog')).getByLabelText('Time')).toHaveValue('')
  }, PICKER_FLOW_TIMEOUT)

  it('Clear removes the deadlines even though nothing is picked', () => {
    const onApply = setup()
    clearDeadline('bulk-deadline-btn')
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onApply).toHaveBeenCalledWith(null)
  })

  it('Done with no date picked applies nothing', () => {
    const onApply = setup()
    openPicker('bulk-deadline-btn')
    clickDone()
    expect(onApply).not.toHaveBeenCalled()
  })
})
