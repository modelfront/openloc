import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import type { AIProviderId, AIConnection, AIConnectionInput, AIVendorExtension } from '../../api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { extensionSetting } from './vendor-catalog'
import { listRowClass } from '@/components/list/list-row'
import { cn } from '@/lib/utils'

// Per-provider hint for the endpoint field. The field is GENERIC — any
// API-compatible endpoint (a cloud resource, a gateway, a proxy, a self-hosted
// model server) — so the copy leads with that and names Azure only as the
// example most people arrive with. The Azure-specific
// handling (path prefix, auth headers) is detected from the hostname and never
// asked of the user.
const BASE_URL_HINT: Record<string, { placeholder: string; help: string }> = {
  openai: {
    placeholder: 'https://api.openai.com',
    help: 'Leave blank for OpenAI. Set it to use any OpenAI-compatible endpoint — an Azure OpenAI resource, a gateway or a proxy. The model you pick must match the name deployed there.',
  },
  claude: {
    placeholder: 'https://api.anthropic.com',
    help: 'Leave blank for Anthropic. Set it to use an Anthropic-compatible endpoint, such as a gateway or proxy.',
  },
  deepl: {
    placeholder: 'https://api.deepl.com',
    // The Free/Pro host is derived from the key (Free keys end in ":fx"), so it
    // is never asked for — saying so stops people pasting the wrong one.
    help: 'Leave blank for DeepL. Free and Pro are detected from the key itself, so only set this for a gateway or proxy.',
  },
  google: {
    placeholder: 'https://translation.googleapis.com',
    help: 'Leave blank for Google Cloud Translation. Set it to use a regional endpoint, a gateway or a proxy.',
  },
}

// The connection editor. ONE field — where the provider is reached — because that
// is the only thing about a connection a user actually chooses: the provider comes
// from the tile, and the name is derived from the provider rather than asked for
// (a free-text label on a fixed list of three is a field that earns nothing).
//
// There is no enable/disable control. A connection is live when it has a key,
// so the switch would be a second source of truth that could only ever
// contradict the first — and "enabled with no key" is not a state that means
// anything. Adding or removing a key IS the on/off action.
//
// What remains is one always-visible Update, which saves the endpoint. Enter in
// the field submits it, since a single-field form with one action should not
// need the mouse.
export function ConnectionForm({ provider, extension, initial, onSubmit, isPending }: {
  /** Comes from the tile that was clicked — never asked for, never editable. */
  provider: AIProviderId
  /** An extension's manifest names the field and may give a default. */
  extension?: AIVendorExtension
  initial?: AIConnection
  onSubmit: (input: AIConnectionInput) => void
  isPending: boolean
}) {
  const schema = z.object({ base_url: z.string() })
  const extSetting = extensionSetting(extension, 'base_url')
  const { register, handleSubmit } = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    // An extension's endpoint starts at its manifest default; a blank one
    // falls back to that default anyway.
    defaultValues: { base_url: initial?.base_url || extSetting?.default || '' },
  })
  const hint = BASE_URL_HINT[provider] ?? { placeholder: extSetting?.default || 'https://…', help: '' }

  // `name` is deliberately absent from the payload. Blank means "keep the
  // stored name" on update and "use the provider's display name" on create, both
  // already handled server-side, so there is one source of truth for it.
  const save = handleSubmit(d => onSubmit({ base_url: d.base_url.trim() }))

  return (
    <form
      onSubmit={save}
      className="space-y-2 pb-1"
      data-testid="ai-connection-form"
    >
      {/* Buttons sit on the same line as the endpoint they act on, so the whole
          control reads as one row rather than a field with a detached footer.
          `items-end` bottom-aligns them with the input, since the field carries
          a label above it and the buttons do not.

          Height comes from `h-input` — the same `--spacing-input` token the text
          field uses — because no button size matches it (xs/sm/default are
          24/28/32px against the input's 30). Pinning to the token means the two
          controls cannot drift apart if that height is ever retuned.

          No Cancel: the way out is Escape, or clicking the open vendor's tile
          again. A button that only ever means "put this back how it was" earns
          less than the space it costs when the toggle is right there. */}
      <div className="flex items-end gap-1.5">
        <FormField label={extSetting?.label || 'Custom endpoint'} className="flex-1">
          <Input {...register('base_url')} placeholder={hint.placeholder} autoComplete="off" data-testid="ai-connection-base-url" />
        </FormField>
        <Button type="submit" size="sm" className="h-input" disabled={isPending} data-testid="ai-connection-save">
          {isPending ? 'Saving…' : 'Update'}
        </Button>
      </div>
      {hint.help && <p className="text-xs text-muted-foreground">{hint.help}</p>}
    </form>
  )
}

// The panel for a built-in provider. No endpoint field, no keys list, no Add
// button — the deployment supplies the credential, so there is nothing here a
// user could change, and offering an empty form would imply otherwise.
export function BuiltInVendorNote({ name }: { name: string }) {
  return (
    <div className="space-y-1" data-testid="ai-builtin-note">
      <span className="block text-xs font-medium uppercase tracking-wide text-muted-foreground">Keys</span>
      <div className={cn(listRowClass, 'cursor-default')}>
        <span className="flex min-w-0 items-center gap-2 truncate">
          <span className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">No key required</span>
          <span className="truncate text-xs text-muted-foreground">
            built in — {name} is configured by this deployment
          </span>
        </span>
      </div>
    </div>
  )
}
