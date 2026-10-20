import { useLayoutEffect, useRef, type RefObject } from 'react'

/**
 * A ref that holds the latest `value`, for event handlers and effects that
 * must read it without re-subscribing when it changes (a caller's inline
 * callback, say). Updated after every commit, before paint, so a handler
 * never sees a value older than the screen. Never read it during render.
 */
export function useLatestRef<T>(value: T): RefObject<T> {
  const ref = useRef(value)
  useLayoutEffect(() => {
    ref.current = value
  })
  return ref
}
