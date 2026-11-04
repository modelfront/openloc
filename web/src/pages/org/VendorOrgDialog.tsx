import { Button } from '@/components/ui/button'
import { chipStripeClass, chipSurfaceClass } from '@/components/ui/chip'
import { MEMBER_STATE_PRESETS } from '@/components/ui/chip-presets'
import { cn } from '@/lib/utils'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { enableVendorAdmin, type VendorOrg } from '../../api'
import { EnableAccessButton } from './EnableAccessButton'

/**
 * A vendor org, opened from its row in the Vendors list: its admins, and
 * Enable on each admin the vendor disabled for disuse. The client may enable
 * them whatever the vendor's other admins do. Owner/admin.
 */
export function VendorOrgDialog({ org, onClose }: { org: VendorOrg; onClose: () => void }) {
  const disabled = org.admins.filter(a => a.disabled).length
  const disabledColor = MEMBER_STATE_PRESETS.disabled.color
  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-lg" data-testid="vendor-org-dialog">
        <DialogHeader>
          <DialogTitle>{org.name}</DialogTitle>
          <DialogDescription>Vendor since {new Date(org.created_at).toLocaleDateString()}</DialogDescription>
        </DialogHeader>

        <section aria-labelledby="vendor-admins-heading">
          <h3 id="vendor-admins-heading" className="text-sm font-medium text-gray-900 dark:text-gray-100">Admins</h3>
          {disabled > 0 && (
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              An admin who does not sign in for 90 days is disabled.
            </p>
          )}
          {org.admins.length === 0 ? (
            <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">No active admin.</p>
          ) : (
            <ul className="mt-2 divide-y divide-gray-100 overflow-hidden rounded-md border border-gray-200 dark:divide-gray-800 dark:border-gray-800">
              {org.admins.map(a => (
                <li
                  key={a.user_id}
                  className={cn('relative flex min-h-11 items-center gap-2 px-3 py-1.5', a.disabled && chipSurfaceClass(disabledColor))}
                  data-testid={`vendor-dialog-admin-${a.user_id}`}
                  data-disabled={a.disabled || undefined}
                >
                  {/* A disabled admin: the Disabled chip's colour as a tint and an edge stripe. */}
                  {a.disabled && (
                    <span
                      aria-hidden="true"
                      className={cn('pointer-events-none absolute inset-y-0 left-0 w-0.5 before:absolute before:inset-0', chipStripeClass(disabledColor))}
                    />
                  )}
                  <span className="min-w-0 flex-1 truncate text-sm text-gray-700 dark:text-gray-300" title={a.email}>{a.email}</span>
                  {/* Enable alone marks a disabled admin; the line above says why. */}
                  {a.disabled && <EnableAccessButton email={a.email} enable={() => enableVendorAdmin(org.id, a.user_id)} />}
                </li>
              ))}
            </ul>
          )}
        </section>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
