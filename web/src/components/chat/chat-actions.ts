// What the panel does with a client_action event — the assistant asking the
// browser to act.
// Paths and anchors come from the server-side page registry; the path check
// here is belt-and-braces.
import type { QueryClient } from '@tanstack/react-query'
import type { ClientActionPayload } from '../../api'
import { attachReplies, runGuide } from '../../lib/guide'
import { runSavedTour } from '../../lib/playbooks'
import type { DisplayMessage } from './ChatMessageBubble'
import { askFromAction } from './chat-ask'

export interface ClientActionContext {
  navigate: (path: string) => void
  /** The address of a docs page: on its own host when it has one (siteLink). */
  docsLink: (path: string) => string
  queryClient: QueryClient
  /** Minimizes the panel so a tour is not hidden behind it. */
  minimize: () => void
  patchLast: (patch: (m: DisplayMessage) => DisplayMessage) => void
  send: (text: string) => void
  /** Shows reply buttons above the message box. */
  showReplies: (replies: string[]) => void
}

/** Same-app relative paths only, never anything origin-shaped. */
export function safePath(path: string | undefined): string | undefined {
  return path && path.startsWith('/') && !path.startsWith('//') ? path : undefined
}

/** The docs and the API docs render outside the app shell, so the panel would unmount there. */
const DOCS_PATH = /^\/(api-)?docs(?:[/?#]|$)/

export function handleClientAction(action: string, payload: ClientActionPayload, ctx: ClientActionContext): void {
  switch (action) {
    case 'navigate': {
      const path = safePath(payload.path)
      if (!path) return
      // A docs page opens in a new tab, like the account menu, so the
      // conversation stays open beside it.
      if (DOCS_PATH.test(path)) window.open(ctx.docsLink(path), '_blank', 'noopener')
      else ctx.navigate(path)
      return
    }
    case 'highlight':
      if (payload.anchor && payload.caption) {
        runGuide([{ path: safePath(payload.path), anchor: payload.anchor, body: payload.caption }], ctx.navigate)
      }
      return
    case 'tour':
      if (payload.steps?.length) {
        ctx.minimize()
        runGuide(payload.steps.map(s => ({ ...s, path: safePath(s.path) })), ctx.navigate)
      }
      return
    case 'playbook_tour':
      if (payload.playbook) {
        runSavedTour(ctx.queryClient, payload.playbook, ctx.navigate)
          .then(started => {
            if (started) ctx.minimize()
            else ctx.patchLast(m => ({ ...m, error: 'This tour has nothing to show yet.' }))
          })
          .catch((err: Error) => ctx.patchLast(m => ({ ...m, error: `Could not start the tour: ${err.message}` })))
      }
      return
    case 'suggest_replies': {
      const labels = payload.replies
      if (!labels?.length) return
      // On the highlight card when one is showing, else above the input.
      const inCard = attachReplies({ labels, onPick: ctx.send, onDismiss: () => ctx.showReplies(labels) })
      if (!inCard) ctx.showReplies(labels)
      return
    }
    case 'ask_screenshot':
    case 'ask_repeat': {
      const ask = askFromAction(action, payload.reason)
      if (ask) ctx.patchLast(m => ({ ...m, ask }))
      return
    }
    case 'render_chart':
      if (payload.chart) {
        const chart = payload.chart
        ctx.patchLast(m => ({ ...m, charts: [...m.charts, chart] }))
      }
      return
  }
}
