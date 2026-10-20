import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { isDemoReadOnly } from '@/lib/demo'

interface MutationWithToastOptions<TData, TVariables> {
  mutationFn: (vars: TVariables) => Promise<TData>
  /**
   * Fixed text, or a function of the result when the message depends on what
   * happened ("Replaced 'x' in 4 segments"). A function may return null when
   * nothing happened worth announcing — the user backed out of a confirmation
   * the mutation raised along the way, or the caller shows a different kind
   * of notice itself.
   */
  successMessage: string | ((data: TData, vars: TVariables) => string | null)
  errorMessage?: string
  /** Set false when the caller renders the rejection itself (e.g. inline in a
   *  dialog via `mutateAsync`) so the user does not see it twice. A function
   *  decides per error: false for the errors `onError` already announces. */
  errorToast?: boolean | ((err: Error) => boolean)
  invalidateKeys?: readonly (readonly unknown[])[]
  onSuccess?: (data: TData, vars: TVariables) => void
  /** Runs on every failure, whether or not a toast is shown. */
  onError?: (err: Error) => void
}

/**
 * Wraps useMutation with toast notifications and automatic query invalidation.
 *
 * On success: shows toast, invalidates specified query keys, calls optional onSuccess.
 * On error: shows toast with the error message.
 */
export function useMutationWithToast<TData = unknown, TVariables = void>({
  mutationFn,
  successMessage,
  errorMessage,
  errorToast = true,
  invalidateKeys,
  onSuccess,
  onError,
}: MutationWithToastOptions<TData, TVariables>) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn,
    onSuccess: (data, vars) => {
      if (invalidateKeys) {
        for (const key of invalidateKeys) {
          queryClient.invalidateQueries({ queryKey: key })
        }
      }
      onSuccess?.(data, vars)
      const message = typeof successMessage === 'function' ? successMessage(data, vars) : successMessage
      if (message) toast.success(message)
    },
    onError: (err: Error) => {
      onError?.(err)
      // The demo organization's refusal has one notice, shown by the query
      // client for every mutation (main.tsx).
      const show = typeof errorToast === 'function' ? errorToast(err) : errorToast
      if (!show || isDemoReadOnly(err)) return
      toast.error(errorMessage ? `${errorMessage}: ${err.message}` : err.message)
    },
  })
}
