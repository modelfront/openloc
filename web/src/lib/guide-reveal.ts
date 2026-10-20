// A tour step asks the element it points at to show itself first, such as a
// collapsed section of a form or a dialog. The tour fires it as it moves to a
// step (lib/guide.ts); a component listens with useGuideReveal. The step's
// anchor is kept until the tour ends, so a section that mounts later (its page
// was still loading) opens too.
import { useEffect, useRef } from 'react'

export const GUIDE_REVEAL_EVENT = 'openloc:guide-reveal'

type RevealDetail = { anchor: string | null }

let revealed: string | null = null

function dispatch(anchor: string | null) {
  revealed = anchor
  window.dispatchEvent(new CustomEvent<RevealDetail>(GUIDE_REVEAL_EVENT, { detail: { anchor } }))
}

export function revealAnchor(anchor: string) {
  dispatch(anchor)
}

/** The tour is over: nothing stays revealed. */
export function endReveal() {
  dispatch(null)
}

/**
 * Runs onReveal when a tour step points at `anchor` (or one of several), or
 * already does on mount; onLeave when the tour moves from one of them to
 * another step, or ends.
 */
export function useGuideReveal(
  anchor: string | readonly string[] | undefined,
  onReveal: () => void,
  onLeave?: () => void,
) {
  const cb = useRef({ onReveal, onLeave })
  useEffect(() => { cb.current = { onReveal, onLeave } })
  const key = anchor === undefined ? '' : typeof anchor === 'string' ? anchor : anchor.join('\n')
  useEffect(() => {
    if (!key) return
    const anchors = key.split('\n')
    let shown = false
    const reveal = (a: string | null) => {
      if (a !== null && anchors.includes(a)) {
        shown = true
        cb.current.onReveal()
      } else if (shown) {
        shown = false
        cb.current.onLeave?.()
      }
    }
    if (revealed !== null && anchors.includes(revealed)) reveal(revealed)
    const listener = (e: Event) => reveal((e as CustomEvent<RevealDetail>).detail?.anchor ?? null)
    window.addEventListener(GUIDE_REVEAL_EVENT, listener)
    return () => window.removeEventListener(GUIDE_REVEAL_EVENT, listener)
  }, [key])
}
