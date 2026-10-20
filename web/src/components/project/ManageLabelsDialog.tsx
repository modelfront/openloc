// ManageLabelsDialog — the Labels page's editor, reachable from the New
// project form so a label can be defined without leaving the form.
//
// The list itself is LabelManager, shared with the page.

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { LabelManager } from '../label/LabelManager'

interface Props {
  open: boolean
  onClose: () => void
  /** False hides every editing control — the list stays readable. */
  canEdit?: boolean
  /** Deleting a whole label is owner-only server-side. */
  canDelete?: boolean
}

export function ManageLabelsDialog({ open, onClose, canEdit = true, canDelete = false }: Props) {
  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="sm:max-w-2xl" data-testid="manage-labels-dialog">
        <DialogHeader>
          <DialogTitle>Manage labels</DialogTitle>
        </DialogHeader>

        {/* `enabled` on the query: the New project form mounts this dialog on
            every render, and the labels are only needed once it is open. */}
        <LabelManager canEdit={canEdit} canDelete={canDelete} enabled={open} />

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose} data-testid="manage-labels-close">
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
