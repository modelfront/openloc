import type { SegmentationProfile } from '@/api'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { Combobox } from '@/components/ui/combobox'
import { HelpTip } from '@/components/ui/help-tip'
import { profileLabel } from '@/lib/segmentation'
import { OpenLinkButton } from '@/components/ui/open-link-button'
import { segmentationProfilePath } from '@/lib/asset-links'

/**
 * The File configurations card of the settings editor. Its one setting is
 * the segmentation profile that splits the files uploaded from now on.
 * `profiles` are the ones that suit the source language; a `value` outside
 * them is the stored profile, listed under `storedLabel` so the field still
 * names it. A failed list shows the error and a Retry instead of the field.
 */
export function FileConfigurationsSection({ profiles, value, storedLabel, missing, disabled, error, onRetry, onChange }: {
  profiles: SegmentationProfile[]
  value: number | null
  storedLabel?: string
  /** Why the field needs a pick, shown under it. */
  missing?: string
  disabled?: boolean
  error?: unknown
  onRetry?: () => void
  onChange: (id: number) => void
}) {
  const stored = value != null && !profiles.some(p => p.id === value)
  const options = [
    ...(stored ? [{ value: String(value), label: storedLabel ?? `Profile #${value}` }] : []),
    ...profiles.map(p => ({ value: String(p.id), label: profileLabel(p) })),
  ]
  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="file-configurations-section">
      <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">File configurations</h3>
      {error ? <LoadFailed what="segmentation profiles" error={error} onRetry={onRetry} /> : (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            Segmentation profile
            <HelpTip text="Decides how uploaded files are split into segments. The list shows the profiles that suit the source language. A change applies to files uploaded afterwards; files already uploaded keep their segments. Profiles are set up under File configurations → Segmentation profiles." />
          </span>
          <Combobox
            value={value != null ? String(value) : ''}
            onChange={v => { if (v && Number(v) !== value) onChange(Number(v)) }}
            options={options}
            placeholder="Pick a profile"
            className="w-64"
            disabled={disabled}
            aria-invalid={!!missing}
            data-testid="segmentation-profile-select"
          />
          {value != null && <OpenLinkButton to={segmentationProfilePath(value)} label="Open segmentation profile" data-testid="segmentation-profile-open" />}
          {missing && <p className="w-full text-xs text-destructive" role="alert" data-testid="segmentation-profile-missing">{missing}</p>}
        </div>
      )}
    </div>
  )
}
