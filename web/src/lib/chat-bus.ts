// Tiny event bus for opening the built-in chat with a prefilled prompt
// (prompt cards). The chat panel is mounted once at the app
// root while the "ask the assistant to…" cards live on arbitrary pages — a
// window event is the lightest coupling that survives both.
//
// The prompt only PREFILLS the input; the user still presses Enter. Nothing
// auto-sends on a click.

export const CHAT_PROMPT_EVENT = 'openloc:chat-prompt'

export interface ChatPromptDetail {
  /** Prefills the input — ready to send with Enter. */
  prompt?: string
  /** Greyed placeholder only — NOT sendable; the user types from scratch. */
  hint?: string
}

export function openChatWithPrompt(prompt: string) {
  window.dispatchEvent(new CustomEvent<ChatPromptDetail>(CHAT_PROMPT_EVENT, { detail: { prompt } }))
}

export function openChatWithHint(hint: string) {
  window.dispatchEvent(new CustomEvent<ChatPromptDetail>(CHAT_PROMPT_EVENT, { detail: { hint } }))
}

/** Opens the assistant panel as it is, with nothing prefilled (guided tours). */
export const CHAT_OPEN_EVENT = 'openloc:chat-open'

export function openChat() {
  window.dispatchEvent(new Event(CHAT_OPEN_EVENT))
}

/** Puts the assistant panel away again (a tour moving on from it). */
export const CHAT_CLOSE_EVENT = 'openloc:chat-close'

export function closeChat() {
  window.dispatchEvent(new Event(CHAT_CLOSE_EVENT))
}
