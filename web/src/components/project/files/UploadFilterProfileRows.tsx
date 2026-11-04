// The "Filter profile" picks of an upload: one row per file that has a filter
// format, its name beside a picker of that format's profiles. Shared by the
// project page's upload dialog and the New project wizard's.

import { Combobox } from '@/components/ui/combobox'
import { TooltipProvider } from '@/components/ui/tooltip'
import { EditableTitle } from '@/components/list/EditableTitle'
import { FILTER_FORMAT_LABELS } from '@/lib/filter-formats'
import type { UploadFilterPick } from '@/hooks/useUploadFilterPicks'

export function UploadFilterProfileRows({ picks }: { picks: UploadFilterPick[] }) {
  if (picks.length === 0) return null
  return (
    <div className="space-y-1.5" data-testid="upload-filter-profiles">
      <h3 className="text-sm font-medium">Filter profile</h3>
      {/* A big batch scrolls here, so the dialog's buttons stay on screen. */}
      <TooltipProvider delay={200}>
        <div className="max-h-[30vh] space-y-1.5 overflow-y-auto">
          {picks.map(p => (
            <div key={p.key} className="flex items-center gap-2">
              <div className="flex min-w-0 flex-1 text-sm"><EditableTitle name={p.file.name} canEdit={false} /></div>
              <Combobox
                value={p.value !== undefined ? String(p.value) : ''}
                onChange={v => { if (v) p.setValue(Number(v)) }}
                options={p.profiles.map(o => ({ value: String(o.id), label: o.name }))}
                placeholder="Loading…"
                disabled={p.profiles.length === 0}
                className="w-48 shrink-0"
                aria-label={`${FILTER_FORMAT_LABELS[p.format]} filter profile of ${p.file.name}`}
                data-testid={`upload-filter-${p.file.name}`}
              />
            </div>
          ))}
        </div>
      </TooltipProvider>
    </div>
  )
}
