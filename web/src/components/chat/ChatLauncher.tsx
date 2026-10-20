import { cn } from '@/lib/utils'
import { ProviderLogo } from './ModelPicker'

/** The assistant window's frame, bottom right, shared by both panels. */
export const chatWindowClassName =
  'fixed bottom-5 right-5 z-40 flex max-h-[calc(100vh-6rem)] max-w-[calc(100vw-2.5rem)] flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-gray-800 dark:bg-gray-900'

/** The header's read-only / write badge. */
export function ChatModeBadge({ allowWrites }: { allowWrites: boolean }) {
  return (
    <span
      data-testid="chat-mode-badge"
      data-guide="chat.mode"
      className={cn('text-[10px] uppercase tracking-wide', allowWrites ? 'text-amber-600 dark:text-amber-500' : 'text-gray-400')}
    >
      {allowWrites ? 'write' : 'read-only'}
    </span>
  )
}

/**
 * The round assistant button, bottom right. Shows the provider's mark
 * (greyed when no key is connected), not a generic icon — the chat is branded
 * by what answers it. Shared by the real panel and the demo organization's
 * example panel.
 */
export function ChatLauncher({ provider, muted, title, onOpen }: {
  provider: string
  muted?: boolean
  title: string
  onOpen: () => void
}) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- chat launcher, not an action button
    <button
      data-testid="chat-launcher"
      data-guide="chat.launcher"
      data-provider={provider}
      onClick={onOpen}
      className={cn(
        'fixed bottom-5 right-5 z-40 flex h-11 w-11 items-center justify-center rounded-full border shadow-lg transition-colors',
        'border-gray-200 bg-white hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-900 dark:hover:bg-gray-800',
        muted && 'text-gray-300 dark:text-gray-600',
      )}
      title={title}
    >
      <ProviderLogo provider={provider} muted={muted} className="h-5 w-5" />
    </button>
  )
}
