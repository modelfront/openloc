import { useCallback, useEffect, useState } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import {
  createVendorToken, updateVendorToken, deleteVendorToken, listEnginesUsingKey,
  type AIProviderId, type AIVendorToken, type VendorTokenInput, type KeyEngine,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../ConfirmDialog'
import { AttachKeyDialog } from './AttachKeyDialog'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { ProviderIcon } from './ProviderIcon'
import { drivesChat, isExtensionProvider } from './vendor-catalog'
import { listRowClass } from '@/components/list/list-row'
import { rowButtonProps } from '@/components/list/row-button'
import { cn } from '@/lib/utils'
import { serverFieldError } from '@/lib/server-field-error'

export type TokenEntry = { provider: AIProviderId; token: AIVendorToken }

// The keys for ONE connection. Sits under that connection's endpoint form, so a
// row does not have to say which connection it belongs to — the panel it is in
// already answers that. `readOnly` lists the keys without add, edit or remove.
export function ConnectionKeys({ provider, keyLabel, entries, ensureConnectionId, readOnly = false }: {
  provider: AIProviderId
  /** The key field's label, when the provider names it (an extension's manifest). */
  keyLabel?: string
  entries: TokenEntry[]
  /** Resolves the connection to attach to, creating the row if needed. */
  ensureConnectionId: () => Promise<number>
  readOnly?: boolean
}) {
  const [showCreate, setShowCreate] = useState(false)
  const [editingId, setEditingId] = useState<number | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<TokenEntry | null>(null)
  const [attachFor, setAttachFor] = useState<AIVendorToken | null>(null)
  const closeAttach = useCallback(() => setAttachFor(null), [])
  const { data: enginesUsing } = useQuery({
    queryKey: queryKeys.aiVendors.enginesUsing(deleteTarget?.provider ?? '', deleteTarget?.token.id ?? 0),
    queryFn: () => listEnginesUsingKey(deleteTarget!.provider, deleteTarget!.token.id),
    enabled: !!deleteTarget,
  })

  const createMut = useMutationWithToast({
    // Two calls, deliberately: a key needs a connection to hang off, and for a
    // provider being set up for the first time that row does not exist yet. If the
    // key is rejected the empty connection survives — harmless, since a
    // connection with no key is invisible and reusable.
    mutationFn: async (input: VendorTokenInput) => {
      const connectionId = await ensureConnectionId()
      return createVendorToken(provider, { ...input, connection_id: connectionId })
    },
    // The form shows the error under the field it belongs to.
    successMessage: 'Key added', errorToast: false,
    invalidateKeys: [queryKeys.aiVendors.all],
    onSuccess: token => { setShowCreate(false); setAttachFor(token) },
  })
  const updateMut = useMutationWithToast({
    mutationFn: ({ provider, id, input }: { provider: AIProviderId; id: number; input: VendorTokenInput }) =>
      updateVendorToken(provider, id, input),
    successMessage: 'Key updated', errorToast: false,
    invalidateKeys: [queryKeys.aiVendors.all], onSuccess: () => setEditingId(null),
  })
  const deleteMut = useMutationWithToast({
    mutationFn: ({ provider, id }: { provider: AIProviderId; id: number }) => deleteVendorToken(provider, id),
    successMessage: 'Key removed', errorMessage: 'Delete failed',
    invalidateKeys: [queryKeys.aiVendors.all], onSuccess: () => setDeleteTarget(null),
  })

  return (
    <div className="space-y-1" data-testid="ai-tokens">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Keys
        </span>
        {!showCreate && !readOnly && (
          <Button variant="outline" size="xs" onClick={() => { createMut.reset(); setShowCreate(true) }} data-testid="ai-token-add">
            <Plus /> Add key
          </Button>
        )}
      </div>

      {showCreate && (
        <TokenForm
          provider={provider}
          keyLabel={keyLabel}
          onSubmit={input => createMut.mutate(input)}
          onCancel={() => setShowCreate(false)}
          isPending={createMut.isPending}
          serverError={createMut.error}
        />
      )}

      {entries.length === 0 && !showCreate && (
        <p className="py-1 text-sm text-muted-foreground">No API keys yet.</p>
      )}

      {entries.map(({ provider, token }) =>
        editingId === token.id ? (
          <TokenForm
            key={token.id}
            provider={provider}
            keyLabel={keyLabel}
            initial={token}
            onSubmit={input => updateMut.mutate({ provider, id: token.id, input })}
            onCancel={() => setEditingId(null)}
            isPending={updateMut.isPending}
            serverError={updateMut.error}
          />
        ) : readOnly ? (
          <div key={token.id} className={cn(listRowClass, 'cursor-default')} data-testid={`ai-token-row-${token.id}`}>
            <TokenSummary provider={provider} token={token} />
          </div>
        ) : (
          <div
            key={token.id}
            className={cn(listRowClass, 'cursor-pointer transition-colors hover:bg-muted')}
            {...rowButtonProps(() => { updateMut.reset(); setEditingId(token.id) })}
            data-testid={`ai-token-row-${token.id}`}
          >
            <TokenSummary provider={provider} token={token} />
            <DeleteButton size="icon-xs" className="shrink-0"
              aria-label="Remove" title="Remove"
              onClick={e => { e.stopPropagation(); setDeleteTarget({ provider, token }) }}
            />
          </div>
        ),
      )}

      {attachFor && <AttachKeyDialog provider={provider} token={attachFor} onClose={closeAttach} />}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Remove key "${deleteTarget?.token.name}"?`}
        description={deleteKeyDescription(enginesUsing)}
        details={<DeleteKeyEngineList engines={enginesUsing} />}
        onConfirm={() => deleteTarget && deleteMut.mutate({ provider: deleteTarget.provider, id: deleteTarget.token.id })}
      />
    </div>
  )
}

// A key's name, its last four characters, and whether the chat spends it.
function TokenSummary({ provider, token }: TokenEntry) {
  return (
    <span className="flex min-w-0 items-center gap-2 truncate">
      <ProviderIcon provider={provider} className="size-4 shrink-0 rounded text-[9px]" />
      <span className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{token.name}</span>
      {token.key_last4 && (
        <span className="truncate text-xs text-muted-foreground">••••{token.key_last4}</span>
      )}
      {/* Which key the chat spends is otherwise invisible until you open
          the token — and with several keys on one vendor, the answer is
          the whole reason the choice exists. Shown only where chat can
          use it, so an extension row never carries an unexplained tag. */}
      {drivesChat(provider) && token.use_for_chat && (
        <span
          className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide bg-primary/10 text-primary"
          title="The built-in chat uses this key"
          data-testid={`ai-token-chat-badge-${token.id}`}
        >
          Chat
        </span>
      )}
    </span>
  )
}

// The delete confirmation counts the engines that lose the key and lists up
// to three of them; long engine names would bury the count otherwise.
const NAMED_ENGINES = 3
const KEPT = 'Their settings and routes are kept.'

function deleteKeyDescription(engines: KeyEngine[] | undefined): string {
  if (!engines?.length) return `Any engine using this key will stop translating until you point it at another one. ${KEPT}`
  if (engines.length === 1) return `The engine "${engines[0].name}" uses this key and will stop working until it gets another one. Its settings and routes are kept.`
  return `${engines.length} engines use this key and will stop working until they get another one:`
}

function DeleteKeyEngineList({ engines }: { engines: KeyEngine[] | undefined }) {
  if (!engines || engines.length < 2) return null
  const rest = engines.length - NAMED_ENGINES
  return (
    <>
      <ul className="list-disc space-y-0.5 pl-5" data-testid="delete-key-engines">
        {engines.slice(0, NAMED_ENGINES).map(e => <li key={`${e.kind}:${e.id}`}>{e.name}</li>)}
        {rest > 0 && <li>and {rest} more</li>}
      </ul>
      <p className="mt-2">{KEPT}</p>
    </>
  )
}

// The key editor. There is no provider field: a key authenticates ONE
// connection, and the connection already knows its provider — asking again would
// let the two disagree.
function TokenForm({ provider, keyLabel = 'API key', initial, onSubmit, onCancel, isPending, serverError }: {
  /** From the panel — decides whether the chat choice is even meaningful. */
  provider: AIProviderId
  keyLabel?: string
  initial?: AIVendorToken
  onSubmit: (input: VendorTokenInput) => void
  onCancel: () => void
  isPending: boolean
  /** The last save's error, shown under the field the server named. */
  serverError?: Error | null
}) {
  const schema = z.object({ name: z.string(), api_key: z.string(), use_for_chat: z.boolean() })
  const { register, control, handleSubmit, setError, formState: { errors } } = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: initial?.name ?? '',
      api_key: '',
      use_for_chat: initial?.use_for_chat ?? false,
    },
  })
  useEffect(() => {
    const e = serverFieldError(serverError)
    if (!e) return
    if (e.field === 'name' || e.field === 'api_key') setError(e.field, { type: 'server', message: e.message })
    else setError('root.server', { type: 'server', message: e.message })
  }, [serverError, setError])
  return (
    <form
      // Neither the provider NOR the connection is asked for: the panel this
      // form lives in is one connection of one provider, so both are already
      // decided by the tile that was clicked. Re-asking after the choice reads as
      // if the click did not register. The caller supplies the row — creating it first if the
      // provider has none yet.
      onSubmit={handleSubmit(d =>
        onSubmit({
          name: d.name.trim() || 'Default',
          api_key: d.api_key.trim(),
          // Only ever sent as true: a provider with keys always has a chat key,
          // so unchecking is not an action — you move it by checking another.
          ...(d.use_for_chat ? { use_for_chat: true } : {}),
        }),
      )}
      className="rounded-md border border-border bg-card p-2.5 space-y-2"
      data-testid="ai-token-form"
    >
      <div className="flex gap-2">
        {/* The only way to say which key is which — and on an MT vendor it is
            what the MT engine form's key picker lists. A blank name becomes
            "Default" server-side. */}
        <FormField label="Name" className="w-56 shrink-0" error={errors.name?.message}><Input {...register('name')} placeholder="Default" /></FormField>
        <FormField label={initial ? `${keyLabel} (blank keeps current)` : keyLabel} required={!initial} className="flex-1" error={errors.api_key?.message}>
          <Input
            {...register('api_key')}
            type="password"
            placeholder={initial?.key_last4 ? `••••••••${initial.key_last4}` : isExtensionProvider(provider) ? '' : 'sk-…'}
            autoComplete="off"
            data-testid="ai-token-api-key"
          />
        </FormField>
      </div>
      {/* Only where chat can actually use the key. Offering it on an extension
          or an MT vendor would be a control that changes nothing. Already-chosen
          is disabled rather than hidden: unchecking is not an action, since a
          vendor with keys always has a chat key. */}
      {drivesChat(provider) && (
        <Controller control={control} name="use_for_chat" render={({ field }) => (
          <CheckboxField
            checked={field.value}
            onChange={field.onChange}
            disabled={initial?.use_for_chat === true}
            label="Use for chat"
            description={
              initial?.use_for_chat
                ? 'The built-in chat spends this key. Pick another key to move it.'
                : 'The built-in chat spends this key for this provider. Otherwise it uses the oldest.'
            }
            data-testid="ai-token-use-for-chat"
          />
        )} />
      )}
      {errors.root?.server && (
        <p role="alert" className="text-xs text-red-500 dark:text-red-400">{errors.root.server.message}</p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
        <Button type="submit" size="sm" disabled={isPending}>{isPending ? 'Saving…' : initial ? 'Update' : 'Add'}</Button>
      </div>
    </form>
  )
}
