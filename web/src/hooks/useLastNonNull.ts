import { useState } from 'react'

/**
 * The value, or — once it goes null — the last non-null value it held.
 *
 * For dialogs opened by a nullable target (`open={target !== null}`): the
 * popup stays mounted through its close animation, so it must keep rendering
 * the target it was opened with after the parent clears it.
 *
 * `value` must be referentially stable while non-null (state, not a fresh
 * array per render) — each new reference is stored.
 */
export function useLastNonNull<T>(value: T | null | undefined): T | null | undefined {
  const [last, setLast] = useState(value)
  if (value != null && value !== last) setLast(value)
  return value ?? last
}
