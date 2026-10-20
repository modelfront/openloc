// A stored workbook's Excel columns, from its row in the New project wizard's
// file list. Only drafts change them: the file is segmented again. Once the
// project is published its segments sit under jobs, so the file is uploaded
// again instead.

import { useQuery } from '@tanstack/react-query'

import { getXLSXLayout, setXLSXLayout, type StoredXLSXLayout, type XLSXLayout } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { QueryBoundary } from '@/components/layout/QueryBoundary'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { XLSXLayoutDialog } from './XLSXLayoutDialog'

interface Props {
  projectId: number
  fileId: number
  fileName: string
  onClose: () => void
}

export function EditXLSXColumnsDialog({ projectId, fileId, fileName, onClose }: Props) {
  const query = useQuery({
    queryKey: queryKeys.files.xlsxLayout(projectId, fileId),
    queryFn: () => getXLSXLayout(projectId, fileId),
  })
  const save = useMutationWithToast({
    mutationFn: (layout: XLSXLayout) => setXLSXLayout(projectId, fileId, layout),
    successMessage: 'Excel columns saved',
    errorMessage: 'Failed to save the Excel columns',
    // The file is segmented again: its segment count changes with it.
    invalidateKeys: [queryKeys.files.byProject(projectId)],
    onSuccess: onClose,
  })

  // A file stored without a layout is read cell by cell: every text cell of
  // every sheet, row 1 included. It opens as that, not as the suggestion.
  const initial = (data: StoredXLSXLayout): XLSXLayout | undefined => {
    if (data.layout) return data.layout
    const suggested = data.inspection.suggested_layout
    return suggested && { sheets: suggested.sheets.map(({ name }) => ({ name, header_row: false })) }
  }

  // The form starts from the data it opens on, so it waits for a read in
  // flight: right after a save the cache still holds the layout before it.
  const fresh = query.isFetching ? undefined : query.data
  const start = fresh && initial(fresh)
  if (fresh && start) {
    return (
      <XLSXLayoutDialog
        open
        onCancel={onClose}
        fileName={fileName}
        inspection={fresh.inspection}
        initial={start}
        pending={save.isPending}
        submitLabel="Save"
        onSubmit={layout => save.mutate(layout)}
      />
    )
  }
  return (
    <Dialog open onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-md" aria-describedby={undefined} data-testid="xlsx-layout-dialog">
        <DialogHeader>
          <DialogTitle>Excel columns</DialogTitle>
        </DialogHeader>
        {query.isFetching ? (
          <LoadingPlaceholder />
        ) : (
          <QueryBoundary query={query} what="Excel columns">
            {data => (
              <Alert tone="warning">
                {data.inspection.bilingual
                  ? `${fileName} has language codes in row 1, so each language goes to the column under its code.`
                  : `${fileName} has no text to choose columns from.`}
              </Alert>
            )}
          </QueryBoundary>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
