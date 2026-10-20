import { useCallback, useRef, useState } from 'react'

/**
 * A dialog a caller awaits: `request()` opens it and resolves with what the
 * dialog settles (`settle(result)`, or `settle(null)` when it closes without
 * one). The resolver lives in a ref so `settle` claims and clears it
 * synchronously: a dialog that fires onCreated and then onOpenChange(false) in
 * the same frame resolves once. A second `request()` while the dialog is open
 * resolves null at once, so no promise is left pending, and skips its `onOpen`.
 *
 * `onOpen` runs only when the dialog actually opens, to preset its fields.
 * `settle` also takes a promise: the dialog closes at once and the caller's
 * promise resolves when that one does, for a result that needs one more fetch.
 */
export function useDialogPromise<T>(): {
  open: boolean
  request: (onOpen?: () => void) => Promise<T | null>
  settle: (result: T | null | PromiseLike<T | null>) => void
} {
  const [open, setOpen] = useState(false)
  const resolverRef = useRef<((result: T | null | PromiseLike<T | null>) => void) | null>(null)

  const request = useCallback((onOpen?: () => void) => {
    if (resolverRef.current !== null) return Promise.resolve(null)
    return new Promise<T | null>((resolve) => {
      resolverRef.current = resolve
      onOpen?.()
      setOpen(true)
    })
  }, [])

  const settle = useCallback((result: T | null | PromiseLike<T | null>) => {
    const resolve = resolverRef.current
    resolverRef.current = null
    setOpen(false)
    resolve?.(result)
  }, [])

  return { open, request, settle }
}
