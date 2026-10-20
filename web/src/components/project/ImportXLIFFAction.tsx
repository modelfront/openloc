import { useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import type { Job } from '@/api'
import { reimportXLIFF } from '@/api/files'
import ConfirmDialog from '@/components/ConfirmDialog'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { invalidateJobContext } from '@/lib/query-invalidation'
import { queryKeys } from '@/lib/query-keys'
import { warnHeldSkipped } from '@/lib/segment-bulk'

const XLIFF_ACCEPT = '.xliff,.xlf,.sdlxliff,.mxliff,.mqxliff'

const isXLIFFFilename = (name: string) =>
  XLIFF_ACCEPT.split(',').some(ext => name.toLowerCase().endsWith(ext))

type ImportJob = Pick<Job, 'id' | 'file_id' | 'target_language'>

interface Props {
  projectId: number
  /** The task's job. Null when there is no single task to import into. */
  job: ImportJob | null
  /** Renders the trigger: `pick` opens the file picker, `pending` while importing. */
  children: (pick: () => void, pending: boolean) => ReactNode
}

/**
 * Import of a task's bilingual XLIFF: the trigger opens the file picker and
 * the picked file is applied at once. A file that would overwrite approved
 * segments imports nothing until the user confirms the overwrite.
 */
export function ImportXLIFFAction({ projectId, job, children }: Props) {
  const queryClient = useQueryClient()
  // The file input, held in state by a callback ref: the opener handed to
  // `children` (called during render) then reads no ref.
  const [input, setInput] = useState<HTMLInputElement | null>(null)
  const [needsForce, setNeedsForce] = useState<{ file: File; approved: number } | null>(null)

  const importMut = useMutationWithToast({
    mutationFn: async ({ target, file, force }: { target: ImportJob; file: File; force: boolean }) => ({
      file,
      result: await reimportXLIFF(target.file_id, file, { lang: target.target_language, force }),
    }),
    successMessage: ({ result }) =>
      result.applied
        ? `Import applied: ${result.updated.toLocaleString()} segment${result.updated === 1 ? '' : 's'} updated`
        : null,
    errorMessage: 'Import failed',
    onSuccess: ({ file, result }) => {
      if (!result.applied) {
        setNeedsForce({ file, approved: result.skipped_approved })
        return
      }
      warnHeldSkipped(result.skipped_held)
      if (!job) return
      invalidateJobContext(queryClient, projectId, job.id)
      queryClient.invalidateQueries({ queryKey: queryKeys.segments.byFile(job.file_id) })
    },
  })

  const onPick = (file: File | undefined) => {
    if (!file || !job) return
    if (!isXLIFFFilename(file.name)) {
      toast.error('Pick an .xliff, .xlf, .sdlxliff, .mxliff or .mqxliff file')
      return
    }
    importMut.mutate({ target: job, file, force: false })
  }

  const approved = needsForce?.approved ?? 0
  return (
    <>
      {children(() => input?.click(), importMut.isPending)}
      <input
        ref={setInput}
        type="file"
        accept={XLIFF_ACCEPT}
        className="hidden"
        data-testid="import-xliff-input"
        onChange={e => {
          onPick(e.target.files?.[0])
          // Lets the same file be picked again.
          e.target.value = ''
        }}
      />
      <ConfirmDialog
        open={needsForce !== null}
        onOpenChange={open => { if (!open) setNeedsForce(null) }}
        title="Overwrite approved segments?"
        description={`The file changes ${approved.toLocaleString()} approved segment${approved === 1 ? '' : 's'}, so nothing was imported. Overwrite ${approved === 1 ? 'it' : 'them'} and import the file? Locked segments are never overwritten.`}
        actionLabel="Overwrite"
        onConfirm={() => {
          if (needsForce && job) importMut.mutate({ target: job, file: needsForce.file, force: true })
          setNeedsForce(null)
        }}
      />
    </>
  )
}
