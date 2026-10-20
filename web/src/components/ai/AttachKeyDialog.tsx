import { useEffect, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { attachKeyToEngines, listKeylessEngines, type AIProviderId, type AIVendorToken, type KeyEngine } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'

const engineKey = (e: KeyEngine) => `${e.kind}:${e.id}`

/**
 * Offers a just-added key to the vendor's engines that have none. Opens only
 * when such engines exist; engines the key cannot run are listed but cannot be
 * ticked. Owner/admin, like adding the key.
 */
export function AttachKeyDialog({ provider, token, onClose }: {
  provider: AIProviderId
  token: AIVendorToken
  onClose: () => void
}) {
  const { data: engines, isSuccess } = useQuery({
    queryKey: queryKeys.aiVendors.keylessEngines(provider, token.id),
    queryFn: () => listKeylessEngines(provider, token.id),
  })
  const fitting = () => new Set(engines?.filter(e => e.fits).map(engineKey))
  const [picked, setPicked] = useState<Set<string>>(fitting)
  useOnChange([engines], () => { if (engines) setPicked(fitting()) })
  useEffect(() => {
    if (isSuccess && engines.length === 0) onClose()
  }, [isSuccess, engines, onClose])

  const attachMut = useMutationWithToast({
    mutationFn: () => {
      const chosen = (engines ?? []).filter(e => picked.has(engineKey(e)))
      return attachKeyToEngines(provider, token.id, {
        mt_engine_ids: chosen.filter(e => e.kind === 'mt').map(e => e.id),
        ai_engine_ids: chosen.filter(e => e.kind === 'ai').map(e => e.id),
      })
    },
    successMessage: r => (r.attached === 1 ? 'Key added to 1 engine' : `Key added to ${r.attached} engines`),
    errorMessage: 'Key not added to engines',
    invalidateKeys: [queryKeys.mtEngines.all, queryKeys.aiEngines.all, queryKeys.aiVendors.all],
    onSuccess: onClose,
  })

  if (!engines?.length) return null
  const toggle = (e: KeyEngine, on: boolean) =>
    setPicked(prev => {
      const next = new Set(prev)
      if (on) next.add(engineKey(e))
      else next.delete(engineKey(e))
      return next
    })

  return (
    <Dialog open onOpenChange={open => !open && onClose()} keepOpen={attachMut.isPending}>
      <DialogContent className="sm:max-w-md" data-testid="attach-key-dialog">
        <DialogHeader>
          <DialogTitle>
            {engines.length === 1 ? '1 engine has no API key' : `${engines.length} engines have no API key`}
          </DialogTitle>
          <DialogDescription>Use the key "{token.name}" for the engines you select.</DialogDescription>
        </DialogHeader>
        <div className="space-y-2 py-2">
          {engines.map(e => (
            <CheckboxField
              key={engineKey(e)}
              checked={picked.has(engineKey(e))}
              onChange={on => toggle(e, on)}
              disabled={!e.fits}
              label={e.name}
              description={e.fits ? undefined : e.reason}
              data-testid={`attach-key-engine-${engineKey(e)}`}
            />
          ))}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={attachMut.isPending}>Skip</Button>
          <Button
            onClick={() => attachMut.mutate()}
            disabled={picked.size === 0}
            disabledReason="Select at least one engine"
            loading={attachMut.isPending}
          >
            Use this key
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
