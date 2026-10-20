import { ApiError } from '@/api/request'

/** A server error split into the form field it names (`error.field`) and its message. */
export function serverFieldError(err: unknown): { field?: string; message: string } | null {
  if (!err) return null
  const message = err instanceof Error ? err.message : String(err)
  if (err instanceof ApiError) {
    const field = (err.body as { error?: { field?: unknown } } | undefined)?.error?.field
    if (typeof field === 'string' && field) return { field, message }
  }
  return { message }
}
