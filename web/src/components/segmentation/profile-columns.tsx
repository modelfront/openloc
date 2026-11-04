import type { Table } from '@tanstack/react-table'
import { Copy, Download } from 'lucide-react'
import type { ColumnDef } from '@/components/ui/data-table'
import type { SegmentationProfile } from '@/api'
import { EditableTitle } from '@/components/list/EditableTitle'
import { RowAction, RowDeleteAction } from '@/components/list/RowAction'
import { LanguageCode } from '@/components/ui/language-code'
import { modeLabel, profileLabel } from '@/lib/segmentation'

export interface ProfileTableMeta {
  canWrite: boolean
  busy: boolean
  onCopy: (profile: SegmentationProfile) => void
  onExport: (profile: SegmentationProfile) => void
  onDelete: (profile: SegmentationProfile) => void
}

const metaOf = (table: Table<SegmentationProfile>) => table.options.meta as unknown as ProfileTableMeta

export const PROFILE_COLUMNS: ColumnDef<SegmentationProfile>[] = [
  {
    id: 'name',
    header: 'Name',
    cell: ({ row: { original: s } }) => (
      <div className="flex min-w-0 flex-col">
        <EditableTitle name={profileLabel(s)} canEdit={false} className="flex font-medium" />
        {s.description && <span className="truncate text-xs text-muted-foreground">{s.description}</span>}
      </div>
    ),
  },
  {
    id: 'language',
    header: 'Languages',
    size: 160,
    cell: ({ row: { original: s } }) => s.source_languages.length === 0
      ? <span className="text-muted-foreground">Any</span>
      : <span className="flex flex-wrap gap-1">{s.source_languages.map(l => <LanguageCode key={l} code={l} />)}</span>,
  },
  {
    id: 'mode',
    header: 'Mode',
    size: 110,
    cell: ({ row: { original: s } }) => modeLabel(s.definition.mode),
  },
  {
    id: 'actions',
    header: '',
    size: 120,
    cell: ({ row: { original: s }, table }) => {
      const m = metaOf(table)
      return (
        <div className="flex justify-end gap-1">
          {m.canWrite && (
            <RowAction label="Copy" onClick={() => m.onCopy(s)} testId={`segmentation-copy-${s.id}`}>
              <Copy />
            </RowAction>
          )}
          <RowAction label="Export SRX" onClick={() => m.onExport(s)} testId={`segmentation-export-${s.id}`}>
            <Download />
          </RowAction>
          {m.canWrite && !s.builtin && (
            <RowDeleteAction label={`Delete ${s.name}`} disabled={m.busy} testId={`segmentation-delete-${s.id}`}
              onClick={() => m.onDelete(s)} />
          )}
        </div>
      )
    },
  },
]
