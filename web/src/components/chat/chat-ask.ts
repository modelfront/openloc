/**
 * A pause tool's request in an assistant message: add a screenshot
 * (ask_for_screenshot) or repeat the steps while the browser records them
 * (ask_to_repeat). The user's answer goes back as their next message.
 */
export interface ChatAsk {
  kind: 'screenshot' | 'repeat'
  /** Why the assistant asks, shown by the button. */
  reason: string
}

/** Sends the user's answer, with the screenshot they approved; resolves to whether the assistant answered. */
export type ChatSend = (text: string, extra?: { screenshotUploadId?: string }) => Promise<boolean>

/** The ask a client action carries, or undefined for any other action. */
export function askFromAction(action: string, reason: string | undefined): ChatAsk | undefined {
  if (!reason) return undefined
  if (action === 'ask_screenshot') return { kind: 'screenshot', reason }
  if (action === 'ask_repeat') return { kind: 'repeat', reason }
  return undefined
}
