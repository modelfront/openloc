import { useEffect, useRef, useState } from 'react'

/**
 * True once the element has come within `rootMargin` of the viewport, and true
 * from then on: for work worth starting only when it is about to be seen, and
 * not worth undoing when it scrolls away again.
 */
export function useInView<T extends Element>(rootMargin = '200px') {
  const ref = useRef<T>(null)
  const [seen, setSeen] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (seen || !el) return
    const observer = new IntersectionObserver(
      entries => { if (entries.some(e => e.isIntersecting)) setSeen(true) },
      { rootMargin },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [seen, rootMargin])
  return [ref, seen] as const
}
