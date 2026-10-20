import { useRef, useState } from 'react'
import { Columns3 } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteFile, listFiles } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { useUploadQueue } from '../../hooks/useUploadQueue'
import { Button } from '@/components/ui/button'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { DeleteButton } from '@/components/ui/delete-button'
import { SOURCE_FILE_ACCEPT } from '@/lib/source-file-formats'
import type { FileRecord } from '@/api'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { EditXLSXColumnsDialog } from './files/EditXLSXColumnsDialog'
import { UploadLayoutQuestion } from './files/XLSXLayoutDialog'

interface Props {
  /** The draft the files belong to; null until the form has been saved once. */
  draftId: number | null
  /** Resolves the draft id, saving the form first when it never was. */
  ensureDraft: () => Promise<number>
  analyze: boolean
  onAnalyzeChange: (v: boolean) => void
}

/**
 * The New Project wizard's "Files" card. Files go
 * straight into the draft project — stored and segmented, no jobs — so they
 * survive leaving the page; publish creates the jobs. "Run analysis on
 * upload" travels with the publish request instead of the uploads.
 */
export function ProjectCreateFilesCard({ draftId, ensureDraft, analyze, onAnalyzeChange }: Props) {
  const queryClient = useQueryClient()
  const [dragging, setDragging] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const [columnsTarget, setColumnsTarget] = useState<FileRecord | null>(null)

  const { data: filesPage } = useQuery({
    queryKey: queryKeys.files.byProject(draftId ?? 0),
    queryFn: () => listFiles(draftId!),
    enabled: draftId != null,
  })
  const files = filesPage?.data ?? []

  const { progress, uploading, start, layoutQuestion, answerLayout } = useUploadQueue(projectId => {
    queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
  })

  const addFiles = async (incoming: FileList | File[]) => {
    const picked = Array.from(incoming)
    if (picked.length === 0) return
    // A failed save has already shown its toast.
    const id = await ensureDraft().catch(() => null)
    if (id == null) return
    start(id, picked)
  }

  const removeMut = useMutationWithToast({
    mutationFn: (fileId: number) => deleteFile(draftId!, fileId),
    successMessage: 'File removed',
    errorMessage: 'Failed to remove file',
    invalidateKeys: draftId != null ? [queryKeys.files.byProject(draftId)] : [],
  })

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4">
      <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">Files (optional)</h3>
      <div
        onDragEnter={e => { if (Array.from(e.dataTransfer.types).includes('Files')) { e.preventDefault(); setDragging(true) } }}
        onDragOver={e => { if (Array.from(e.dataTransfer.types).includes('Files')) e.preventDefault() }}
        onDragLeave={() => setDragging(false)}
        onDrop={e => { e.preventDefault(); setDragging(false); void addFiles(e.dataTransfer.files) }}
        onClick={() => inputRef.current?.click()}
        className={`flex flex-col items-center justify-center gap-1 border-2 border-dashed rounded-md py-6 cursor-pointer transition-colors ${dragging ? 'border-primary bg-primary/10' : 'border-gray-300 dark:border-gray-700'}`}
        data-testid="create-file-dropzone"
        data-guide="project_create.files"
      >
        <p className="text-sm text-gray-600 dark:text-gray-300">
          {progress
            ? `Uploading ${progress.current}/${progress.total}${progress.pct > 0 ? ` (${progress.pct}%)` : '...'}`
            : 'Drag files here'}
        </p>
        {/* The box is clickable for the mouse, but the picker needs a real
            control to be reachable by keyboard — the hidden input is
            display:none, so it is out of the tab order. stopPropagation keeps
            the enclosing onClick from opening the picker a second time. */}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={uploading}
          onClick={e => { e.stopPropagation(); inputRef.current?.click() }}
          data-testid="create-file-browse"
        >
          Browse files
        </Button>
        <p className="text-xs text-gray-400">DOCX, PPTX, PDF, XLIFF, XLSX, and more</p>
        <input
          ref={inputRef}
          type="file"
          multiple
          accept={SOURCE_FILE_ACCEPT}
          className="hidden"
          onChange={e => { if (e.target.files) void addFiles(e.target.files); e.target.value = '' }}
        />
      </div>

      {files.length > 0 && (
        <TooltipProvider delay={200}>
          <ul className="mt-3 space-y-1" data-testid="create-file-list">
            {files.map(f => (
              <li key={f.id} className="flex items-center justify-between text-sm bg-muted rounded px-2 py-1" data-testid={`create-file-${f.id}`}>
                <span className="truncate">{f.name} <span className="text-xs text-gray-400">({f.segment_count} segments)</span></span>
                <span className="ml-2 flex shrink-0 items-center gap-0.5">
                  {f.format === 'xlsx' && (
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            disabled={uploading}
                            onClick={() => setColumnsTarget(f)}
                            aria-label={`Excel columns of ${f.name}`}
                            data-testid={`create-file-xlsx-columns-${f.id}`}
                          >
                            <Columns3 />
                          </Button>
                        }
                      />
                      <TooltipContent>Excel columns</TooltipContent>
                    </Tooltip>
                  )}
                  <DeleteButton
                    size="icon-xs"
                    type="button"
                    aria-label={`Remove ${f.name}`}
                    disabled={uploading || removeMut.isPending}
                    onClick={() => removeMut.mutate(f.id)}
                  />
                </span>
              </li>
            ))}
          </ul>
        </TooltipProvider>
      )}

      <UploadLayoutQuestion question={layoutQuestion} onAnswer={answerLayout} />
      {columnsTarget && draftId != null && (
        <EditXLSXColumnsDialog
          projectId={draftId}
          fileId={columnsTarget.id}
          fileName={columnsTarget.name}
          onClose={() => setColumnsTarget(null)}
        />
      )}

      <CheckboxField
        checked={analyze}
        onChange={onAnalyzeChange}
        label="Run analysis on upload"
        className="mt-3"
        data-testid="create-analyze-checkbox"
      />
    </div>
  )
}
