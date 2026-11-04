import { useMemo, useRef, useState } from 'react'
import { Columns3 } from 'lucide-react'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { deleteFile, listFiles } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { toast } from 'sonner'
import { errorText } from '@/lib/error-text'
import { useUploadQueue } from '../../hooks/useUploadQueue'
import { useFilterProfiles } from '../../hooks/useFilterProfiles'
import { Button } from '@/components/ui/button'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { DeleteButton } from '@/components/ui/delete-button'
import { VirtualDataTable } from '@/components/ui/virtual-data-table'
import type { ColumnDef } from '@/components/ui/data-table'
import { SOURCE_FILE_ACCEPT } from '@/lib/source-file-formats'
import type { FileRecord, FilterProfileChoice } from '@/api'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { EditXLSXColumnsDialog } from './files/EditXLSXColumnsDialog'
import { UploadLayoutQuestion } from './files/XLSXLayoutDialog'
import { UploadFilterProfilesDialog, asksFilterProfile } from './files/UploadFilterProfilesDialog'

const FILES_PAGE = 100
const ROW_HEIGHT = 36
const HEADER_HEIGHT = 34
const MAX_VISIBLE_ROWS = 8

interface Props {
  /** The draft the files belong to; null until the form has been saved once. */
  draftId: number | null
  /** Resolves the draft id, saving the form first when it never was. */
  ensureDraft: () => Promise<number>
  analyze: boolean
  onAnalyzeChange: (v: boolean) => void
  /** The form's filter profile per format, preselected for each upload; a
   *  format left out uses its Default. */
  filterChoices?: FilterProfileChoice[] | null
}

/**
 * The New Project wizard's "Files" card. Files go
 * straight into the draft project — stored and segmented, no jobs — so they
 * survive leaving the page; publish creates the jobs. "Run analysis on
 * upload" travels with the publish request instead of the uploads.
 */
export function ProjectCreateFilesCard({ draftId, ensureDraft, analyze, onAnalyzeChange, filterChoices }: Props) {
  const queryClient = useQueryClient()
  const [dragging, setDragging] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const [columnsTarget, setColumnsTarget] = useState<FileRecord | null>(null)
  // Files waiting for their filter profiles, with the draft they go to.
  const [awaitingFilters, setAwaitingFilters] = useState<{ draftId: number; files: File[] } | null>(null)
  const { byFormat } = useFilterProfiles()

  // The server sends the files a page at a time; the list loads the next page
  // as it is scrolled to its end.
  const { data: filePages, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteQuery({
    queryKey: queryKeys.files.pages(draftId ?? 0),
    queryFn: ({ pageParam }) => listFiles(draftId!, { limit: FILES_PAGE, cursor: pageParam }),
    initialPageParam: 0,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
    enabled: draftId != null,
  })
  const files = useMemo(() => filePages?.pages.flatMap(p => p.data) ?? [], [filePages])
  const total = filePages?.pages[0]?.total ?? 0

  const { progress, uploading, start, layoutQuestion, answerLayout } = useUploadQueue(projectId => {
    queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(projectId) })
  })

  const addFiles = async (incoming: FileList | File[]) => {
    const picked = Array.from(incoming)
    if (picked.length === 0) return
    const id = await ensureDraft().catch((err: unknown) => {
      toast.error(`Draft not saved: ${errorText(err, String(err))}`)
      return null
    })
    if (id == null) return
    if (asksFilterProfile(picked, byFormat)) setAwaitingFilters({ draftId: id, files: picked })
    else start(id, picked)
  }

  const removeMut = useMutationWithToast({
    mutationFn: (fileId: number) => deleteFile(draftId!, fileId),
    successMessage: 'File removed',
    errorMessage: 'Failed to remove file',
    invalidateKeys: draftId != null ? [queryKeys.files.byProject(draftId)] : [],
  })

  const { isPending: removePending, mutate: removeFile } = removeMut
  const columns = useMemo<ColumnDef<FileRecord>[]>(() => [
    {
      id: 'name',
      header: 'File',
      cell: ({ row: { original: f } }) => (
        <span className="truncate block" data-testid={`create-file-${f.id}`}>
          {f.name} <span className="text-xs text-gray-400">({f.segment_count} segments)</span>
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      size: 64,
      cell: ({ row: { original: f } }) => (
        <span className="flex items-center justify-end gap-0.5">
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
            disabled={uploading || removePending}
            onClick={() => removeFile(f.id)}
          />
        </span>
      ),
    },
  ], [uploading, removePending, removeFile])

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4">
      <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">{total > 0 ? `Files (${total})` : 'Files (optional)'}</h3>
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
          <div
            className="mt-3 flex flex-col"
            // As tall as its rows, up to MAX_VISIBLE_ROWS; then it scrolls.
            style={{ height: HEADER_HEIGHT + Math.min(files.length, MAX_VISIBLE_ROWS) * ROW_HEIGHT }}
            data-testid="create-file-list"
          >
            <VirtualDataTable
              columns={columns}
              data={files}
              estimateSize={ROW_HEIGHT}
              onLoadMore={hasNextPage ? () => { void fetchNextPage() } : undefined}
              isLoadingMore={isFetchingNextPage}
            />
          </div>
        </TooltipProvider>
      )}

      <UploadFilterProfilesDialog
        files={awaitingFilters?.files ?? null}
        filterChoices={filterChoices}
        onCancel={() => setAwaitingFilters(null)}
        onConfirm={filterProfiles => {
          setAwaitingFilters(null)
          if (awaitingFilters) start(awaitingFilters.draftId, awaitingFilters.files, { filterProfiles })
        }}
      />
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
