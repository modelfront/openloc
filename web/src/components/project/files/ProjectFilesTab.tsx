// Project page → Files tab: the project's source files and how ready each one
// is. Work on a file's languages happens in the Tasks tab — a click on a row
// opens it there, filtered to that file.
//
// Access: managers, like the rest of the project page. Deleting a file of a
// live project is owner/admin (the handler's gate — see internal/file), so
// only admins see Delete; re-import is admin-only too.

import { useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Search } from 'lucide-react'

import { deleteFile, type Job } from '@/api'
import { useBulkJobActions } from '@/hooks/useBulkJobActions'
import { useUrlParamSetters } from '@/hooks/useUrlParamSetters'
import {
  FILE_FILTER_PARAM_KEYS,
  FILE_READINESS,
  FILE_READINESS_DISPLAY,
  fileMatchesFilters,
  parseFileFilters,
  summarizeFiles,
  type FileSummary,
} from '@/lib/file-readiness'
import { SOURCE_FILE_ACCEPT } from '@/lib/source-file-formats'
import ConfirmDialog from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/layout/EmptyState'
import { ClearFiltersButton, FilterChip, FilterOption } from '@/components/list'
import { SelectionBar, SelectionBarDivider } from '@/components/list/SelectionBar'
import { DownloadMenu } from '@/components/project/DownloadMenu'
import { Button } from '@/components/ui/button'
import { DataTable } from '@/components/ui/data-table'
import { FILE_COLUMNS, type FilesTableMeta } from './file-columns'
import { Input } from '@/components/ui/input'

interface Props {
  projectId: number
  jobs: Job[]
  isAdmin: boolean
  /** Completed projects take no new files. */
  uploadLocked: boolean
  uploading: boolean
  /** The button's text — "Upload files", or the queue's progress. */
  uploadLabel: string
  onPickFiles: (files: File[]) => void
}

export function ProjectFilesTab({
  projectId, jobs, isAdmin, uploadLocked, uploading, uploadLabel, onPickFiles,
}: Props) {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const filters = useMemo(() => parseFileFilters(params), [params])
  const inputRef = useRef<HTMLInputElement>(null)
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [deleteTarget, setDeleteTarget] = useState<FileSummary[] | null>(null)
  const { run, runningAction } = useBulkJobActions(projectId)

  // The file rows carry the revision token an XLIFF re-import locks on.

  const files = useMemo(() => summarizeFiles(jobs), [jobs])
  const visible = useMemo(() => files.filter(f => fileMatchesFilters(f, filters)), [files, filters])
  const picked = useMemo(() => visible.filter(f => selected.has(f.fileId)), [visible, selected])

  const { setParam, clearParams } = useUrlParamSetters()
  const toggle = (ids: number[], on: boolean) => setSelected(prev => {
    const next = new Set(prev)
    for (const id of ids) {
      if (on) next.add(id)
      else next.delete(id)
    }
    return next
  })

  const runDelete = (targets: FileSummary[]) =>
    run({
      actionKey: 'delete',
      label: 'Delete',
      unit: 'file',
      items: targets,
      worker: async f => { await deleteFile(projectId, f.fileId) },
    }).then(() => toggle(targets.map(f => f.fileId), false))

  const tableMeta: FilesTableMeta = {
    projectId,
    isAdmin,
    selected,
    visibleIds: visible.map(f => f.fileId),
    onToggle: toggle,
    onDelete: f => setDeleteTarget([f]),
  }

  const activeFilters = Object.keys(filters).length

  return (
    <div className="space-y-4" data-testid="project-files">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Project files</h2>
        <input
          ref={inputRef}
          type="file"
          multiple
          onChange={e => {
            const list = e.target.files
            if (list && list.length > 0) onPickFiles(Array.from(list))
            e.target.value = ''
          }}
          className="hidden"
          accept={SOURCE_FILE_ACCEPT}
        />
        <Button
          onClick={() => inputRef.current?.click()}
          disabled={uploading || uploadLocked}
          title={uploadLocked ? 'Project is completed — reopen it to upload more files' : undefined}
        >
          {uploadLabel}
        </Button>
      </div>

      {files.length > 0 && (
        <div className="flex flex-wrap items-center gap-2" data-testid="file-filter-bar">
          <Input
            type="text"
            value={filters.q ?? ''}
            onChange={e => setParam('q', e.target.value)}
            placeholder="Search files…"
            data-testid="file-filter-search"
            icon={<Search className="h-3.5 w-3.5" />}
            className="h-7 w-60"
            inputClassName="text-xs"
          />
          <FilterChip
            label="Status"
            value={filters.readiness ? FILE_READINESS_DISPLAY[filters.readiness].label : null}
            testId="file-filter-status"
            render={close => (
              <>
                <FilterOption selected={!filters.readiness} onClick={() => { setParam('file_status', null); close() }}>All</FilterOption>
                {FILE_READINESS.map(r => (
                  <FilterOption key={r} selected={filters.readiness === r} onClick={() => { setParam('file_status', r); close() }}>
                    {FILE_READINESS_DISPLAY[r].label}
                  </FilterOption>
                ))}
              </>
            )}
          />
          <ClearFiltersButton count={activeFilters} onClear={() => clearParams(FILE_FILTER_PARAM_KEYS)} testId="file-filter-clear-all" />
        </div>
      )}

      {picked.length > 0 && (
        <SelectionBar
          label={`${picked.length} ${picked.length === 1 ? 'file' : 'files'} selected`}
          onClear={() => setSelected(new Set())}
          busy={runningAction !== null}
          testIdPrefix="file"
        >
          <DownloadMenu
            projectId={projectId}
            jobs={picked.flatMap(f => f.jobs)}
            fileIds={picked.map(f => f.fileId)}
            testidPrefix="file-bulk-download"
          />
          {isAdmin && (
            <>
              <SelectionBarDivider />
              <Button
                variant="destructive"
                size="xs"
                onClick={() => setDeleteTarget(picked)}
                disabled={runningAction !== null}
                data-testid="file-bulk-delete-btn"
              >
                {runningAction === 'delete' ? 'Delete…' : 'Delete'}
              </Button>
            </>
          )}
        </SelectionBar>
      )}

      {visible.length > 0 ? (
        <div className="overflow-hidden rounded-lg border border-gray-200 bg-card dark:border-gray-800">
          <DataTable
            columns={FILE_COLUMNS}
            data={visible}
            meta={tableMeta as unknown as Record<string, unknown>}
            // A file's work lives in the Tasks tab; open it there, just this file.
            onRowClick={f => navigate(`/projects/${projectId}?file=${f.fileId}`)}
            rowClassName="group"
          />
        </div>
      ) : (
        <EmptyState
          message={files.length === 0
            ? 'No files yet. Upload or drag & drop one to start translating.'
            : 'No files match the current filters.'}
        />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={open => { if (!open) setDeleteTarget(null) }}
        title={deleteTarget?.length === 1
          ? `Delete file "${deleteTarget[0].name}"?`
          : `Delete ${deleteTarget?.length ?? 0} files?`}
        description="This also deletes their jobs, segments and translations."
        actionLabel="Delete"
        onConfirm={() => { if (deleteTarget) void runDelete(deleteTarget) }}
      />
    </div>
  )
}
