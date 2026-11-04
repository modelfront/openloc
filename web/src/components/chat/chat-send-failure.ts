import { ApiError } from '@/api/request'
import { notify } from '@/lib/errtrack'

/**
 * The text the panel shows for a send that rejected. An API error carries the
 * server's message for the user. sendChatMessage reports a failed or dropped
 * connection through onError, so anything else here is a bug: the user gets a
 * plain sentence instead of its raw text, and the error is reported.
 */
export function reportSendFailure(err: unknown): string {
  if (err instanceof ApiError) return err.message
  notify(err, { context: { action: 'chat-send' } })
  return 'Something went wrong. Try again.'
}
