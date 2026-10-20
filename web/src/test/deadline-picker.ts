// Drives a DeadlinePicker / DateTimePicker through its popover, as a user
// would: open, page to the month, click the day, optionally type a time, close
// with Done (which is when DeadlinePicker commits). fireEvent, not userEvent,
// so it also runs under fake timers.

import { act, fireEvent, screen, within } from '@testing-library/react'

// By slot, not by role: the picker may sit inside a modal, which is a dialog too.
function panel(): HTMLElement {
  const open = document.querySelectorAll<HTMLElement>('[data-slot="popover-content"]')
  if (open.length !== 1) throw new Error(`expected one open picker, found ${open.length}`)
  return open[0]
}

function clickButton(el: Element) {
  act(() => { fireEvent.click(el) })
}

/** Opens the picker with this test id. */
export function openPicker(testId: string): HTMLElement {
  clickButton(screen.getByTestId(testId))
  return panel()
}

/**
 * Per-test budget for a test that opens a picker and pages its calendar. Every
 * step re-renders the open panel, zone list included: about half a second for
 * the chain on an idle machine, which can outrun vitest's 5s default in the
 * full run.
 */
export const PICKER_FLOW_TIMEOUT = 15_000

/** Pages the open calendar to `day` ("yyyy-MM-dd") and clicks it. */
export function clickDay(day: string) {
  for (let i = 0; i < 60; i++) {
    const cell = panel().querySelector(`[data-day="${day}"]:not([data-outside])`)
    if (cell) {
      clickButton(cell.querySelector('button')!)
      return
    }
    const shown = panel().querySelector('[data-day]:not([data-outside])')!.getAttribute('data-day')!
    clickButton(within(panel()).getByRole('button', { name: day > shown ? /next month/i : /previous month/i }))
  }
  throw new Error(`calendar never reached ${day}`)
}

export function typeTime(time: string) {
  act(() => { fireEvent.change(within(panel()).getByLabelText('Time'), { target: { value: time } }) })
}

export function clickDone() {
  clickButton(within(panel()).getByRole('button', { name: 'Done' }))
}

/** Clicks a quick pick ("1 day", "2 days" …) in the open picker. */
export function clickPreset(label: string) {
  clickButton(within(panel()).getByRole('button', { name: label }))
}

/** Open, pick `day` (and `time`, "HH:mm", in the picker's zone), Done. */
export function pickDeadline(testId: string, day: string, time?: string) {
  openPicker(testId)
  clickDay(day)
  if (time) typeTime(time)
  clickDone()
}

/** Open and press the panel's Clear. */
export function clearDeadline(testId: string) {
  openPicker(testId)
  clickButton(within(panel()).getByRole('button', { name: 'Clear' }))
}

/**
 * The zone picker, with Combobox mocked as a native <select>
 * (`@/test/combobox-mock`). The time field is a combobox too, so find it by tag.
 */
export function zoneSelect(): HTMLSelectElement {
  return panel().querySelector('select')!
}
