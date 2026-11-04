import { useId, useState } from 'react'
import type { FilterProfile, SegmentationProfile } from '@/api'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { Combobox } from '@/components/ui/combobox'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { HelpTip } from '@/components/ui/help-tip'
import { profileLabel } from '@/lib/segmentation'
import { FILTER_FORMATS, FILTER_FORMAT_LABELS, filterFormatHint, type FilterFormat } from '@/lib/filter-formats'
import { defaultFilterProfile } from '@/hooks/useFilterProfiles'
import { OpenLinkButton } from '@/components/ui/open-link-button'
import { filterProfilePath, segmentationProfilePath } from '@/lib/asset-links'

/** The filter profile pickers' data: every format's profiles, the stored
 *  picks (a format left out uses its Default) and where a pick goes; null is
 *  the format's Default. */
export interface FilterPicks {
  byFormat: Record<FilterFormat, FilterProfile[]>
  values: Partial<Record<FilterFormat, number>>
  error?: unknown
  onRetry?: () => void
  onChange: (format: FilterFormat, id: number | null) => void
}

/**
 * The File configurations card of the settings editor: the segmentation
 * profile that splits the files uploaded from now on, and the filter profile
 * each file format is read with.
 * `profiles` are the ones that suit the source language; a `value` outside
 * them is the stored profile, listed under `storedLabel` so the field still
 * names it. A failed list shows the error and a Retry instead of the field.
 */
export function FileConfigurationsSection({ profiles, value, storedLabel, missing, disabled, error, onRetry, onChange, filters }: {
  profiles: SegmentationProfile[]
  value: number | null
  storedLabel?: string
  /** Why the field needs a pick, shown under it. */
  missing?: string
  disabled?: boolean
  error?: unknown
  onRetry?: () => void
  onChange: (id: number) => void
  filters: FilterPicks
}) {
  const stored = value != null && !profiles.some(p => p.id === value)
  const options = [
    ...(stored ? [{ value: String(value), label: storedLabel ?? `Profile #${value}` }] : []),
    ...profiles.map(p => ({ value: String(p.id), label: profileLabel(p) })),
  ]
  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="file-configurations-section">
      <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">File configurations</h3>
      {/* One grid, so the segmentation and filter pickers share a left edge. */}
      <div className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2">
        {error ? <div className="col-span-2"><LoadFailed what="segmentation profiles" error={error} onRetry={onRetry} /></div> : (
          <>
            <span className="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300">
              Segmentation profile
              <HelpTip text="Decides how uploaded files are split into segments. The list shows the profiles that suit the source language. A change applies to files uploaded afterwards; files already uploaded keep their segments. Profiles are set up under File configurations → Segmentation profiles." />
            </span>
            <div className="flex items-center gap-1">
              <Combobox
                value={value != null ? String(value) : ''}
                onChange={v => { if (v && Number(v) !== value) onChange(Number(v)) }}
                options={options}
                placeholder="Pick a profile"
                className="w-80"
                disabled={disabled}
                aria-invalid={!!missing}
                data-testid="segmentation-profile-select"
              />
              {value != null && <OpenLinkButton to={segmentationProfilePath(value)} label="Open segmentation profile" data-testid="segmentation-profile-open" />}
            </div>
            {missing && <p className="col-start-2 text-xs text-destructive" role="alert" data-testid="segmentation-profile-missing">{missing}</p>}
          </>
        )}
        <FilterProfilePickers filters={filters} disabled={disabled} />
      </div>
    </div>
  )
}

/** One picker per file format, rows of the card's grid; each shows the
 *  format's Default until another profile is picked. The rows collapse behind
 *  the heading, closed at first: the list grows with every format, and the
 *  heading's summary says which formats have a pick. */
function FilterProfilePickers({ filters, disabled }: { filters: FilterPicks; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const rowsId = useId()
  const picked = FILTER_FORMATS.filter(format => {
    const id = filters.values[format]
    return id != null && id !== defaultFilterProfile(filters.byFormat[format])?.id
  })
  const summary = picked.length === 0 ? 'All Default' : `${picked.map(f => FILTER_FORMAT_LABELS[f]).join(', ')} picked`
  const help = <HelpTip text="Decide how each file format is read: for example whether strings contain HTML, or which Excel columns to translate. A change applies to files uploaded afterwards. A file can also get another profile when it is uploaded. Profiles are set up under File configurations → Filter profiles." />
  // A failed load has no rows and no picks to summarise: a plain heading, then the error.
  if (filters.error) {
    return (
      <>
        <span className="col-span-2 mt-2 flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300" data-testid="filter-profiles-heading">
          Filter profiles
          {help}
        </span>
        <div className="col-span-2"><LoadFailed what="filter profiles" error={filters.error} onRetry={filters.onRetry} /></div>
      </>
    )
  }
  const heading = (
    <span className="col-span-2 mt-2 flex items-center gap-1">
      <DisclosureToggle open={open} onToggle={() => setOpen(o => !o)} controls={rowsId} variant="field"
        className="text-sm" summary={<>{' '}— {summary}</>} data-testid="filter-profiles-toggle">
        Filter profiles
      </DisclosureToggle>
      {help}
    </span>
  )
  return (
    <>
      {heading}
      {open && (
        <div className="contents" id={rowsId} data-testid="filter-profile-pickers">
          {FILTER_FORMATS.map(format => {
            const profiles = filters.byFormat[format]
            const fallback = defaultFilterProfile(profiles)
            const value = filters.values[format] ?? fallback?.id
            return (
              <FilterProfileRow key={format} format={format} profiles={profiles} value={value} defaultId={fallback?.id}
                disabled={disabled} onChange={id => filters.onChange(format, id === fallback?.id ? null : id)} />
            )
          })}
        </div>
      )}
    </>
  )
}

function FilterProfileRow({ format, profiles, value, defaultId, disabled, onChange }: {
  format: FilterFormat
  profiles: FilterProfile[]
  value: number | undefined
  defaultId: number | undefined
  disabled?: boolean
  onChange: (id: number) => void
}) {
  // "(built-in)" as in the segmentation picker; the chip marks the org default.
  const options = profiles.map(p => ({ value: String(p.id), label: profileLabel(p), chip: p.org_default ? 'Org default' : undefined }))
  return (
    <>
      <span className="pl-3 text-sm text-gray-700 dark:text-gray-300" title={filterFormatHint(format)}>{FILTER_FORMAT_LABELS[format]}</span>
      <div className="flex items-center gap-1">
        <Combobox
          value={value != null ? String(value) : ''}
          onChange={v => { if (v && Number(v) !== value) onChange(Number(v)) }}
          options={options}
          placeholder={defaultId == null ? 'Loading…' : 'Default'}
          className="w-80"
          disabled={disabled}
          aria-label={`${FILTER_FORMAT_LABELS[format]} filter profile`}
          data-testid={`filter-profile-select-${format}`}
        />
        {value != null && (
          <OpenLinkButton to={filterProfilePath(value)} label={`Open ${FILTER_FORMAT_LABELS[format]} filter profile`}
            data-testid={`filter-profile-open-${format}`} />
        )}
      </div>
    </>
  )
}
