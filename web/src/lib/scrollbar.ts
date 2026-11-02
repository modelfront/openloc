/**
 * Whether the browser draws overlay scrollbars (macOS's default): they take no
 * width from their box and draw over the content's right edge, wider on hover.
 * Measured on a hidden probe that always shows its scrollbar.
 */
export function hasOverlayScrollbars(): boolean {
  const probe = document.createElement('div')
  probe.style.cssText = 'position:absolute;top:-9999px;width:100px;height:100px;overflow:scroll'
  document.body.appendChild(probe)
  const overlay = probe.offsetWidth === probe.clientWidth
  probe.remove()
  return overlay
}
