// The Files table's columns. Module scope, so they keep their identity across
// renders — what changes (the selection, the role, the file records) arrives
// through the table's `meta` (FilesTableMeta) instead.

import type { Table } from '@tanstack/react-table'
import { SelectAllCheckbox } from '@/components/list/SelectAllCheckbox'

import type { FileSummary } from '@/lib/file-readiness'
import { formatDate } from '@/utils'
import { EditableTitle } from '@/components/list/EditableTitle'
import { DownloadMenu } from '@/components/project/DownloadMenu'
import { CountMeta } from '@/components/ui/count-meta'
import type { ColumnDef, DataTableColumnMeta } from '@/components/ui/data-table'
import { DeleteButton } from '@/components/ui/delete-button'
import { DotSeparator } from '@/components/ui/dot-separator'
import { FileReadiness } from './FileReadiness'

export interface FilesTableMeta {
  projectId: number
  isAdmin: boolean
  selected: ReadonlySet<number>
  visibleIds: number[]
  onToggle: (ids: number[], on: boolean) => void
  onDelete: (file: FileSummary) => void
}

const metaOf = (table: Table<FileSummary>) => table.options.meta as unknown as FilesTableMeta

export const FILE_COLUMNS: ColumnDef<FileSummary>[] = [
  {
    id: 'select',
    size: 36,
    // The whole cell toggles, not just the 14px box.
    meta: {
      onCellClick: (f, table) => {
        const { selected, onToggle } = metaOf(table)
        onToggle([f.fileId], !selected.has(f.fileId))
      },
      onHeaderClick: table => {
        const { selected, visibleIds, onToggle } = metaOf(table)
        onToggle(visibleIds, !(visibleIds.length > 0 && visibleIds.every(id => selected.has(id))))
      },
    } satisfies DataTableColumnMeta<FileSummary>,
    header: ({ table }) => {
      const { selected, visibleIds, onToggle } = metaOf(table)
      const all = visibleIds.length > 0 && visibleIds.every(id => selected.has(id))
      const some = visibleIds.some(id => selected.has(id))
      return (
        <SelectAllCheckbox
          checked={all}
          indeterminate={some}
          onChange={on => onToggle(visibleIds, on)}
          aria-label="Select all files"
          data-testid="select-all-files"
        />
      )
    },
    cell: ({ row, table }) => {
      const { selected, onToggle } = metaOf(table)
      return (
        <input
          type="checkbox"
          checked={selected.has(row.original.fileId)}
          onChange={e => onToggle([row.original.fileId], e.target.checked)}
          className="size-3.5 cursor-pointer accent-primary"
          aria-label={`Select ${row.original.name}`}
          data-testid={`select-file-${row.original.fileId}`}
        />
      )
    },
  },
  {
    id: 'file',
    header: 'File',
    cell: ({ row }) => (
      <div className="flex min-w-0 flex-col">
        <EditableTitle name={row.original.name} canEdit={false} className="text-sm font-medium text-gray-900 dark:text-gray-100" />
        <span className="truncate text-xs text-gray-500 dark:text-gray-400">
          {row.original.format.toUpperCase()}
          <DotSeparator />
          <CountMeta items={[{ count: row.original.segments, noun: 'segment' }, { count: row.original.words, noun: 'word' }]} />
          <DotSeparator />
          {formatDate(row.original.uploadedAt)}
        </span>
      </div>
    ),
  },
  { id: 'readiness', header: 'Status', size: 200, cell: ({ row }) => <FileReadiness file={row.original} /> },
  {
    id: 'actions',
    size: 112,
    cell: ({ row, table }) => {
      const { projectId, isAdmin, onDelete } = metaOf(table)
      const f = row.original
      return (
        // Actions act on the file; they must not also open its tasks.
        <div className="flex items-center justify-end gap-0.5" onClick={e => e.stopPropagation()}>
          <DownloadMenu projectId={projectId} jobs={f.jobs} fileIds={[f.fileId]} variant="ghost" color="muted" hoverColor="muted" iconOnly testidPrefix={`file-download-${f.fileId}`} />
          {isAdmin && (
            <DeleteButton
              size="icon-xs"
              onClick={() => onDelete(f)}
              aria-label={`Delete file ${f.name}`}
              data-testid={`delete-file-${f.fileId}`}
            />
          )}
        </div>
      )
    },
  },
]
