import { useState } from 'react'

/**
 * The previous array while `next` holds the same items in the same order, so
 * a list recomputed on every change keeps its identity until it really
 * changes. Costs one pass over the items per render.
 */
export function useStableArray<T>(next: T[]): T[] {
  const [prev, setPrev] = useState(next)
  const same = prev === next || (prev.length === next.length && prev.every((v, i) => v === next[i]))
  if (!same) setPrev(next)
  return same ? prev : next
}
