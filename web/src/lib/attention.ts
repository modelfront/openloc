// Draws the eye to something that is not where the user is looking: scrolls
// it to the middle of the screen and pulses a ring around it for a moment.
// The ring is CSS keyed on `data-attention` (index.css), so any element can
// take it — a card further down this page, or, through `attentionHref` and
// `useAttentionTarget` (hooks/useAttention.tsx), one on another page.

// The ring pulses PULSES times, PULSE_MS each, back to back. The element gets
// both as custom properties, so the keyframes in index.css and the removal
// timer below run on the same numbers.
const PULSES = 3
const PULSE_MS = 700

// The removal timer of each ringed element, so a second call restarts the
// ring instead of being cut short when the first timer fires.
const timers = new WeakMap<HTMLElement, number>()

/** The query parameter a link carries to point at an element on the page it
 *  opens: `?attention=<key>`, claimed by the `useAttentionTarget(key)` there. */
export const ATTENTION_PARAM = 'attention'

/** A link that opens `path` and points at the element declaring `key`. */
export function attentionHref(path: string, key: string): string {
  const [base, query = ''] = path.split('?')
  const params = new URLSearchParams(query)
  params.set(ATTENTION_PARAM, key)
  return `${base}?${params}`
}

interface Target {
  /** The element to scroll to and ring, or a CSS selector for it. */
  target: string | HTMLElement
  /** CSS selector of a control inside it to focus, when one is wanted. */
  focus?: string
  framesLeft?: number
}

/** Waits a few frames for the target when the page that holds it is still
 *  rendering, so it can follow a navigation. */
export function drawAttention({ target, focus, framesLeft = 20 }: Target) {
  const el = typeof target === 'string' ? document.querySelector<HTMLElement>(target) : target
  if (!el) {
    if (framesLeft > 0) requestAnimationFrame(() => drawAttention({ target, focus, framesLeft: framesLeft - 1 }))
    return
  }
  el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  if (focus) document.querySelector<HTMLElement>(focus)?.focus({ preventScroll: true })
  // A second call while the ring still shows restarts it: the attribute has
  // to leave and come back for the animation to run again.
  delete el.dataset.attention
  void el.offsetWidth
  el.style.setProperty('--attention-pulses', String(PULSES))
  el.style.setProperty('--attention-pulse-ms', `${PULSE_MS}ms`)
  el.dataset.attention = ''
  window.clearTimeout(timers.get(el))
  timers.set(el, window.setTimeout(() => { delete el.dataset.attention; timers.delete(el) }, PULSES * PULSE_MS))
}
