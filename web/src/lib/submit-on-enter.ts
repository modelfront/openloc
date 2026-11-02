import type { KeyboardEvent } from 'react'

/**
 * onKeyDown for a dialog's fields: Enter submits. A dialog renders in a
 * portal, so a `<form>` inside it would still submit the page's own form
 * through React's event bubbling; dialogs use this on a wrapper instead, and
 * the event stops here. Enter in a text area still adds a line.
 */
export function submitOnEnter(submit: () => unknown) {
  return (e: KeyboardEvent) => {
    if (e.key !== 'Enter' || e.target instanceof HTMLTextAreaElement) return
    e.preventDefault()
    e.stopPropagation()
    void submit()
  }
}
