import { useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listAIVendors, createAIConnection, updateAIConnection, listMTEngineCatalog,
  type AIProviderId, type AIConnection, type AIConnectionInput, type AIVendorExtension, type AIVendorStatus,
} from '../../api'
import { toast } from 'sonner'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { ProviderTile, ProviderTileRow } from './ProviderTile'
import {
  VENDOR_GROUPS, vendorsIn, wiredProviderIds, vendorName, vendorLabel, allowsCustomEndpoint, isBuiltIn,
  extensionSetting, type VendorGroup,
} from './vendor-catalog'
import { ConnectionKeys, type TokenEntry } from './ConnectionKeys'
import { ConnectionForm, BuiltInVendorNote } from './ConnectionForm'
import { AddCustomProvider, CustomProviderDetails } from './CustomProvider'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { loadFailure } from '@/lib/load-failure'
import { SectionInfo } from '@/components/ui/section-info'
import { cn } from '@/lib/utils'

// Providers + AI connections.
//
// A grid of provider tiles; clicking one expands its endpoint form AND its keys
// together, because they are one decision — where we call, and what
// authenticates there. Keys sit next to the endpoint they authenticate, so no
// key row has to re-state which connection it belongs to.
//
// `canWrite` (owner/admin) edits endpoints and keys and adds, replaces or
// removes custom providers; without it the grid is read-only.
export function AIVendorsSection({ defaultOpen = false, canWrite }: { defaultOpen?: boolean; canWrite: boolean }) {
  const vendorsQuery = useQuery({ queryKey: queryKeys.aiVendors.all, queryFn: listAIVendors })
  const { data: vendors } = vendorsQuery
  // Built-in providers have no ai_connections row. What proves one is available
  // is the DEPLOYMENT offering it, which the MT engine catalog answers — not
  // whether an engine row happens to exist, since an org may delete its
  // LibreTranslate engine and build another later.
  const catalogQuery = useQuery({ queryKey: queryKeys.mtEngines.catalog, queryFn: listMTEngineCatalog })
  const { data: catalog } = catalogQuery
  // Without the vendors every tile reads as unconfigured, and without the
  // catalog the built-ins drop out of the grid, so the grid waits for both.
  const loading = vendorsQuery.isLoading || catalogQuery.isLoading
  const failure = loadFailure(vendorsQuery, catalogQuery)
  const builtInTypes = new Set((catalog ?? []).map(c => c.engine_type))
  const byId = new Map((vendors ?? []).map(v => [v.provider, v]))
  // Flattened in catalog order so the lists always read the same way, whatever
  // order the API returns providers in. Only WIRED providers can have rows — the
  // catalog's wired providers, then the installed extensions.
  const extensions = (vendors ?? []).filter(v => v.extension)
  const wired = [...wiredProviderIds(), ...extensions.map(v => v.provider)]
  const connections = wired.flatMap(id => byId.get(id)?.connections ?? [])
  const tokens = wired.flatMap(id =>
    (byId.get(id)?.tokens ?? []).map(token => ({ provider: id, token })),
  )

  return (
    <SlimCollapsibleSection
      title="Providers"
      defaultOpen={defaultOpen}
      info={
        <SectionInfo label="About providers">
          Connect OpenLoc to AI providers.
        </SectionInfo>
      }
    >
      {loading ? (
        <LoadingPlaceholder size="sm" />
      ) : failure ? (
        <LoadFailed what="AI vendors" error={failure.error} onRetry={failure.retry} />
      ) : (
        <ConnectionsGrid
          connections={connections} tokens={tokens} builtInTypes={builtInTypes} extensions={extensions} canWrite={canWrite}
        />
      )}
    </SlimCollapsibleSection>
  )
}

