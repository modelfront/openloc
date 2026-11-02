// Connected apps: OAuth grants the CALLER has approved —
// Claude, ChatGPT, or any MCP client that went through /oauth/authorize.
// Disconnecting revokes the whole token family server-side, killing the
// app's access immediately (not at token expiry). Rendered inside Connect an
// AI tool, so the apps are listed where they are added.
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listOAuthConnections, revokeOAuthConnection } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { DeleteButton } from '@/components/ui/delete-button'
import ConfirmDialog from '@/components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'

export function ConnectedAppsList() {
  const { data: connections, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.oauthConnections.list,
    queryFn: listOAuthConnections,
  })
  // The grant pending disconnect confirmation (themed dialog, not
  // native window.confirm).
  const [pending, setPending] = useState<{ familyID: string; name: string } | null>(null)
  const revokeMut = useMutationWithToast({
    mutationFn: (familyID: string) => revokeOAuthConnection(familyID),
    successMessage: 'App disconnected',
    errorMessage: 'Failed to disconnect app',
    invalidateKeys: [queryKeys.oauthConnections.list],
  })

  return (
    <div data-testid="connected-apps">
      <h3 className="text-xs font-medium text-gray-500 dark:text-gray-400">Connected apps</h3>
      {isLoading ? (
        <LoadingPlaceholder size="sm" />
      ) : isError && !connections ? (
        <LoadFailed what="connected apps" error={error} onRetry={() => refetch()} />
      ) : !connections?.length ? (
        <p className="py-3 text-sm text-gray-400" data-testid="connected-apps-empty">
          No apps connected yet. Once you approve a tool in OpenLoc, it appears here.
        </p>
      ) : (
        <ul className="divide-y divide-gray-200 dark:divide-gray-800">
          {connections.map(c => (
            <li key={c.family_id} className="flex items-center justify-between py-3" data-testid="connected-app-row">
              <div>
                <p className="text-sm font-medium">{c.client_name}</p>
                <p className="text-xs text-gray-400">
                  {c.scope === 'write' ? 'Read and write' : 'Read-only'} · connected{' '}
                  {new Date(c.created_at).toLocaleDateString()}
                  {c.last_used_at && <> · last used {new Date(c.last_used_at).toLocaleDateString()}</>}
                </p>
              </div>
              <DeleteButton
                aria-label={`Disconnect ${c.client_name}`}
                onClick={() => setPending({ familyID: c.family_id, name: c.client_name })}
              />
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={open => { if (!open) setPending(null) }}
        title={`Disconnect ${pending?.name ?? 'this app'}?`}
        description="Its access to this organization stops immediately. You can reconnect from the app at any time."
        actionLabel="Disconnect"
        variant="destructive"
        onConfirm={() => {
          if (pending) revokeMut.mutate(pending.familyID)
          setPending(null)
        }}
      />
    </div>
  )
}
