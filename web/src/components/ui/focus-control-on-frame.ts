import type { MouseEvent } from 'react'

/** Anything inside the field that owns its own click and must keep it. */
const INTERACTIVE = 'button, a, input, select, textarea, [tabindex], [role="button"]'

/**
 * `onMouseDown` for the box `Input` and `Textarea` draw around their element.
 *
 * The box owns the padding, so a click on the frame, the leading icon or a
 * static action (a `%` suffix, a spinner, a kbd hint) lands on a div and
 * focuses nothing — a field is expected to focus anywhere you click it.
 * Interactive content keeps its own click, which is why this tests the target
 * rather than focusing unconditionally.
 */
export function focusControlOnFrame(e: MouseEvent<HTMLElement>) {
  const target = e.target as HTMLElement
  // Scoped to the box: an interactive ANCESTOR of the field (a rename inside a
  // row link) is not the thing being clicked and must not suppress the focus.
  const interactive = target.closest(INTERACTIVE)
  if (interactive && interactive !== e.currentTarget && e.currentTarget.contains(interactive)) return
  const control = e.currentTarget.querySelector<HTMLElement>('[data-slot="input-control"]')
  if (!control || control.matches(':disabled')) return
  // The default would move focus to the div; take it to the element instead.
  e.preventDefault()
  control.focus()
}
