// useState backed by localStorage: the value is read once on mount and written
// back on every change. `parse` turns whatever is stored into a valid value, so
// a stale or hand-edited entry falls back to a default instead of reaching the
// UI. Storage that throws (blocked, full, private mode) degrades to plain
// in-memory state.
//
// Values are stored as JSON by default, and `parse` gets the decoded value (or
// null when nothing is stored, or the entry is not valid JSON). With
// `{ raw: true }` a string value is stored as-is and `parse` gets the stored
// string — for keys whose entries are bare strings like `write`, which are not
// JSON.

import { useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import { readStorage, writeStorage } from '@/lib/safe-storage'

function decodeJSON(raw: string | null): unknown {
  if (raw == null) return null
  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

export function useStoredState<T>(key: string, parse: (stored: unknown) => T): readonly [T, Dispatch<SetStateAction<T>>]
export function useStoredState<T extends string>(key: string, parse: (stored: string | null) => T, options: { raw: true }): readonly [T, Dispatch<SetStateAction<T>>]
export function useStoredState<T>(key: string, parse: (stored: never) => T, options?: { raw: true }): readonly [T, Dispatch<SetStateAction<T>>] {
  const raw = options?.raw === true
  const [value, setValue] = useState<T>(() => {
    const stored = readStorage(key)
    return (parse as (s: unknown) => T)(raw ? stored : decodeJSON(stored))
  })
  useEffect(() => {
    writeStorage(key, raw ? String(value) : JSON.stringify(value))
  }, [key, value, raw])
  return [value, setValue] as const
}
