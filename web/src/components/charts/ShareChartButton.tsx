// Share one chart, as an icon in the chart's action row.
//
// Whoever may change the chart mints a capability token: the link opens for ANY
// signed-in colleague — including a linguist, who cannot browse dashboards at
// all. That is the only way to hand out a chart from a private dashboard.
// Because the link is a standing grant, the dialog is where revoking lives too.
//
// Everyone else gets the same icon, and it copies this page's URL: it opens
// only for people who can already reach the chart, and creates nothing.
import { useState } from 'react'
import { useLocation } from 'react-router-dom'
import { Share2 } from 'lucide-react'
import { revokeChartShare } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import { Button } from '@/components/ui/button'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

export function ShareChartButton({ chartId, onShare, search, canMint, className }: {
  chartId: number
  onShare: (chartId: number) => Promise<string>
  /** The current selection, serialized. The link reproduces what the sender is
   *  looking at — a bare token would open at the defaults instead. */
  search?: string
  /** May create a share link: the chart's write gate. Otherwise the icon copies
   *  the page URL. */
  canMint: boolean
  className?: string
}) {
  if (!canMint) return <CopyChartLink search={search} className={className} />
  return <MintShareLink chartId={chartId} onShare={onShare} search={search} className={className} />
}

/** The share icon that only copies this page's URL, with the selection. */
export function CopyChartLink({ search, className }: { search?: string; className?: string }) {
  const { pathname } = useLocation()
  return (
    <CopyLinkButton
      iconOnly
      icon={Share2}
      url={`${window.location.origin}${pathname}${search ? `?${search}` : ''}`}
      label="Copy link to this chart"
      className={className}
      data-testid="chart-share"
    />
  )
}

function MintShareLink({ chartId, onShare, search, className }: {
  chartId: number
  onShare: (chartId: number) => Promise<string>
  search?: string
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState<string | null>(null)

  const shareMut = useMutationWithToast({
    mutationFn: () => onShare(chartId),
    successMessage: 'Share link created',
    errorMessage: 'Failed to create a share link',
    onSuccess: setToken,
  })
  const revokeMut = useMutationWithToast({
    mutationFn: () => revokeChartShare(chartId),
    successMessage: 'Share link revoked',
    errorMessage: 'Failed to revoke the share link',
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: () => { setToken(null); setOpen(false) },
  })

  const url = token ? `${window.location.origin}/shared/charts/${token}${search ? `?${search}` : ''}` : ''

  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className={className}
        title="Share…"
        aria-label="Share this chart"
        data-testid="chart-share"
        onClick={() => { setOpen(true); shareMut.mutate() }}
      >
        <Share2 className="h-4 w-4" />
      </Button>

      <Dialog open={open} onOpenChange={setOpen} keepOpen={revokeMut.isPending}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Share this chart</DialogTitle>
            <DialogDescription>
              Anyone signed in to this organization can open this link, even if they cannot
              normally see the dashboard it lives on. The link works until you revoke it.
            </DialogDescription>
          </DialogHeader>

          {token ? (
            <p
              className="break-all rounded border border-gray-200 bg-gray-50 p-2 font-mono text-xs dark:border-gray-800 dark:bg-gray-900"
              data-testid="share-link"
            >
              {url}
            </p>
          ) : (
            <p className="text-sm text-gray-500">Creating a link…</p>
          )}

          <DialogFooter>
            {/* Revoking is offered beside the link, not hidden elsewhere: the
                moment a link exists, taking it back is the next thing someone
                needs. It also kills any PREVIOUS link for this chart. */}
            <Button
              variant="outline"
              onClick={() => revokeMut.mutate()}
              loading={revokeMut.isPending}
              data-testid="chart-unshare"
            >
              Stop sharing
            </Button>
            {token && <CopyLinkButton url={url} toastMessage="Share link copied" data-testid="share-copy" />}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
