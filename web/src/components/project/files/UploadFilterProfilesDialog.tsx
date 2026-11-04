// The New project wizard's filter profile question: asked at upload, where no
// other upload dialog opens, when a file's format has profiles of the org's
// own. The project page asks inside its upload dialog. Open while `files` is
// set; Upload → `onConfirm` with the picks other than the form's, Cancel →
// discard the files.
//
// Access: managers, like the upload and the wizard.

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import type { FilterProfile, FilterProfileChoice } from '@/api'
import { useLastNonNull } from '@/hooks/useLastNonNull'
import { useUploadFilterPicks } from '@/hooks/useUploadFilterPicks'
import { filterFormatOfFilename, type FilterFormat } from '@/lib/filter-formats'
import { UploadFilterProfileRows } from './UploadFilterProfileRows'

/** Whether any of `files` has a format with a profile of the org's own: only
 *  then is there a pick the project's settings do not already offer. */
export function asksFilterProfile(files: File[], byFormat: Record<FilterFormat, FilterProfile[]>): boolean {
  return files.some(f => {
    const format = filterFormatOfFilename(f.name)
    return format !== null && byFormat[format].some(p => !p.builtin)
  })
}

interface Props {
  /** The files waiting for their picks; null closes the dialog. */
  files: File[] | null
  /** The form's filter profile per format; a format left out uses its Default. */
  filterChoices: FilterProfileChoice[] | null | undefined
  onCancel: () => void
  onConfirm: (filterProfiles: ReadonlyMap<File, number>) => void
}

export function UploadFilterProfilesDialog({ files: queued, filterChoices, onCancel, onConfirm }: Props) {
  const open = queued !== null
  // Rendered through the close animation, after the parent clears the queue.
  const files = useLastNonNull(queued) ?? []
  const filters = useUploadFilterPicks(files, filterChoices, open)

  return (
    <AlertDialog open={open} onOpenChange={o => { if (!o) onCancel() }}>
      <AlertDialogContent className="data-[size=default]:sm:max-w-lg" data-testid="upload-filter-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Upload {files.length} file{files.length === 1 ? '' : 's'}
          </AlertDialogTitle>
          <AlertDialogDescription>
            Choose how each file is read.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <UploadFilterProfileRows picks={filters.picks} />

        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={() => onConfirm(filters.overrides())} data-testid="upload-filter-confirm">
            Upload
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
