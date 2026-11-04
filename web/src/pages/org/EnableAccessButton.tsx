import { Power } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { queryKeys } from '../../lib/query-keys'

/**
 * Enable for someone a vendor org disabled for disuse: in the member dialog
 * and in a vendor's window. The lists reload; onEnabled updates the window.
 */
export function EnableAccessButton({ email, enable, onEnabled }: {
  email: string
  enable: () => Promise<unknown>
  onEnabled?: () => void
}) {
  const mut = useMutationWithToast({
    mutationFn: enable,
    successMessage: `${email} can use the organization again`,
    errorMessage: 'Failed to enable access',
    invalidateKeys: [queryKeys.members.all, queryKeys.vendorOrgs.all],
    onSuccess: () => onEnabled?.(),
  })
  return (
    <Button
      type="button"
      size="xs"
      variant="outline"
      // Solid in dark mode too: the outline's own fill is see-through, and
      // the button sits on a disabled person's yellow tint.
      className="dark:bg-card dark:hover:bg-muted"
      startIcon={<Power />}
      loading={mut.isPending}
      onClick={() => mut.mutate()}
      aria-label={`Enable ${email}`}
    >
      Enable
    </Button>
  )
}
