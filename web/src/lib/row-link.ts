import type { MouseEvent } from 'react'

/**
 * Opens a list row's page as a link would: in a new tab on Cmd/Ctrl+click or
 * a middle click, else in this tab through navigate.
 */
export function openRowLink(event: MouseEvent, href: string, navigate: (href: string) => void) {
  if (event.metaKey || event.ctrlKey || event.button === 1) {
    window.open(href, '_blank', 'noopener')
    return
  }
  navigate(href)
}
