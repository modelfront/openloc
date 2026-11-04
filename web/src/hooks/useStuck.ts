import { useEffect, useRef, useState } from 'react'

/** The nearest ancestor that scrolls vertically, or null for the page. */
function scrollParent(el: Element): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p
  }
  return null
}

/**
 * True while a `position: sticky` element is pinned to the top edge of its
 * scroll area: the area is scrolled and the element's top sits on that edge.
 * It is false again once the end of the element's parent pushes it off the
 * edge. The element must stick at the edge itself (its `top` cancels the
 * area's top padding).
 */
export function useStuck<T extends Element>() {
  const ref = useRef<T>(null)
  const [stuck, setStuck] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const area = scrollParent(el)
    const target: HTMLElement | Window = area ?? window
    let frame = 0
    const measure = () => {
      frame = 0
      const edge = area ? area.getBoundingClientRect().top : 0
      const scrolled = area ? area.scrollTop > 0 : window.scrollY > 0
      setStuck(scrolled && Math.abs(el.getBoundingClientRect().top - edge) < 1)
    }
    const onScroll = () => { if (!frame) frame = requestAnimationFrame(measure) }
    measure()
    target.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    return () => {
      target.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [])
  return [ref, stuck] as const
}
