import type { KeyboardEvent } from 'react'

export interface RowButtonProps {
  role: 'button'
  tabIndex: 0
  onClick: () => void
  onKeyDown: (e: KeyboardEvent<HTMLElement>) => void
}

/**
 * Props that make a non-button element (a list row, a card, a section header)
 * act as a button: focusable, clicked by mouse, and activated by Enter or
 * Space. Spread them on the element:
 *
 *   <div {...rowButtonProps(canWrite ? onEdit : undefined)} />
 *
 * Pass `undefined` for a read-only element: it gets no props at all, so it is
 * neither tabbable nor announced as a button.
 *
 * Only a key pressed on the element itself activates it. A nested control (a
 * Delete button, a link) is focused on its own, and its Enter or Space keeps
 * its native meaning instead of activating the row too. Space calls
 * `preventDefault` so it does not scroll the page.
 */
export function rowButtonProps(onActivate: (() => void) | undefined): Partial<RowButtonProps> {
  if (!onActivate) return {}
  return {
    role: 'button',
    tabIndex: 0,
    onClick: () => onActivate(),
    onKeyDown: e => {
      if (e.target !== e.currentTarget) return
      if (e.key !== 'Enter' && e.key !== ' ') return
      e.preventDefault()
      onActivate()
    },
  }
}
