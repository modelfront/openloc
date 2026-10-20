// The bordered card an assistant answer shows for something the user acts
// on: a confirm card, a request for a screenshot or a recording.
import type { ComponentProps, ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function ChatCard({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('my-1 rounded-lg border border-gray-200 px-2 py-1.5 dark:border-gray-700', className)} {...props} />
}

/** The card's text: what the card is about. */
export function ChatCardText({ className, ...props }: ComponentProps<'span'>) {
  return <span className={cn('text-xs text-gray-700 dark:text-gray-300', className)} {...props} />
}

/**
 * A request from the assistant: why it asks, what the user can do, and once
 * they did it, what happened.
 */
export function ChatAskCard({ reason, status, state, testId, children }: {
  reason: string
  /** Shown once the request is answered. */
  status?: ReactNode
  state: string
  testId: string
  children?: ReactNode
}) {
  return (
    <ChatCard className="space-y-2" data-testid={testId} data-state={state}>
      <ChatCardText className="block">{reason}</ChatCardText>
      {children}
      {status && <p className="text-xs text-muted-foreground">{status}</p>}
    </ChatCard>
  )
}

/** Why a request's buttons wait while the answer that brought it still streams. */
export const WAIT_FOR_ANSWER = 'Wait for the answer to finish.'
