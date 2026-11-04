import type { Table } from '@tanstack/react-table'
import { Copy, Star } from 'lucide-react'
import type { ColumnDef } from '@/components/ui/data-table'
import type { FilterProfile } from '@/api'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Chip } from '@/components/ui/chip'
import { RowAction, RowDeleteAction } from '@/components/list/RowAction'
import { isFilterFormat } from '@/lib/filter-formats'
import { optionsSummary } from '@/lib/filter-options'
import { profileLabel } from '@/lib/segmentation'

export interface FilterProfileTableMeta {
  canWrite: boolean
  busy: boolean
  onCopy: (profile: FilterProfile) => void
  onDelete: (profile: FilterProfile) => void
  /** Makes the profile the org default of its format, or clears it (false). */
  onOrgDefault: (profile: FilterProfile, on: boolean) => void
}

const metaOf = (table: Table<FilterProfile>) => table.options.meta as unknown as FilterProfileTableMeta

export const FILTER_PROFILE_COLUMNS: ColumnDef<FilterProfile>[] = [
  {
    id: 'name',
    header: 'Name',
    cell: ({ row: { original: p } }) => (
      <div className="flex min-w-0 flex-col">
        <div className="flex min-w-0 items-center gap-2">
          <EditableTitle name={profileLabel(p)} canEdit={false} className="flex font-medium" />
          {p.org_default && <Chip data-testid={`filter-profile-org-default-${p.id}`}>Org default</Chip>}
        </div>
        {p.description && <span className="truncate text-xs text-muted-foreground">{p.description}</span>}
      </div>
    ),
  },
  {
    id: 'options',
    header: 'Options',
    cell: ({ row: { original: p } }) => (
      <span className="text-muted-foreground" data-testid={`filter-profile-summary-${p.id}`}>
        {isFilterFormat(p.format) ? optionsSummary(p.format, p.options) : ''}
      </span>
    ),
  },
  {
    id: 'actions',
    header: '',
    size: 136,
    cell: ({ row: { original: p }, table }) => {
      const m = metaOf(table)
      if (!m.canWrite) return null
      return (
        <div className="flex justify-end gap-1">
          {/* Filled on the default: the star sets it, and clicking it again removes it. */}
          {p.org_default ? (
            <RowAction label="Remove org default" disabled={m.busy} onClick={() => m.onOrgDefault(p, false)}
              testId={`filter-profile-unset-default-${p.id}`}>
              <Star className="fill-current" />
            </RowAction>
          ) : (
            <RowAction label="Set as org default" disabled={m.busy} onClick={() => m.onOrgDefault(p, true)}
              testId={`filter-profile-set-default-${p.id}`}>
              <Star />
            </RowAction>
          )}
          <RowAction label="Copy" onClick={() => m.onCopy(p)} testId={`filter-profile-copy-${p.id}`}>
            <Copy />
          </RowAction>
          {/* A built-in keeps the delete's slot, so every row's actions line up. */}
          {p.builtin ? <span className="size-8 shrink-0" aria-hidden="true" /> : (
            <RowDeleteAction label={`Delete ${p.name}`} disabled={m.busy} testId={`filter-profile-delete-${p.id}`}
              onClick={() => m.onDelete(p)} />
          )}
        </div>
      )
    },
  },
]