// One line under the provider name, and only when there is something real to say.
//
// Still no STATUS text — not "Not configured", not "No key yet" — because the
// dimmed tile already carries that and repeating it on a mostly-unconfigured
// grid is noise. A key count is different: it is a fact you cannot see any
// other way without opening the provider.
//
// The count wins over the endpoint when both apply. The tile is a quarter of a
// row wide, so only one of them fits, and the endpoint is visible in the panel
// the moment you click.
function connectionMeta(c: AIConnection): string | undefined {
  if (c.token_count > 0) return `${c.token_count} ${c.token_count === 1 ? 'key' : 'keys'}`
  if (!c.base_url) return undefined
  // Host only — a full URL truncates to nothing useful at this width.
  return c.base_url.replace(/^https?:\/\//, '').replace(/\/.*$/, '')
}

// What a tile stands for. The provider is always known — it comes from the tile
// you clicked — so the form never has to ask, and a connection can never be
// created against the wrong provider by mistake. `wired: false` providers are on
// the roadmap: they render, but cannot be configured yet.
type TileTarget = {
  provider: AIProviderId
  name: string
  wired: boolean
  connection?: AIConnection
  /** Set for an installed extension's tile. */
  extension?: AIVendorExtension
}

// Every catalogued provider gets a tile whether or not it has been configured, so
// the grid reads as "here is what you can connect to" rather than "here is what
// you already set up". A provider with several connections (two Azure resources,
// say) contributes one tile each.
//
// Custom providers (installed extensions) join the QE+APE row after the
// catalogued providers, named by their manifest.
function vendorTiles(
  group: VendorGroup, connections: AIConnection[], builtInTypes: Set<string>, extensions: AIVendorStatus[],
): TileTarget[] {
  const catalogued = vendorsIn(group).flatMap(v => {
    // A built-in this deployment does not offer is not available here —
    // rendering it would promise a provider nothing backs.
    if (v.builtIn && !builtInTypes.has(v.id)) return []
    const own = connections.filter(c => c.provider === v.id)
    return own.length
      ? own.map(c => ({ provider: v.id as AIProviderId, name: c.name, wired: v.wired, connection: c }))
      : [{ provider: v.id as AIProviderId, name: v.name, wired: v.wired }]
  })
  if (group !== 'qe') return catalogued
  const installed = extensions.flatMap((v): TileTarget[] => {
    const base = { provider: v.provider, name: vendorLabel(v), wired: true, extension: v.extension }
    const own = connections.filter(c => c.provider === v.provider)
    return own.length ? own.map(c => ({ ...base, connection: c })) : [base]
  })
  return [...catalogued, ...installed]
}

// The connections grid — providers in two rows (QE+APE-capable, then MT engines),
// four across. Clicking one opens the editor below its own row rather than
// swapping the tile in place, so the row never reflows mid-edit.
//
// NO headings anywhere in here: not over the grid, not over either row. The
// section is already titled, and the split is conveyed by the gap between the
// two rows.
function ConnectionsGrid({ connections, tokens, builtInTypes, extensions, canWrite }: {
  connections: AIConnection[]
  tokens: TokenEntry[]
  /** Engine types this deployment offers, from the MT engine catalog. */
  builtInTypes: Set<string>
  /** Installed extensions, as vendors. */
  extensions: AIVendorStatus[]
  canWrite: boolean
}) {
  // Identity, not a snapshot: `connectionId` is null for a provider that has no
  // connection yet. The row itself is looked up from `connections` on every
  // render, so a save that refetches is reflected in the open panel instead of
  // leaving it showing the state from before the save.
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<{ provider: AIProviderId; connectionId: number | null } | null>(null)

  const close = () => setEditing(null)
  const createMut = useMutationWithToast({
    mutationFn: ({ provider, input }: { provider: AIProviderId; input: AIConnectionInput }) =>
      createAIConnection(provider, input),
    successMessage: 'Connection added', errorMessage: 'Add failed',
    invalidateKeys: [queryKeys.aiVendors.all],
    // Stay open and adopt the new row, so its keys list appears in place.
    onSuccess: created =>
      setEditing(prev => (prev ? { ...prev, connectionId: created.id } : prev)),
  })
  const updateMut = useMutationWithToast({
    mutationFn: ({ provider, id, input }: { provider: AIProviderId; id: number; input: AIConnectionInput }) =>
      updateAIConnection(provider, id, input),
    successMessage: 'Connection updated', errorMessage: 'Update failed',
    invalidateKeys: [queryKeys.aiVendors.all],
    // Deliberately does NOT close: enabling or updating is rarely the last
    // thing you do here — the keys are right below and usually the next step.
  })

  const isOpen = (t: TileTarget) =>
    editing?.provider === t.provider && editing.connectionId === (t.connection?.id ?? null)

  // The live row behind the open panel. Falls back to matching on provider while
  // `connectionId` is still null, which is the window between creating a
  // connection and learning its id.
  const activeConnection = editing
    ? connections.find(c =>
        editing.connectionId !== null ? c.id === editing.connectionId : c.provider === editing.provider,
      )
    : undefined

  const openTile = (t: TileTarget) => {
    // Built-in providers are unwired — they have no ai_connections row — but they
    // are not "under development": they already work. They open a panel that
    // simply states there is nothing to configure.
    if (!t.wired && !isBuiltIn(t.provider)) {
      // An unwired provider says so instead of opening a form that cannot save.
      // The toast is the same channel every other outcome on this page uses.
      //
      // Deliberately generic rather than naming the provider: the tile you clicked
      // is still on screen and highlighted, and one fixed string cannot drift out
      // of sync with the catalog.
      toast.info('Under development', {
        description: 'This integration is not connectable yet.',
      })
      return
    }
    // Second click on the provider that is already open closes it — the tile is
    // the toggle, which is why the form needs no Cancel button.
    if (isOpen(t)) {
      close()
      return
    }
    setEditing({ provider: t.provider, connectionId: t.connection?.id ?? null })
  }

  // Escape closes the panel, but only from inside it or from the tile that
  // opened it — pressing Escape while focus sits on some OTHER provider's tile
  // should do nothing, since that tile did not open anything.
  //
  // `defaultPrevented` guards the nested overlays: the connection combobox and
  // the delete dialog both handle Escape themselves and mark it handled, so the
  // first Escape closes the popover and only a second one closes the panel.
  const onPanelKeyDown = (e: ReactKeyboardEvent) => {
    if (e.key !== 'Escape' || e.defaultPrevented) return
    const el = e.target as HTMLElement | null
    const fromPanel = el?.closest('[data-testid="ai-connection-panel"]')
    const fromOpenTile = el?.closest('[aria-pressed="true"]')
    if (!fromPanel && !fromOpenTile) return
    e.stopPropagation()
    close()
  }

  // A connection row for the open provider, created if it does not exist yet.
  //
  // The row is plumbing: it is what a key hangs off (the FK), not something the
  // user asks for. So adding the first key to a provider quietly creates it,
  // rather than making "save the endpoint" a prerequisite step — which is what
  // let the keys list stay on screen from the moment a provider is opened.
  const ensureConnectionId = async (): Promise<number> => {
    if (activeConnection) return activeConnection.id
    // The id we already minted for this panel, before the provider list has
    // caught up. activeConnection is resolved from the FETCHED list, so it
    // stays undefined until a refetch lands — without this second guard every
    // further key creates another empty connection, and the provider grid
    // grows a duplicate tile per attempt.
    if (editing?.connectionId != null) return editing.connectionId
    const created = await createAIConnection(editing!.provider, {})
    setEditing(prev => (prev ? { ...prev, connectionId: created.id } : prev))
    // This path bypasses createMut, so nothing else refreshes the list the
    // panel reads.
    await queryClient.invalidateQueries({ queryKey: queryKeys.aiVendors.all })
    return created.id
  }

  // The expansion: endpoint first, then the keys — both always present, so the
  // panel does not change shape underneath you when the first key lands.
  const editingVendor = editing ? extensions.find(v => v.provider === editing.provider) : undefined
  const editingExtension = editingVendor?.extension
  const installed = new Set(extensions.map(v => v.provider))
  // A newly added custom provider opens straight away, so its key is the next click.
  const openUploaded = (provider: AIProviderId) =>
    setEditing({ provider, connectionId: connections.find(c => c.provider === provider)?.id ?? null })
  const expansion = editing && (
    <div className="space-y-3" data-testid="ai-connection-panel">
      {editingVendor?.extension && (
        <CustomProviderDetails
          vendor={{ ...editingVendor, extension: editingVendor.extension }}
          installed={installed}
          canWrite={canWrite}
          onRemoved={close}
        />
      )}
      {isBuiltIn(editing.provider) ? (
        <BuiltInVendorNote name={vendorName(editing.provider)} />
      ) : (
      <>
      {/* A provider with no endpoint to choose gets a keys-only panel. The
          connection row still exists underneath — ensureConnectionId creates it
          on the first key, exactly as it does for a vendor whose endpoint has
          simply been left blank. */}
      {canWrite && allowsCustomEndpoint(editing.provider, editingExtension) && (
      <ConnectionForm
        key={activeConnection?.id ?? `new-${editing.provider}`}
        provider={editing.provider}
        extension={editingExtension}
        initial={activeConnection}
        onSubmit={input =>
          activeConnection
            ? updateMut.mutate({ provider: editing.provider, id: activeConnection.id, input })
            : createMut.mutate({ provider: editing.provider, input })
        }
        isPending={createMut.isPending || updateMut.isPending}
      />
      )}
      <ConnectionKeys
        provider={editing.provider}
        keyLabel={extensionSetting(editingExtension, 'token')?.label}
        entries={activeConnection ? tokens.filter(t => t.token.connection_id === activeConnection.id) : []}
        ensureConnectionId={ensureConnectionId}
        readOnly={!canWrite}
      />
      </>
      )}
    </div>
  )

  return (
    <div className="space-y-4" data-testid="ai-connections">
      {VENDOR_GROUPS.map((g, i) => {
        const tiles = vendorTiles(g.key, connections, builtInTypes, extensions)
        // The editor renders inside the group it belongs to, so the form never
        // appears under a row that does not contain the tile you clicked.
        const editingHere = editing && tiles.some(t => t.provider === editing.provider)
        return (
          <div
            key={g.key}
            // A rule instead of a heading keeps the two groups reading as two.
            // Between the rows only — never above the first.
            className={cn('space-y-2', i > 0 && 'border-t border-gray-200 pt-4 dark:border-gray-800')}
            data-testid={`ai-connection-group-${g.key}`}
            onKeyDown={onPanelKeyDown}
          >
            <ProviderTileRow>
              {tiles.map(t => (
                <ProviderTile
                  key={t.connection?.id ?? `unconfigured-${t.provider}`}
                  provider={t.provider}
                  name={t.name}
                  // A built-in provider is live on arrival — the deployment
                  // supplies its credential, so it needs no connection row and
                  // says so instead of counting keys nobody entered.
                  meta={isBuiltIn(t.provider) ? 'No key required' : t.connection ? connectionMeta(t.connection) : undefined}
                  // Lit means USABLE, which means it has a key. There is no
                  // separate switch: a connection with no credential cannot be
                  // called, and one with a credential has no reason not to be.
                  // Unconfigured and unwired render dimmed for the same reason.
                  enabled={isBuiltIn(t.provider) || (t.connection?.token_count ?? 0) > 0}
                  selected={isOpen(t)}
                  onClick={() => openTile(t)}
                  data-testid={t.connection ? `ai-connection-tile-${t.connection.id}` : `ai-connection-tile-new-${t.provider}`}
                />
              ))}
              {g.key === 'qe' && canWrite && (
                <AddCustomProvider installed={installed} onAdded={ext => openUploaded(ext.provider)} />
              )}
            </ProviderTileRow>
            {editingHere && expansion}
          </div>
        )
      })}
    </div>
  )
}
