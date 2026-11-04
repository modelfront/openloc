import { useState } from 'react'
import { LanguageFilterChip, PagedTable } from '@/components/list'
import type { ColumnDef } from '@/components/ui/data-table'
import { usePagedQuery, type ListPage } from '@/hooks/usePagedQuery'

/** A source filter offering only the languages with a hit, as on the Vendors list. */
export function LanguageFilterChipDemo({ size }: { size?: 'sm' | 'md' }) {
  const [value, setValue] = useState<string | null>(null)
  return (
    <LanguageFilterChip
      size={size}
      label="Source"
      value={value}
      codes={['de', 'en', 'fr']}
      onChange={setValue}
      testId={`debug-language-filter-${size ?? 'md'}`}
    />
  )
}

interface DemoRow { id: number; name: string }

const DEMO_ROWS: DemoRow[] = Array.from({ length: 23 }, (_, i) => ({ id: i + 1, name: `Member ${i + 1}` }))
const DEMO_COLUMNS: ColumnDef<DemoRow>[] = [
  { id: 'name', header: 'Name', cell: ({ row: { original: r } }) => <span className="text-sm">{r.name}</span> },
]

// A server page, made up in the browser.
const demoPage = (rows: DemoRow[], offset: number, limit: number): Promise<ListPage<DemoRow>> =>
  Promise.resolve({ data: rows.slice(offset, offset + limit), total: rows.length })

/** 23 rows in pages of ten, the page number in `?debugpage=`; `empty` shows the empty message. */
export function PagedTableDemo({ empty = false }: { empty?: boolean }) {
  const rows = empty ? [] : DEMO_ROWS
  const paged = usePagedQuery({
    pageKey: empty ? 'debugemptypage' : 'debugpage',
    queryKey: page => ['debug-paged-table', empty, page],
    queryFn: (offset, limit) => demoPage(rows, offset, limit),
  })
  return (
    <div className="w-96 rounded-lg border border-gray-200 dark:border-gray-800">
      <PagedTable
        paged={paged}
        what="members"
        emptyMessage="No members yet."
        columns={DEMO_COLUMNS}
        rowTestId={r => `debug-paged-row-${r.id}`}
        testId={empty ? 'debug-paged-empty-pages' : 'debug-paged-pages'}
      />
    </div>
  )
}
