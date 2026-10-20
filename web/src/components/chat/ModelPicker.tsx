// Model picker for the built-in chat: current model's
// provider logo + label on a light button; dropdown lists the models this org's
// keys can reach, grouped by provider. Unselectable rows render greyed with the
// reason, so a model that exists but can't be used says why.
//
// A vendor with no key contributes no rows at all: the list comes from each
// vendor's API, called with the org's own key, so there is
// nothing to enumerate until a key exists.
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { siClaude } from 'simple-icons'
import { providerMark } from '../ai/provider-marks'
import { ChevronDown } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import { useStoredState } from '@/hooks/useStoredState'
import { readStorage } from '@/lib/safe-storage'
import { listChatModels } from '../../api'
import { queryKeys } from '../../lib/query-keys'

// A bare model id, not JSON.
const MODEL_STORAGE_KEY = 'openloc-chat-model'

/** The chat model the user picked last, remembered per browser; '' when none. */
export function useStoredModel() {
  return useStoredState(MODEL_STORAGE_KEY, stored => stored ?? '', { raw: true })
}

// Brand colour for the non-OpenAI marks. OpenAI's is black on light / white on
// dark (its published usage), which is a class rather than a colour, so it is
// handled separately below. `muted` drops to currentColor so disabled/greyed
// contexts inherit their gray.
const CLAUDE_HEX = `#${siClaude.hex}` // #D97757

/**
 * Provider of the currently selected chat model ("claude" until models load).
 * Shared by the chat launcher/header and the dashboard prompt cards so every
 * assistant-branded surface shows the SAME mark.
 */
export function useChatProvider(): string {
  const { data: models } = useQuery({
    queryKey: queryKeys.chat.models,
    queryFn: listChatModels,
    staleTime: 5 * 60_000,
  })
  const stored = readStorage(MODEL_STORAGE_KEY)
  const current = models?.find(m => m.id === stored) ?? models?.find(m => m.default)
  return current?.provider ?? 'claude'
}

export function ProviderLogo({ provider, className, muted }: { provider: string; className?: string; muted?: boolean }) {
  // Paths come from the shared registry, so the chat and the AI settings badges
  // cannot end up drawing the same vendor differently.
  const path = providerMark(provider)?.path ?? siClaude.path
  return (
    <svg
      viewBox="0 0 24 24"
      className={cn(!muted && provider === 'openai' && 'text-black dark:text-white', className)}
      style={!muted && provider !== 'openai' ? { color: CLAUDE_HEX } : undefined}
      aria-hidden
      fill="currentColor"
    >
      <path d={path} />
    </svg>
  )
}

interface ModelPickerProps {
  /** Currently selected model id (validated by the parent). */
  model: string
  onSelect: (model: string) => void
  disabled?: boolean
}

export function ModelPicker({ model, onSelect, disabled }: ModelPickerProps) {
  const { data: models, isPending, isError } = useQuery({
    queryKey: queryKeys.chat.models,
    queryFn: listChatModels,
    staleTime: 5 * 60_000,
  })

  // If the stored selection is no longer selectable (key revoked, model
  // retired from the registry), snap back to the default rather than sending
  // a doomed model with every message.
  useEffect(() => {
    if (!models) return
    const current = models.find(m => m.id === model)
    if (current?.selectable) return
    const fallback = models.find(m => m.default && m.selectable) ?? models.find(m => m.selectable)
    if (fallback && fallback.id !== model) onSelect(fallback.id)
  }, [models, model, onSelect])

  const current = models?.find(m => m.id === model)
  const anySelectable = models?.some(m => m.selectable) ?? false
  const label = current?.label ?? 'Model'
  const provider = current?.provider ?? 'claude'

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        data-testid="chat-model-picker"
        disabled={disabled}
        className={cn(
          // Borderless, like the composer controls it sits between — the
          // surrounding box already carries the input's border.
          'inline-flex h-7 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs transition-colors',
          anySelectable
            ? 'text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200'
            : 'text-gray-300 dark:text-gray-600', // no key anywhere → greyed logo
        )}
        title={anySelectable ? 'Choose a model' : 'No AI provider connected'}
      >
        <ProviderLogo provider={provider} muted={!anySelectable} className="h-3.5 w-3.5" />
        <span className="max-w-24 truncate">{label}</span>
        <ChevronDown className="h-3 w-3 opacity-60" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        {/* Every state that yields no rows says so. `models` is undefined while
            loading and on error, so testing only for an empty array would open
            an empty menu with no explanation. */}
        {!models?.length && (
          <DropdownMenuItem disabled className="text-xs opacity-50">
            {isPending
              ? 'Loading models…'
              : isError
                ? "Couldn't load models"
                : 'No AI provider connected'}
          </DropdownMenuItem>
        )}
        {(models ?? []).map(m => (
          <DropdownMenuItem
            key={m.id}
            data-testid={`chat-model-${m.id}`}
            disabled={!m.selectable}
            onClick={() => onSelect(m.id)}
            className={cn('gap-2', !m.selectable && 'opacity-50')}
          >
            <ProviderLogo provider={m.provider} muted={!m.selectable} className="h-4 w-4 shrink-0" />
            <span className="flex-1 truncate">{m.label}</span>
            {m.id === model && <span className="text-[10px] text-primary">current</span>}
            {!m.selectable && (
              <span className="text-[10px] text-gray-400">
                {m.connected ? 'soon' : 'no key'}
              </span>
            )}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
